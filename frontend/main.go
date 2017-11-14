// frontend script for changesapp.
//
// It's a Go package meant to be compiled with GOARCH=js
// and executed in a browser, where the DOM is available.
package main

import (
	"github.com/gopherjs/gopherjs/js"
	"github.com/shurcooL/go/gopherjs_http/jsutil"
	"honnef.co/go/js/dom"
)

var document = dom.GetWindow().Document().(dom.HTMLDocument)

func main() {
	js.Global.Set("ToggleDetails", jsutil.Wrap(ToggleDetails))
}

func ToggleDetails(el dom.HTMLElement) {
	container := getAncestorByClassName(el, "commit-container").(dom.HTMLElement)
	details := container.QuerySelector("pre.commit-details").(dom.HTMLElement)

	switch details.Style().GetPropertyValue("display") {
	default:
		details.Style().SetProperty("display", "none", "")
	case "none":
		details.Style().SetProperty("display", "block", "")
	}
}

func getAncestorByClassName(el dom.Element, class string) dom.Element {
	for ; el != nil && !el.Class().Contains(class); el = el.ParentElement() {
	}
	return el
}
