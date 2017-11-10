package changesapp

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

	"dmitri.shuralyov.com/changes"
	"dmitri.shuralyov.com/changes/app/assets"
	"dmitri.shuralyov.com/changes/app/common"
	"dmitri.shuralyov.com/changes/app/component"
	"github.com/dustin/go-humanize"
	"github.com/shurcooL/github_flavored_markdown"
	"github.com/shurcooL/htmlg"
	"github.com/shurcooL/httperror"
	"github.com/shurcooL/httpfs/html/vfstemplate"
	"github.com/shurcooL/httpgzip"
	"github.com/shurcooL/issues"
	"github.com/shurcooL/notifications"
	"github.com/shurcooL/octiconssvg"
	"github.com/shurcooL/reactions"
	reactionscomponent "github.com/shurcooL/reactions/component"
	"github.com/shurcooL/users"
	"golang.org/x/net/html"
	"sourcegraph.com/sourcegraph/go-diff/diff"
)

// TODO: Find a better way for changesapp to be able to ensure registration of a top-level route:
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
// 	changesApp := changesapp.New(...)
//
// 	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
// 		req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, string(...)))
// 		req = req.WithContext(context.WithValue(req.Context(), changesapp.BaseURIContextKey, string(...)))
// 		changesApp.ServeHTTP(w, req)
// 	})
func New(service changes.Service, users users.Service, opt Options) http.Handler {
	static, err := loadTemplates(common.State{}, opt.BodyPre)
	if err != nil {
		log.Fatalln("loadTemplates failed:", err)
	}
	h := handler{
		is:               service,
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

// RepoSpecContextKey is a context key for the request's issues.RepoSpec.
// That value specifies which repo the issues are to be displayed for.
// The associated value will be of type string.
var RepoSpecContextKey = &contextKey{"RepoSpec"}

// BaseURIContextKey is a context key for the request's base URI.
// That value specifies the base URI prefix to use for all absolute URLs.
// The associated value will be of type string.
var BaseURIContextKey = &contextKey{"BaseURI"}

// Options for configuring issues app.
type Options struct {
	Notifications    notifications.Service // If not nil, issues containing unread notifications are highlighted.
	DisableReactions bool                  // Disable all support for displaying and toggling reactions.

	HeadPre, HeadPost template.HTML
	BodyPre           string // An html/template definition of "body-pre" template.

	// BodyTop provides components to include on top of <body> of page rendered for req. It can be nil.
	BodyTop func(req *http.Request) ([]htmlg.Component, error)
}

// handler handles all requests to changesapp. It acts like a request multiplexer,
// choosing from various endpoints and parsing the repository ID from URL.
type handler struct {
	is changes.Service
	us users.Service // May be nil if there's no users service.

	assetsFileServer http.Handler
	gfmFileServer    http.Handler

	// static is loaded once in New, and is only for rendering templates that don't use state.
	static *template.Template

	Options
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) error {
	if _, ok := req.Context().Value(RepoSpecContextKey).(string); !ok {
		return fmt.Errorf("request to %v doesn't have changesapp.RepoSpecContextKey context key set", req.URL.Path)
	}
	if _, ok := req.Context().Value(BaseURIContextKey).(string); !ok {
		return fmt.Errorf("request to %v doesn't have changesapp.BaseURIContextKey context key set", req.URL.Path)
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
		return h.IssuesHandler(w, req)
	}

	// Handle "/{changeID}" and "/{changeID}/...".
	elems := strings.SplitN(req.URL.Path[1:], "/", 3)
	changeID, err := strconv.ParseUint(elems[0], 10, 64)
	if err != nil {
		return httperror.HTTP{Code: http.StatusNotFound, Err: fmt.Errorf("invalid issue ID %q: %v", elems[0], err)}
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

func (h *handler) IssuesHandler(w http.ResponseWriter, req *http.Request) error {
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
	is, err := h.is.List(req.Context(), state.RepoSpec, changes.ListOptions{State: filter})
	if err != nil {
		return err
	}
	openCount, err := h.is.Count(req.Context(), state.RepoSpec, changes.ListOptions{State: changes.StateFilter(changes.OpenState)})
	if err != nil {
		return fmt.Errorf("issues.Count(open): %v", err)
	}
	closedCount, err := h.is.Count(req.Context(), state.RepoSpec, changes.ListOptions{State: changes.StateFilter(changes.ClosedState)})
	if err != nil {
		return fmt.Errorf("issues.Count(closed): %v", err)
	}
	var es []component.ChangeEntry
	for _, i := range is {
		es = append(es, component.ChangeEntry{Change: i, BaseURI: state.BaseURI})
	}
	es = state.augmentUnread(req.Context(), es, h.is, h.Notifications)
	state.Changes = component.Issues{
		IssuesNav: component.IssuesNav{
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
	err = h.static.ExecuteTemplate(w, "issues.html.tmpl", &state)
	if err != nil {
		return fmt.Errorf("h.static.ExecuteTemplate: %v", err)
	}
	return nil
}

const (
	// stateQueryKey is name of query key for controlling issue state filter.
	stateQueryKey = "state"
)

// stateFilter parses the issue state filter from query,
// returning an error if the value is unsupported.
func stateFilter(query url.Values) (changes.StateFilter, error) {
	selectedTabName := query.Get(stateQueryKey)
	switch selectedTabName {
	case "":
		return changes.StateFilter(changes.OpenState), nil
	case "closed":
		return changes.StateFilter(changes.ClosedState), nil
	case "all":
		return changes.AllStates, nil
	default:
		return "", fmt.Errorf("unsupported state filter value: %q", selectedTabName)
	}
}

func (s state) augmentUnread(ctx context.Context, es []component.ChangeEntry, is changes.Service, notificationsService notifications.Service) []component.ChangeEntry {
	if notificationsService == nil {
		return es
	}

	tt, ok := is.(interface {
		ThreadType() string
	})
	if !ok {
		log.Println("augmentUnread: changes service doesn't implement ThreadType")
		return es
	}
	threadType := tt.ThreadType()

	if s.CurrentUser.ID == 0 {
		// Unauthenticated user cannot have any unread changes.
		return es
	}

	// TODO: Consider starting to do this in background in parallel with is.List.
	ns, err := notificationsService.List(ctx, notifications.ListOptions{
		Repo: &notifications.RepoSpec{URI: s.RepoSpec},
	})
	if err != nil {
		log.Println("augmentUnread: failed to notifications.List:", err)
		return es
	}

	unreadThreads := make(map[uint64]struct{}) // Set of unread thread IDs.
	for _, n := range ns {
		if n.ThreadType != threadType { // Assumes RepoSpec matches because we filtered via notifications.ListOptions.
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

func (h *handler) ChangeHandler(w http.ResponseWriter, req *http.Request, changeID uint64) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	state, err := h.state(req, changeID)
	if err != nil {
		return err
	}
	state.Change, err = h.is.Get(req.Context(), state.RepoSpec, state.IssueID)
	if err != nil {
		return err
	}
	cs, err := h.is.ListComments(req.Context(), state.RepoSpec, state.IssueID, nil)
	if err != nil {
		return fmt.Errorf("changes.ListComments: %v", err)
	}
	es, err := h.is.ListEvents(req.Context(), state.RepoSpec, state.IssueID, nil)
	if err != nil {
		return fmt.Errorf("changes.ListEvents: %v", err)
	}
	var items []issueItem
	for _, comment := range cs {
		items = append(items, issueItem{comment})
	}
	for _, event := range es {
		items = append(items, issueItem{event})
	}
	sort.Sort(byCreatedAtID(items))
	state.Items = items
	// Call loadTemplates to set updated reactionsBar, reactableID, etc., template functions.
	t, err := loadTemplates(state.State, h.Options.BodyPre)
	if err != nil {
		return fmt.Errorf("loadTemplates: %v", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = t.ExecuteTemplate(w, "issue.html.tmpl", &state)
	if err != nil {
		return fmt.Errorf("t.ExecuteTemplate: %v", err)
	}
	return nil
}

func (h *handler) ChangeCommitsHandler(w http.ResponseWriter, req *http.Request, changeID uint64) error {
	if req.Method != http.MethodGet {
		return httperror.Method{Allowed: []string{http.MethodGet}}
	}
	state, err := h.state(req, changeID)
	if err != nil {
		return err
	}
	state.Change, err = h.is.Get(req.Context(), state.RepoSpec, state.IssueID)
	if err != nil {
		return err
	}
	cs, err := h.is.ListCommits(req.Context(), state.RepoSpec, state.IssueID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = h.static.ExecuteTemplate(w, "change-commits.html.tmpl", &state)
	if err != nil {
		return err
	}
	var commits []Commit
	for _, c := range cs {
		commits = append(commits, Commit{Commit: c})
	}
	err = htmlg.RenderComponents(w, Commits{Commits: commits})
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
	state.Change, err = h.is.Get(req.Context(), state.RepoSpec, state.IssueID)
	if err != nil {
		return err
	}
	var commit commitMessage
	if commitID != "" {
		cs, err := h.is.ListCommits(req.Context(), state.RepoSpec, state.IssueID)
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
	}
	var opt *changes.GetDiffOptions
	if commitID != "" {
		opt = &changes.GetDiffOptions{Commit: commitID}
	}
	rawDiff, err := h.is.GetDiff(req.Context(), state.RepoSpec, state.IssueID, opt)
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
func commitIndex(cs []changes.Commit, commitID string) int {
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
		reqPath = "" // This is needed so that absolute URL for root view, i.e., /issues, is "/issues" and not "/issues/" because of "/issues" + "/".
	}
	b := state{
		State: common.State{
			BaseURI:  req.Context().Value(BaseURIContextKey).(string),
			ReqPath:  reqPath,
			RepoSpec: req.Context().Value(RepoSpecContextKey).(string),
			IssueID:  changeID,
		},
	}
	b.HeadPre = h.HeadPre
	b.HeadPost = h.HeadPost
	if h.BodyTop != nil {
		c, err := h.BodyTop(req)
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

	Changes component.Issues
	Change  changes.Change
	Items   []issueItem
}

func (s state) Tabnav(selected string) template.HTML {
	// Render the tabnav.
	return template.HTML(htmlg.RenderComponentsString(tabnav{
		Tabs: []tab{
			{
				Content:  iconText{Icon: octiconssvg.CommentDiscussion, Text: "Discussion"},
				URL:      fmt.Sprintf("%s/%d", s.BaseURI, s.IssueID),
				Selected: selected == "Discussion",
			},
			{
				Content: contentCounter{
					Content: iconText{Icon: octiconssvg.GitCommit, Text: "Commits"},
					Count:   s.Change.Commits,
				},
				URL:      fmt.Sprintf("%s/%d/commits", s.BaseURI, s.IssueID),
				Selected: selected == "Commits",
			},
			{
				Content:  iconText{Icon: octiconssvg.Diff, Text: "Files"},
				URL:      fmt.Sprintf("%s/%d/files", s.BaseURI, s.IssueID),
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
		"reactableID": func(commentID uint64) string {
			return fmt.Sprintf("%d/%d", state.IssueID, commentID)
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
			icon := octiconssvg.Icon(name)
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
		"event":            func(e issues.Event) htmlg.Component { return component.Event{Event: e} },
		"changeStateBadge": func(c changes.Change) htmlg.Component { return component.ChangeStateBadge{Change: c} },
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
	return "dmitri.shuralyov.com/changes/app context value " + k.name
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
