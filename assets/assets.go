// +build dev

package assets

import (
	"go/build"
	"log"
	"net/http"

	"github.com/shurcooL/go/gopherjs_http"
	"github.com/shurcooL/httpfs/union"
)

// Assets contains assets for issuesapp.
var Assets = union.New(map[string]http.FileSystem{
	"/script.js": gopherjs_http.Package("dmitri.shuralyov.com/changes/app/frontend"),
	"/assets":    http.Dir(importPathToDir("dmitri.shuralyov.com/changes/app/_data")),
})

func importPathToDir(importPath string) string {
	p, err := build.Import(importPath, "", build.FindOnly)
	if err != nil {
		log.Fatalln(err)
	}
	return p.Dir
}
