// Package component contains individual components that can render themselves as HTML.
package component

import (
	"fmt"
	"time"

	"dmitri.shuralyov.com/service/change"
	"github.com/dustin/go-humanize"
	"github.com/shurcooL/htmlg"
	issuescomponent "github.com/shurcooL/issuesapp/component"
	"github.com/shurcooL/octiconssvg"
	"github.com/shurcooL/users"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Event is an event component.
type Event struct {
	Event change.TimelineItem
}

func (e Event) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <div class="list-entry event event-{{.Type}}">
	// 	{{.Icon}}
	// 	<div class="event-header">
	// 		{{render (avatar .Actor)}} {{render (user .Actor)}} {{.Text}} {{render (time .CreatedAt)}}
	// 	</div>
	// </div>

	div := htmlg.DivClass("event-header")
	htmlg.AppendChildren(div, Avatar{User: e.Event.Actor, Size: 16, inline: true}.Render()...)
	htmlg.AppendChildren(div, User{e.Event.Actor}.Render()...)
	div.AppendChild(htmlg.Text(" "))
	htmlg.AppendChildren(div, e.text()...)
	div.AppendChild(htmlg.Text(" "))
	htmlg.AppendChildren(div, Time{e.Event.CreatedAt}.Render()...)

	outerDiv := htmlg.DivClass("list-entry event",
		e.icon(),
		div,
	)
	return []*html.Node{outerDiv}
}

func (e Event) icon() *html.Node {
	var (
		icon            *html.Node
		color           = "#767676"
		backgroundColor = "#f3f3f3"
	)
	switch p := e.Event.Payload.(type) {
	case change.ClosedEvent:
		icon = octiconssvg.CircleSlash()
		color, backgroundColor = "#fff", "#bd2c00"
	case change.ReopenedEvent:
		icon = octiconssvg.PrimitiveDot()
		color, backgroundColor = "#fff", "#6cc644"
	case change.RenamedEvent:
		icon = octiconssvg.Pencil()
	case change.LabeledEvent, change.UnlabeledEvent:
		icon = octiconssvg.Tag()
	case change.ReviewRequestedEvent:
		icon = octiconssvg.Eye()
	case change.ReviewRequestRemovedEvent:
		icon = octiconssvg.X()
	case change.MergedEvent:
		icon = octiconssvg.GitMerge()
		color, backgroundColor = "#fff", "#6f42c1"
	case change.DeletedEvent:
		switch p.Type {
		case "branch":
			icon = octiconssvg.GitBranch()
			color, backgroundColor = "#fff", "#767676"
		case "comment":
			icon = octiconssvg.X()
		default:
			panic("unreachable")
		}
	case change.ApprovedEvent:
		icon = octiconssvg.Check()
		color, backgroundColor = "#fff", "#6cc644"
	case change.ChangesRequestedEvent:
		icon = octiconssvg.X()
		color, backgroundColor = "#fff", "#bd2c00"
	default:
		icon = octiconssvg.PrimitiveDot()
	}
	return &html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr: []html.Attribute{
			{Key: atom.Class.String(), Val: "event-icon"},
			{Key: atom.Style.String(), Val: fmt.Sprintf("color: %s; background-color: %s;", color, backgroundColor)},
		},
		FirstChild: icon,
	}
}

func (e Event) text() []*html.Node {
	switch p := e.Event.Payload.(type) {
	case change.ClosedEvent:
		return []*html.Node{htmlg.Text("closed this")}
	case change.ReopenedEvent:
		return []*html.Node{htmlg.Text("reopened this")}
	case change.RenamedEvent:
		return []*html.Node{htmlg.Text("changed the title from "), htmlg.Strong(p.From), htmlg.Text(" to "), htmlg.Strong(p.To)}
	case change.LabeledEvent:
		var ns []*html.Node
		ns = append(ns, htmlg.Text("added the "))
		ns = append(ns, issuescomponent.Label{Label: p.Label}.Render()...)
		ns = append(ns, htmlg.Text(" label"))
		return ns
	case change.UnlabeledEvent:
		var ns []*html.Node
		ns = append(ns, htmlg.Text("removed the "))
		ns = append(ns, issuescomponent.Label{Label: p.Label}.Render()...)
		ns = append(ns, htmlg.Text(" label"))
		return ns
	case change.ReviewRequestedEvent:
		ns := []*html.Node{htmlg.Text("requested a review from ")}
		ns = append(ns, Avatar{User: p.RequestedReviewer, Size: 16, inline: true}.Render()...)
		ns = append(ns, User{p.RequestedReviewer}.Render()...)
		return ns
	case change.ReviewRequestRemovedEvent:
		ns := []*html.Node{htmlg.Text("removed the review request from ")}
		ns = append(ns, Avatar{User: p.RequestedReviewer, Size: 16, inline: true}.Render()...)
		ns = append(ns, User{p.RequestedReviewer}.Render()...)
		return ns
	case change.MergedEvent:
		var ns []*html.Node
		ns = append(ns, htmlg.Text("merged commit "))
		ns = append(ns, htmlg.Strong(p.CommitID)) // TODO: Code{}, use CommitHTMLURL.
		ns = append(ns, htmlg.Text(" into "))
		ns = append(ns, htmlg.Strong(p.RefName)) // TODO: Code{}.
		return ns
	case change.DeletedEvent:
		switch p.Type {
		case "branch":
			var ns []*html.Node
			ns = append(ns, htmlg.Text("deleted the "))
			ns = append(ns, htmlg.Strong(p.Name)) // TODO: Code{}.
			ns = append(ns, htmlg.Text(" branch"))
			return ns
		case "comment":
			return []*html.Node{htmlg.Text("deleted a comment")}
		default:
			panic("unreachable")
		}
	case change.ApprovedEvent:
		return []*html.Node{htmlg.Text("approved this change")}
	case change.ChangesRequestedEvent:
		return []*html.Node{htmlg.Text("requested changes")}
	default:
		return []*html.Node{htmlg.Text("unknown event")} // TODO: See if this is optimal.
	}
}

// ChangeStateBadge is a component that displays the state of a change
// with a badge, who opened it, and when it was opened.
type ChangeStateBadge struct {
	Change change.Change
}

func (i ChangeStateBadge) Render() []*html.Node {
	var ns []*html.Node
	ns = append(ns, ChangeBadge{State: i.Change.State}.Render()...)
	span := &html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr: []html.Attribute{
			{Key: atom.Style.String(), Val: "margin-left: 4px;"},
		},
	}
	htmlg.AppendChildren(span, User{i.Change.Author}.Render()...)
	span.AppendChild(htmlg.Text(" opened this change "))
	htmlg.AppendChildren(span, Time{i.Change.CreatedAt}.Render()...)
	ns = append(ns, span)
	return ns
}

// ChangeBadge is a change badge, displaying the change's state.
type ChangeBadge struct {
	State change.State
}

func (cb ChangeBadge) Render() []*html.Node {
	var (
		icon  *html.Node
		text  string
		color string
	)
	switch cb.State {
	case change.OpenState:
		icon = octiconssvg.GitPullRequest()
		text = "Open"
		color = "#6cc644"
	case change.ClosedState:
		icon = octiconssvg.GitPullRequest()
		text = "Closed"
		color = "#bd2c00"
	case change.MergedState:
		icon = octiconssvg.GitMerge()
		text = "Merged"
		color = "#6f42c1"
	default:
		return []*html.Node{htmlg.Text(string(cb.State))}
	}
	span := &html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr: []html.Attribute{{
			Key: atom.Style.String(),
			Val: `display: inline-block;
padding: 4px 6px 4px 6px;
margin: 4px;
color: #fff;
background-color: ` + color + `;`,
		}},
	}
	span.AppendChild(&html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr:       []html.Attribute{{Key: atom.Style.String(), Val: "margin-right: 6px;"}},
		FirstChild: icon,
	})
	span.AppendChild(htmlg.Text(text))
	return []*html.Node{span}
}

// ChangeIcon is a change icon, displaying the change's state.
type ChangeIcon struct {
	State change.State
}

func (ii ChangeIcon) Render() []*html.Node {
	// TODO: Make this much nicer.
	// {{if eq . "open"}}
	// 	<span style="margin-right: 6px; color: #6cc644;" class="octicon octicon-issue-opened"></span>
	// {{else if eq . "closed"}}
	// 	<span style="margin-right: 6px; color: #bd2c00;" class="octicon octicon-issue-closed"></span>
	// {{end}}
	var (
		icon  *html.Node
		color string
	)
	switch ii.State {
	case change.OpenState:
		icon = octiconssvg.GitPullRequest()
		color = "#6cc644"
	case change.ClosedState:
		icon = octiconssvg.GitPullRequest()
		color = "#bd2c00"
	case change.MergedState:
		icon = octiconssvg.GitMerge()
		color = "#6f42c1"
	}
	span := &html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr: []html.Attribute{{
			Key: atom.Style.String(),
			Val: `margin-right: 6px;
color: ` + color + `;`,
		}},
		FirstChild: icon,
	}
	return []*html.Node{span}
}

// User is a user component.
type User struct {
	User users.User
}

func (u User) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <a class="black" href="{{.HTMLURL}}"><strong>{{.Login}}</strong></a>
	a := &html.Node{
		Type: html.ElementNode, Data: atom.A.String(),
		Attr: []html.Attribute{
			{Key: atom.Class.String(), Val: "black"},
			{Key: atom.Href.String(), Val: u.User.HTMLURL},
		},
		FirstChild: htmlg.Strong(u.User.Login),
	}
	return []*html.Node{a}
}

// Avatar is an avatar component.
type Avatar struct {
	User   users.User
	Size   int  // In pixels, e.g., 48.
	inline bool // inline is experimental; so keep it contained to this package only for now.
}

func (a Avatar) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <a style="..." href="{{.User.HTMLURL}}" tabindex=-1>
	// 	<img style="..." width="{{.Size}}" height="{{.Size}}" src="{{.User.AvatarURL}}">
	// </a>
	imgStyle := "border-radius: 3px;"
	if a.inline {
		imgStyle += " vertical-align: middle; margin-right: 4px;"
	}
	return []*html.Node{{
		Type: html.ElementNode, Data: atom.A.String(),
		Attr: []html.Attribute{
			{Key: atom.Style.String(), Val: "display: inline-block;"},
			{Key: atom.Href.String(), Val: a.User.HTMLURL},
			{Key: atom.Tabindex.String(), Val: "-1"},
		},
		FirstChild: &html.Node{
			Type: html.ElementNode, Data: atom.Img.String(),
			Attr: []html.Attribute{
				{Key: atom.Style.String(), Val: imgStyle},
				{Key: atom.Width.String(), Val: fmt.Sprint(a.Size)},
				{Key: atom.Height.String(), Val: fmt.Sprint(a.Size)},
				{Key: atom.Src.String(), Val: a.User.AvatarURL},
			},
		},
	}}
}

// Time component that displays human friendly relative time (e.g., "2 hours ago", "yesterday"),
// but also contains a tooltip with the full absolute time (e.g., "Jan 2, 2006, 3:04 PM MST").
//
// TODO: Factor out, it's the same as in notificationsapp.
type Time struct {
	Time time.Time
}

func (t Time) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <abbr title="{{.Format "Jan 2, 2006, 3:04 PM MST"}}">{{reltime .}}</abbr>
	abbr := &html.Node{
		Type: html.ElementNode, Data: atom.Abbr.String(),
		Attr:       []html.Attribute{{Key: atom.Title.String(), Val: t.Time.Format("Jan 2, 2006, 3:04 PM MST")}},
		FirstChild: htmlg.Text(humanize.Time(t.Time)),
	}
	return []*html.Node{abbr}
}
