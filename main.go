package changes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"dmitri.shuralyov.com/app/changes/assets"
	"dmitri.shuralyov.com/app/changes/common"
	"dmitri.shuralyov.com/app/changes/component"
	"dmitri.shuralyov.com/service/change"
	statepkg "dmitri.shuralyov.com/state"
	"github.com/dustin/go-humanize"
	"github.com/shurcooL/github_flavored_markdown"
	"github.com/shurcooL/htmlg"
	"github.com/shurcooL/httperror"
	"github.com/shurcooL/httpfs/html/vfstemplate"
	"github.com/shurcooL/httpgzip"
	"github.com/shurcooL/notifications"
	"github.com/shurcooL/octicon"
	"github.com/shurcooL/reactions"
	reactionscomponent "github.com/shurcooL/reactions/component"
	"github.com/shurcooL/users"
	"github.com/sourcegraph/go-diff/diff"
	"golang.org/x/net/html"
)

// TODO: Find a better way for changes to be able to ensure registration of a top-level route:
//
// 	emojisHandler := httpgzip.FileServer(emojis.Assets, httpgzip.FileServerOptions{ServeError: httpgzip.Detailed})
// 	http.Handle("/emojis/", http.StripPrefix("/emojis", emojisHandler))
//
// So that it can depend on it.

// New returns a changes app http.Handler using given services and options.
// If users is nil, then there is no way to have an authenticated user.
// Emojis image data is expected to be available at /emojis/emojis.png, unless
// opt.DisableReactions is true.
//
// In order to serve HTTP requests, the returned http.Handler expects each incoming
// request to have 2 parameters provided to it via RepoSpecContextKey and BaseURIContextKey
// context keys. For example:
//
// 	changesApp := changes.New(...)
//
// 	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
// 		req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, string(...)))
// 		req = req.WithContext(context.WithValue(req.Context(), changes.BaseURIContextKey, string(...)))
// 		changesApp.ServeHTTP(w, req)
// 	})
//
// An HTTP API must be available (currently, only EditComment endpoint is used):
//
// 	// Register HTTP API endpoints.
// 	apiHandler := httphandler.Change{Change: service}
// 	http.Handle(httproute.EditComment, errorHandler(apiHandler.EditComment))
func New(service change.Service, users users.Service, opt Options) http.Handler {
	static, err := loadTemplates(common.State{}, opt.BodyPre)
	if err != nil {
		log.Fatalln("loadTemplates failed:", err)
	}
	h := handler{
		cs:               service,
		us:               users,
		static:           static,
		assetsFileServer: httpgzip.FileServer(assets.Assets, httpgzip.FileServerOptions{ServeError: httpgzip.Detailed}),
		gfmFileServer:    httpgzip.FileServer(assets.GFMStyle, httpgzip.FileServerOptions{ServeError: httpgzip.Detailed}),
		Options:          opt,
	}
	return &errorHandler{
		handler: h.ServeHTTP,
		users:   users,
	}
}

// RepoSpecContextKey is a context key for the request's repo spec.
// That value specifies which repo the changes are to be displayed for.
// The associated value will be of type string.
var RepoSpecContextKey = &contextKey{"RepoSpec"}

// BaseURIContextKey is a context key for the request's base URI.
// That value specifies the base URI prefix to use for all absolute URLs.
// The associated value will be of type string.
var BaseURIContextKey = &contextKey{"BaseURI"}

// Options for configuring changes app.
type Options struct {
	// Notifications, if not nil, is used to highlight changes containing
	// unread notifications, and to mark changes that are viewed as read.
	Notifications notifications.Service

	DisableReactions bool // Disable all support for displaying and toggling reactions.

	HeadPre, HeadPost template.HTML
	BodyPre           string // An html/template definition of "body-pre" template.

	// BodyTop provides components to include on top of <body> of page rendered for req. It can be nil.
	BodyTop func(*http.Request, common.State) ([]htmlg.Component, error)
}

// handler handles all requests to changes. It acts like a request multiplexer,
// choosing from various endpoints and parsing the repository ID from URL.
type handler struct {
	cs change.Service
	us users.Service // May be nil if there's no users service.

	assetsFileServer http.Handler
	gfmFileServer    http.Handler

	// static is loaded once in New, and is only for rendering templates that don't use state.
	static *template.Template

	Options
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) error {
	if _, ok := req.Context().Value(RepoSpecContextKey).(string); !ok {
		return fmt.Errorf("request to %v doesn't have changes.RepoSpecContextKey context key set", req.URL.Path)
	}
	if _, ok := req.Context().Value(BaseURIContextKey).(string); !ok {
		return fmt.Errorf("request to %v doesn't have changes.BaseURIContextKey context key set", req.URL.Path)
	}

	// Handle "/assets/gfm/...".
	if strings.HasPrefix(req.URL.Path, "/assets/gfm/") {
		req = stripPrefix(req, len("/assets/gfm"))
		h.gfmFileServer.ServeHTTP(w, req)
		return nil
	}

	// Handle "/assets/script.js".
	if req.URL.Path == "/assets/script.js" {
		req = stripPrefix(req, len("/assets"))
		h.assetsFileServer.ServeHTTP(w, req)
		return nil
	}

	// Handle (the rest of) "/assets/...".
	if strings.HasPrefix(req.URL.Path, "/assets/") {
		h.assetsFileServer.ServeHTTP(w, req)
		return nil
	}

	// Handle "/".
	if req.URL.Path == "/" {
		return h.ChangesHandler(w, req)
	}

	// Handle "/mock".
	if req.URL.Path == "/mock" {
		return h.MockHandler(w, req)
	}

	// Handle "/{changeID}" and "/{changeID}/...".
	elems := strings.SplitN(req.URL.Path[1:], "/", 3)
	changeID, err := strconv.ParseUint(elems[0], 10, 64)
	if err != nil {
		return httperror.HTTP{Code: http.StatusNotFound, Err: fmt.Errorf("invalid change ID %q: %v", elems[0], err)}
	}
	switch {
	// "/{changeID}".
	case len(elems) == 1:
		return h.ChangeHandler(w, req, changeID)

	// "/{changeID}/commits".
	case len(elems) == 2 && elems[1] == "commits":
		return h.ChangeCommitsHandler(w, req, changeID)

	// "/{changeID}/files".
	case len(elems) == 2 && elems[1] == "files":
		return h.ChangeFilesHandler(w, req, changeID, "")

	// "/{changeID}/files/{commitID}".
	case len(elems) == 3 && elems[1] == "files":
		commitID := elems[2]
		return h.ChangeFilesHandler(w, req, changeID, commitID)

	default:
		return httperror.HTTP{Code: http.StatusNotFound, Err: errors.New("no route")}
	}
}

func (h *handler) ChangesHandler(w http.ResponseWriter, req *http.Request) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	state, err := h.state(req, 0)
	if err != nil {
		return err
	}
	filter, err := stateFilter(req.URL.Query())
	if err != nil {
		return httperror.BadRequest{Err: err}
	}
	is, err := h.cs.List(req.Context(), state.RepoSpec, change.ListOptions{Filter: filter})
	if err != nil {
		return err
	}
	openCount, err := h.cs.Count(req.Context(), state.RepoSpec, change.ListOptions{Filter: change.FilterOpen})
	if err != nil {
		return fmt.Errorf("changes.Count(open): %v", err)
	}
	closedCount, err := h.cs.Count(req.Context(), state.RepoSpec, change.ListOptions{Filter: change.FilterClosedMerged})
	if err != nil {
		return fmt.Errorf("changes.Count(closed): %v", err)
	}
	var es []component.ChangeEntry
	for _, i := range is {
		es = append(es, component.ChangeEntry{Change: i, BaseURI: state.BaseURI})
	}
	if h.Notifications != nil {
		es = state.augmentUnread(req.Context(), es, h.Notifications, h.cs)
	}
	state.Changes = component.Changes{
		ChangesNav: component.ChangesNav{
			OpenCount:     openCount,
			ClosedCount:   closedCount,
			Path:          state.BaseURI + state.ReqPath,
			Query:         req.URL.Query(),
			StateQueryKey: stateQueryKey,
		},
		Filter:  filter,
		Entries: es,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = h.static.ExecuteTemplate(w, "changes.html.tmpl", &state)
	if err != nil {
		return fmt.Errorf("h.static.ExecuteTemplate: %v", err)
	}
	return nil
}

const (
	// stateQueryKey is name of query key for controlling change state filter.
	stateQueryKey = "state"
)

// stateFilter parses the change state filter from query,
// returning an error if the value is unsupported.
func stateFilter(query url.Values) (change.StateFilter, error) {
	selectedTabName := query.Get(stateQueryKey)
	switch selectedTabName {
	case "":
		return change.FilterOpen, nil
	case "closed":
		return change.FilterClosedMerged, nil
	case "all":
		return change.FilterAll, nil
	default:
		return "", fmt.Errorf("unsupported state filter value: %q", selectedTabName)
	}
}

func (s state) augmentUnread(ctx context.Context, es []component.ChangeEntry, notificationService notifications.Service, changeService change.Service) []component.ChangeEntry {
	tt, ok := changeService.(interface {
		ThreadType(repo string) string
	})
	if !ok {
		log.Println("augmentUnread: change service doesn't implement ThreadType")
		return es
	}
	threadType := tt.ThreadType(s.RepoSpec)

	if s.CurrentUser.ID == 0 {
		// Unauthenticated user cannot have any unread changes.
		return es
	}

	// TODO: Consider starting to do this in background in parallel with is.List.
	ns, err := notificationService.List(ctx, notifications.ListOptions{
		Repo: &notifications.RepoSpec{URI: s.RepoSpec},
		All:  false,
	})
	if err != nil {
		log.Println("augmentUnread: failed to notifications.List:", err)
		return es
	}

	unreadThreads := make(map[uint64]struct{}) // Set of unread thread IDs.
	for _, n := range ns {
		// n.RepoSpec == s.RepoSpec is guaranteed because we filtered in notifications.ListOptions,
		// so we only need to check that n.ThreadType matches.
		if n.ThreadType != threadType {
			continue
		}
		unreadThreads[n.ThreadID] = struct{}{}
	}

	for i, e := range es {
		_, unread := unreadThreads[e.Change.ID]
		es[i].Unread = unread
	}
	return es
}

func (h *handler) MockHandler(w http.ResponseWriter, req *http.Request) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	st, err := h.state(req, 0)
	if err != nil {
		return err
	}
	t, err := loadTemplates(st.State, h.Options.BodyPre)
	if err != nil {
		return fmt.Errorf("loadTemplates: %v", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = t.ExecuteTemplate(w, "review-mock", struct {
		state
		Review change.Review
	}{
		state: st,
		Review: change.Review{
			ID:        "0",
			User:      users.User{Login: "Eric Grosse", AvatarURL: "https://lh6.googleusercontent.com/-_sdEtv2PRxk/AAAAAAAAAAI/AAAAAAAAAAA/aE1Q66Cuvb4/s100-p/photo.jpg"},
			CreatedAt: time.Now().UTC(),
			Edited:    nil,
			State:     statepkg.ReviewPlus2,
			Body:      "",
			Reactions: []reactions.Reaction{},
			Editable:  true,
			Comments: []change.InlineComment{
				{
					File: "rpc/keyserver/server.go",
					Line: 26,
					Body: "Ok by me, but how was this chosen?",
				},
				{
					File: "rpc/keyserver/server.go",
					Line: 31,
					Body: "As someone who reads the server logs, my gut feeling is that 1 QPS of Lookup logs will give me sufficient data to tell me the system is working, without creating a big mess.",
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("t.ExecuteTemplate: %v", err)
	}
	return nil
}

func (h *handler) ChangeHandler(w http.ResponseWriter, req *http.Request, changeID uint64) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	state, err := h.state(req, changeID)
	if err != nil {
		return err
	}
	state.Change, err = h.cs.Get(req.Context(), state.RepoSpec, state.ChangeID)
	if err != nil {
		return err
	}
	ts, err := h.cs.ListTimeline(req.Context(), state.RepoSpec, state.ChangeID, nil)
	if err != nil {
		return fmt.Errorf("changes.ListTimeline: %v", err)
	}
	if h.Notifications != nil {
		err := state.markRead(req.Context(), h.Notifications, h.cs)
		if err != nil {
			log.Println("ChangeHandler: failed to markRead:", err)
		}
	}
	var timeline []timelineItem
	for _, item := range ts {
		timeline = append(timeline, timelineItem{item})
	}
	sort.Sort(byCreatedAtID(timeline))
	state.Timeline = timeline
	// Call loadTemplates to set updated reactionsBar, reactableID, etc., template functions.
	t, err := loadTemplates(state.State, h.Options.BodyPre)
	if err != nil {
		return fmt.Errorf("loadTemplates: %v", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = t.ExecuteTemplate(w, "change.html.tmpl", &state)
	if err != nil {
		return fmt.Errorf("t.ExecuteTemplate: %v", err)
	}
	return nil
}

func (s state) markRead(ctx context.Context, notificationService notifications.Service, changeService change.Service) error {
	tt, ok := changeService.(interface {
		ThreadType(repo string) string
	})
	if !ok {
		log.Println("markRead: change service doesn't implement ThreadType")
		return nil
	}
	threadType := tt.ThreadType(s.RepoSpec)

	if s.CurrentUser.ID == 0 {
		// Unauthenticated user cannot mark anything as read.
		return nil
	}

	err := notificationService.MarkRead(ctx, notifications.RepoSpec{URI: s.RepoSpec}, threadType, s.ChangeID)
	return err
}

func (h *handler) ChangeCommitsHandler(w http.ResponseWriter, req *http.Request, changeID uint64) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	state, err := h.state(req, changeID)
	if err != nil {
		return err
	}
	state.Change, err = h.cs.Get(req.Context(), state.RepoSpec, state.ChangeID)
	if err != nil {
		return err
	}
	list, err := h.cs.ListCommits(req.Context(), state.RepoSpec, state.ChangeID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = h.static.ExecuteTemplate(w, "change-commits.html.tmpl", &state)
	if err != nil {
		return err
	}
	var cs []commit
	for _, c := range list {
		cs = append(cs, commit{Commit: c})
	}
	err = htmlg.RenderComponents(w, commits{Commits: cs})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, `</body></html>`)
	return err
}

// ChangeFilesHandler is the handler for "/{changeID}/files" and "/{changeID}/files/{commitID}" endpoints.
// commitID is empty string for all files, or the SHA of a single commit for single-commit view.
func (h *handler) ChangeFilesHandler(w http.ResponseWriter, req *http.Request, changeID uint64, commitID string) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	state, err := h.state(req, changeID)
	if err != nil {
		return err
	}
	state.Change, err = h.cs.Get(req.Context(), state.RepoSpec, state.ChangeID)
	if err != nil {
		return err
	}
	var commit commitMessage
	if commitID != "" {
		cs, err := h.cs.ListCommits(req.Context(), state.RepoSpec, state.ChangeID)
		if err != nil {
			return err
		}
		i := commitIndex(cs, commitID)
		if i == -1 {
			return os.ErrNotExist
		}
		subject, body := splitCommitMessage(cs[i].Message)
		commit = commitMessage{
			CommitHash: cs[i].SHA,
			Subject:    subject,
			Body:       body,
			Author:     cs[i].Author,
			AuthorTime: cs[i].AuthorTime,
		}
		if prev := i - 1; prev >= 0 {
			commit.PrevSHA = cs[prev].SHA
		}
		if next := i + 1; next < len(cs) {
			commit.NextSHA = cs[next].SHA
		}
		state.PrevSHA, state.NextSHA = commit.PrevSHA, commit.NextSHA
	}
	var opt *change.GetDiffOptions
	if commitID != "" {
		opt = &change.GetDiffOptions{Commit: commitID}
	}
	rawDiff, err := h.cs.GetDiff(req.Context(), state.RepoSpec, state.ChangeID, opt)
	if err != nil {
		return err
	}
	fileDiffs, err := diff.ParseMultiFileDiff(rawDiff)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = h.static.ExecuteTemplate(w, "change-files.html.tmpl", &state)
	if err != nil {
		return err
	}
	if commitID != "" {
		err = h.static.ExecuteTemplate(w, "CommitMessage", commit)
		if err != nil {
			return err
		}
	}
	for _, f := range fileDiffs {
		err = h.static.ExecuteTemplate(w, "FileDiff", fileDiff{FileDiff: f})
		if err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, `</body></html>`)
	return err
}

// commitIndex returns the index of commit with SHA equal to commitID,
// or -1 if not found.
func commitIndex(cs []change.Commit, commitID string) int {
	for i := range cs {
		if cs[i].SHA == commitID {
			return i
		}
	}
	return -1
}

func (h *handler) state(req *http.Request, changeID uint64) (state, error) {
	// TODO: Caller still does a lot of work outside to calculate req.URL.Path by
	//       subtracting BaseURI from full original req.URL.Path. We should be able
	//       to compute it here internally by using req.RequestURI and BaseURI.
	reqPath := req.URL.Path
	if reqPath == "/" {
		reqPath = "" // This is needed so that absolute URL for root view, i.e., /changes, is "/changes" and not "/changes/" because of "/changes" + "/".
	}
	b := state{
		State: common.State{
			BaseURI:  req.Context().Value(BaseURIContextKey).(string),
			ReqPath:  reqPath,
			RepoSpec: req.Context().Value(RepoSpecContextKey).(string),
			ChangeID: changeID,
		},
	}
	b.HeadPre = h.HeadPre
	b.HeadPost = h.HeadPost
	if h.BodyTop != nil {
		c, err := h.BodyTop(req, b.State)
		if err != nil {
			return state{}, err
		}
		var buf bytes.Buffer
		err = htmlg.RenderComponents(&buf, c...)
		if err != nil {
			return state{}, fmt.Errorf("htmlg.RenderComponents: %v", err)
		}
		b.BodyTop = template.HTML(buf.String())
	}

	b.DisableReactions = h.Options.DisableReactions
	b.DisableUsers = h.us == nil

	if h.us == nil {
		// No user service provided, so there can never be an authenticated user.
		b.CurrentUser = users.User{}
	} else if user, err := h.us.GetAuthenticated(req.Context()); err == nil {
		b.CurrentUser = user
	} else {
		return state{}, fmt.Errorf("h.us.GetAuthenticated: %v", err)
	}

	return b, nil
}

type state struct {
	HeadPre, HeadPost template.HTML
	BodyTop           template.HTML

	common.State

	Changes  component.Changes
	Change   change.Change
	Timeline []timelineItem
}

// Tabnav renders the tabnav.
func (s state) Tabnav(selected string) template.HTML {
	var files htmlg.Component = iconText{Icon: octicon.Diff, Text: "Files"}
	if s.Change.ChangedFiles != 0 {
		files = contentCounter{Content: files, Count: s.Change.ChangedFiles}
	}
	return template.HTML(htmlg.RenderComponentsString(tabnav{
		Tabs: []tab{
			{
				Content: contentCounter{
					Content: iconText{Icon: octicon.CommentDiscussion, Text: "Discussion"},
					Count:   s.Change.Replies,
				},
				URL:      fmt.Sprintf("%s/%d", s.BaseURI, s.ChangeID),
				Selected: selected == "Discussion",
			},
			{
				Content: contentCounter{
					Content: iconText{Icon: octicon.GitCommit, Text: "Commits"},
					Count:   s.Change.Commits,
				},
				URL:      fmt.Sprintf("%s/%d/commits", s.BaseURI, s.ChangeID),
				Selected: selected == "Commits",
			},
			{
				Content:  files,
				URL:      fmt.Sprintf("%s/%d/files", s.BaseURI, s.ChangeID),
				Selected: selected == "Files",
			},
		},
	}))
}

func loadTemplates(state common.State, bodyPre string) (*template.Template, error) {
	t := template.New("").Funcs(template.FuncMap{
		"json": func(v interface{}) (string, error) {
			b, err := json.Marshal(v)
			return string(b), err
		},
		"jsonfmt": func(v interface{}) (string, error) {
			b, err := json.MarshalIndent(v, "", "\t")
			return string(b), err
		},
		"reltime":          humanize.Time,
		"gfm":              func(s string) template.HTML { return template.HTML(github_flavored_markdown.Markdown([]byte(s))) },
		"reactionPosition": func(emojiID reactions.EmojiID) string { return reactions.Position(":" + string(emojiID) + ":") },
		"equalUsers": func(a, b users.User) bool {
			return a.UserSpec == b.UserSpec
		},
		"reactableID": func(commentID string) string {
			return fmt.Sprintf("%d/%s", state.ChangeID, commentID)
		},
		"reactionsBar": func(reactions []reactions.Reaction, reactableID string) htmlg.Component {
			return reactionscomponent.ReactionsBar{
				Reactions:   reactions,
				CurrentUser: state.CurrentUser,
				ID:          reactableID,
			}
		},
		"newReaction": func(reactableID string) htmlg.Component {
			return reactionscomponent.NewReaction{
				ReactableID: reactableID,
			}
		},
		"state": func() common.State { return state },

		"octicon": func(name string) (template.HTML, error) {
			icon := octicon.Icon(name)
			if icon == nil {
				return "", fmt.Errorf("%q is not a valid Octicon symbol name", name)
			}
			var buf bytes.Buffer
			err := html.Render(&buf, icon)
			if err != nil {
				return "", err
			}
			return template.HTML(buf.String()), nil
		},

		"render": func(c htmlg.Component) template.HTML {
			return template.HTML(htmlg.Render(c.Render()...))
		},
		"event":            func(e change.TimelineItem) htmlg.Component { return component.Event{Event: e} },
		"changeStateBadge": func(c change.Change) htmlg.Component { return component.ChangeStateBadge{Change: c} },
		"time":             func(t time.Time) htmlg.Component { return component.Time{Time: t} },
		"user":             func(u users.User) htmlg.Component { return component.User{User: u} },
		"avatar":           func(u users.User) htmlg.Component { return component.Avatar{User: u, Size: 48} },
	})
	t, err := vfstemplate.ParseGlob(assets.Assets, t, "/assets/*.tmpl")
	if err != nil {
		return nil, err
	}
	return t.New("body-pre").Parse(bodyPre)
}

// contextKey is a value for use with context.WithValue. It's used as
// a pointer so it fits in an interface{} without allocation.
type contextKey struct {
	name string
}

func (k *contextKey) String() string {
	return "dmitri.shuralyov.com/app/changes context value " + k.name
}

// stripPrefix returns request r with prefix of length prefixLen stripped from r.URL.Path.
// prefixLen must not be longer than len(r.URL.Path), otherwise stripPrefix panics.
// If r.URL.Path is empty after the prefix is stripped, the path is changed to "/".
func stripPrefix(r *http.Request, prefixLen int) *http.Request {
	r2 := new(http.Request)
	*r2 = *r
	r2.URL = new(url.URL)
	*r2.URL = *r.URL
	r2.URL.Path = r.URL.Path[prefixLen:]
	if r2.URL.Path == "" {
		r2.URL.Path = "/"
	}
	return r2
}
