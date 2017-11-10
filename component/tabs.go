package component

import (
	"fmt"
	"net/url"

	"github.com/shurcooL/htmlg"
	"github.com/shurcooL/octiconssvg"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// IssuesNav is a navigation component for displaying a header for a list of changes.
// It contains tabs to switch between viewing open and closed changes.
type IssuesNav struct {
	OpenCount     uint64     // Open changes count.
	ClosedCount   uint64     // Closed changes count.
	Path          string     // URL path of current page (needed to generate correct links).
	Query         url.Values // URL query of current page (needed to generate correct links).
	StateQueryKey string     // Name of query key for controlling change state filter. Constant, but provided externally.
}

func (n IssuesNav) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <header class="list-entry-header">
	// 	<nav>{{.Tabs}}</nav>
	// </header>
	nav := &html.Node{Type: html.ElementNode, Data: atom.Nav.String()}
	htmlg.AppendChildren(nav, n.tabs()...)
	header := &html.Node{
		Type: html.ElementNode, Data: atom.Header.String(),
		Attr:       []html.Attribute{{Key: atom.Class.String(), Val: "list-entry-header"}},
		FirstChild: nav,
	}
	return []*html.Node{header}
}

// tabs renders the HTML nodes for <nav> element with tab header links.
func (n IssuesNav) tabs() []*html.Node {
	selectedTabName := n.Query.Get(n.StateQueryKey)
	var ns []*html.Node
	for i, tab := range []struct {
		Name      string // Tab name corresponds to its state filter query value.
		Component htmlg.Component
	}{
		// Note: The routing logic (i.e., exact tab Name values) is duplicated with tabStateFilter.
		//       Might want to try to factor it out into a common location (e.g., a route package or so).
		{Name: "", Component: OpenIssuesTab{Count: n.OpenCount}},
		{Name: "closed", Component: ClosedIssuesTab{Count: n.ClosedCount}},
	} {
		tabURL := (&url.URL{
			Path:     n.Path,
			RawQuery: n.rawQuery(tab.Name),
		}).String()
		a := &html.Node{
			Type: html.ElementNode, Data: atom.A.String(),
			Attr: []html.Attribute{
				{Key: atom.Href.String(), Val: tabURL},
			},
		}
		if tab.Name == selectedTabName {
			a.Attr = append(a.Attr, html.Attribute{Key: atom.Class.String(), Val: "selected"})
		}
		if i > 0 {
			a.Attr = append(a.Attr, html.Attribute{Key: atom.Style.String(), Val: "margin-left: 12px;"})
		}
		htmlg.AppendChildren(a, tab.Component.Render()...)
		ns = append(ns, a)
	}
	return ns
}

// rawQuery returns the raw query for a link pointing to tabName.
func (n IssuesNav) rawQuery(tabName string) string {
	q := n.Query
	if tabName == "" {
		q.Del(n.StateQueryKey)
		return q.Encode()
	}
	q.Set(n.StateQueryKey, tabName)
	return q.Encode()
}

// OpenIssuesTab is an "Open Issues Tab" component.
type OpenIssuesTab struct {
	Count uint64 // Count of open changes.
}

func (t OpenIssuesTab) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <span style="margin-right: 4px;">{{octicon "git-pull-request"}}</span>
	// {{.Count}} Open
	icon := &html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr: []html.Attribute{
			{Key: atom.Style.String(), Val: "margin-right: 4px;"},
		},
		FirstChild: octiconssvg.GitPullRequest(),
	}
	text := htmlg.Text(fmt.Sprintf("%d Open", t.Count))
	return []*html.Node{icon, text}
}

// ClosedIssuesTab is a "Closed Issues Tab" component.
type ClosedIssuesTab struct {
	Count uint64 // Count of closed changes.
}

func (t ClosedIssuesTab) Render() []*html.Node {
	// TODO: Make this much nicer.
	// <span style="margin-right: 4px;">{{octicon "check"}}</span>
	// {{.Count}} Closed
	icon := &html.Node{
		Type: html.ElementNode, Data: atom.Span.String(),
		Attr: []html.Attribute{
			{Key: atom.Style.String(), Val: "margin-right: 4px;"},
		},
		FirstChild: octiconssvg.Check(),
	}
	text := htmlg.Text(fmt.Sprintf("%d Closed", t.Count))
	return []*html.Node{icon, text}
}
