package changesapp

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/shurcooL/highlight_diff"
	"github.com/shurcooL/htmlg"
	"github.com/sourcegraph/annotate"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"sourcegraph.com/sourcegraph/go-diff/diff"
)

// TODO: Dedup.

// tabnav is a left-aligned horizontal row of tabs Primer CSS component.
//
// http://primercss.io/nav/#tabnav
type tabnav struct {
	Tabs []tab
}

func (t tabnav) Render() []*html.Node {
	nav := &html.Node{
		Type: html.ElementNode, Data: atom.Nav.String(),
		Attr: []html.Attribute{{Key: atom.Class.String(), Val: "tabnav-tabs"}},
	}
	for _, t := range t.Tabs {
		htmlg.AppendChildren(nav, t.Render()...)
	}
	return []*html.Node{htmlg.DivClass("tabnav", nav)}
}

// tab is a single tab entry within a tabnav.
type tab struct {
	Content  htmlg.Component
	URL      string
	Selected bool
}

func (t tab) Render() []*html.Node {
	aClass := "tabnav-tab"
	if t.Selected {
		aClass += " selected"
	}
	a := &html.Node{
		Type: html.ElementNode, Data: atom.A.String(),
		Attr: []html.Attribute{
			{Key: atom.Href.String(), Val: t.URL},
			{Key: atom.Class.String(), Val: aClass},
		},
	}
	htmlg.AppendChildren(a, t.Content.Render()...)
	return []*html.Node{a}
}

type contentCounter struct {
	Content htmlg.Component
	Count   int
}

func (cc contentCounter) Render() []*html.Node {
	var ns []*html.Node
	ns = append(ns, cc.Content.Render()...)
	ns = append(ns, htmlg.SpanClass("counter", htmlg.Text(fmt.Sprint(cc.Count))))
	return ns
}

// iconText is an icon with text on the right.
// Icon must be not nil.
type iconText struct {
	Icon func() *html.Node // Must be not nil.
	Text string
}

func (it iconText) Render() []*html.Node {
	icon := htmlg.Span(it.Icon())
	icon.Attr = append(icon.Attr, html.Attribute{
		Key: atom.Style.String(), Val: "margin-right: 4px;",
	})
	text := htmlg.Text(it.Text)
	return []*html.Node{icon, text}
}

// fileDiff represents a file diff for display purposes.
type fileDiff struct {
	*diff.FileDiff
}

func (f fileDiff) Title() (template.HTML, error) {
	old := strings.TrimPrefix(f.OrigName, "a/")
	new := strings.TrimPrefix(f.NewName, "b/")
	switch {
	case old != "/dev/null" && new != "/dev/null" && old == new: // Modified.
		return template.HTML(html.EscapeString(new)), nil
	case old != "/dev/null" && new != "/dev/null" && old != new: // Renamed.
		return template.HTML(html.EscapeString(old + " -> " + new)), nil
	case old == "/dev/null" && new != "/dev/null": // Added.
		return template.HTML(html.EscapeString(new)), nil
	case old != "/dev/null" && new == "/dev/null": // Removed.
		return template.HTML("<strikethrough>" + html.EscapeString(old) + "</strikethrough>"), nil
	default:
		return "", fmt.Errorf("unexpected *diff.FileDiff: %+v", f)
	}
}

func (f fileDiff) Diff() (template.HTML, error) {
	hunks, err := diff.PrintHunks(f.Hunks)
	if err != nil {
		return "", err
	}
	diff, err := highlightDiff(hunks)
	if err != nil {
		return "", err
	}
	return template.HTML(diff), nil
}

// highlightDiff highlights the src diff, returning the annotated HTML.
func highlightDiff(src []byte) ([]byte, error) {
	anns, err := highlight_diff.Annotate(src)
	if err != nil {
		return nil, err
	}

	lines := bytes.Split(src, []byte("\n"))
	lineStarts := make([]int, len(lines))
	var offset int
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		lineStarts[lineIndex] = offset
		offset += len(lines[lineIndex]) + 1
	}

	lastDel, lastIns := -1, -1
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		var lineFirstChar byte
		if len(lines[lineIndex]) > 0 {
			lineFirstChar = lines[lineIndex][0]
		}
		switch lineFirstChar {
		case '+':
			if lastIns == -1 {
				lastIns = lineIndex
			}
		case '-':
			if lastDel == -1 {
				lastDel = lineIndex
			}
		default:
			if lastDel != -1 || lastIns != -1 {
				if lastDel == -1 {
					lastDel = lastIns
				} else if lastIns == -1 {
					lastIns = lineIndex
				}

				beginOffsetLeft := lineStarts[lastDel]
				endOffsetLeft := lineStarts[lastIns]
				beginOffsetRight := lineStarts[lastIns]
				endOffsetRight := lineStarts[lineIndex]

				anns = append(anns, &annotate.Annotation{Start: beginOffsetLeft, End: endOffsetLeft, Left: []byte(`<span class="gd input-block">`), Right: []byte(`</span>`), WantInner: 0})
				anns = append(anns, &annotate.Annotation{Start: beginOffsetRight, End: endOffsetRight, Left: []byte(`<span class="gi input-block">`), Right: []byte(`</span>`), WantInner: 0})

				if '@' != lineFirstChar {
					//leftContent := string(src[beginOffsetLeft:endOffsetLeft])
					//rightContent := string(src[beginOffsetRight:endOffsetRight])
					// This is needed to filter out the "-" and "+" at the beginning of each line from being highlighted.
					// TODO: Still not completely filtered out.
					leftContent := ""
					for line := lastDel; line < lastIns; line++ {
						leftContent += "\x00" + string(lines[line][1:]) + "\n"
					}
					rightContent := ""
					for line := lastIns; line < lineIndex; line++ {
						rightContent += "\x00" + string(lines[line][1:]) + "\n"
					}

					var sectionSegments [2][]*annotate.Annotation
					highlight_diff.HighlightedDiffFunc(leftContent, rightContent, &sectionSegments, [2]int{beginOffsetLeft, beginOffsetRight})

					anns = append(anns, sectionSegments[0]...)
					anns = append(anns, sectionSegments[1]...)
				}
			}
			lastDel, lastIns = -1, -1
		}
	}

	sort.Sort(anns)

	out, err := annotate.Annotate(src, anns, template.HTMLEscape)
	if err != nil {
		return nil, err
	}

	return out, nil
}
