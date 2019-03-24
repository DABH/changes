// changesdev is a sample program that serves changes.
//
// E.g., try http://localhost:8080/changes/33158.
package main

/*
Notes:

https://godoc.org/github.com/andygrunwald/go-gerrit
https://gerrit-review.googlesource.com/Documentation/rest-api.html
https://gerrit-review.googlesource.com/Documentation/rest-api-changes.html#list-changes
https://gerrit-review.googlesource.com/Documentation/user-search.html#_search_operators
https://review.openstack.org/Documentation/config-hooks.html#_comment_added

Go CL <-> PR doc: https://docs.google.com/document/d/131IKF-SHY8cLwpXhkR46HVrKiIYJztZml3YyqECwrgk/edit

small Go CL:
-	http://localhost:8080/changes/92456
-	https://go-review.googlesource.com/c/go/+/92456

go-review / build / 80840 is a good simple CL with inline comments, replies, few revisions

another small one is go-review / debug / 92416:
-	https://go-review.googlesource.com/c/debug/+/92416/1
-	https://go-review.googlesource.com/c/debug/+/92416/1/gocore/dwarf.go#297
-	http://localhost:8080/changes/92416
-	http://localhost:8080/changes/92416/files/5f8a8d64c594b92bbd2a5b0735a25a91c2ffdbb3
*/

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"dmitri.shuralyov.com/app/changes"
	"dmitri.shuralyov.com/service/change"
	"dmitri.shuralyov.com/service/change/fs"
	"dmitri.shuralyov.com/service/change/gerritapi"
	"dmitri.shuralyov.com/service/change/githubapi"
	"dmitri.shuralyov.com/service/change/httphandler"
	"dmitri.shuralyov.com/service/change/httproute"
	"dmitri.shuralyov.com/service/change/maintner"
	"github.com/andygrunwald/go-gerrit"
	githubv3 "github.com/google/go-github/github"
	"github.com/gregjones/httpcache"
	"github.com/shurcooL/githubv4"
	"github.com/shurcooL/home/httputil"
	"github.com/shurcooL/httpgzip"
	"github.com/shurcooL/reactions/emojis"
	"github.com/shurcooL/users"
	ghusers "github.com/shurcooL/users/githubapi"
	"golang.org/x/build/maintner/godata"
	"golang.org/x/oauth2"
)

var httpFlag = flag.String("http", ":8080", "Listen for HTTP connections on this address.")

func main() {
	flag.Parse()

	var usersService users.Service
	var service change.Service
	switch 2 {
	case 0:
		// Perform GitHub API authentication with provided token.
		token := os.Getenv("CHANGES_GITHUB_TOKEN")
		if token == "" {
			log.Fatalln("CHANGES_GITHUB_TOKEN env var is empty")
		}
		cacheTransport := &httpcache.Transport{
			Transport: &oauth2.Transport{
				Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
			},
			Cache:               httpcache.NewMemoryCache(),
			MarkCachedResponses: true,
		}
		httpClient := &http.Client{Transport: cacheTransport}
		ghV3 := githubv3.NewClient(httpClient)
		ghV4 := githubv4.NewClient(httpClient)

		var err error
		usersService, err = ghusers.NewService(ghV3)
		if err != nil {
			log.Fatalln("ghusers.NewService:", err)
		}
		service = githubapi.NewService(ghV3, ghV4, nil)

	case 1:
		cacheTransport := httpcache.NewMemoryCacheTransport()
		gerrit, err := gerrit.NewClient("https://go-review.googlesource.com/", &http.Client{Transport: cacheTransport})
		//gerrit, err := gerrit.NewClient("https://upspin-review.googlesource.com/", &http.Client{Transport: cacheTransport})
		if err != nil {
			log.Fatalln(err)
		}

		service = gerritapi.NewService(gerrit)

	case 2:
		corpus, err := godata.Get(context.Background())
		if err != nil {
			log.Fatalln(err)
		}

		service = maintner.NewService(corpus)

	case 3:
		service = &fs.Service{}
	}

	// Register HTTP API endpoints.
	apiHandler := httphandler.Change{Change: service}
	http.Handle(httproute.EditComment, httputil.ErrorHandler(usersService, apiHandler.EditComment))

	changesOpt := changes.Options{
		HeadPre: `<meta name="viewport" content="width=device-width">
<style type="text/css">
	body {
		margin: 20px;
		font-family: Go;
		font-size: 14px;
		line-height: initial;
		color: #373a3c;
	}
	.btn {
		font-size: 11px;
		line-height: 11px;
		border-radius: 4px;
		border: solid #d2d2d2 1px;
		background-color: #fff;
		box-shadow: 0 1px 1px rgba(0, 0, 0, .05);
	}
</style>`,
	}
	changesApp := changes.New(service, usersService, changesOpt)

	issuesHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		prefixLen := len("/changes")
		if prefix := req.URL.Path[:prefixLen]; req.URL.Path == prefix+"/" {
			baseURL := prefix
			if req.URL.RawQuery != "" {
				baseURL += "?" + req.URL.RawQuery
			}
			http.Redirect(w, req, baseURL, http.StatusFound)
			return
		}
		req.URL.Path = req.URL.Path[prefixLen:]
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/google/go-github"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/dustin/go-humanize"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/neugram/ng"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/golang/scratch"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/bradleyfalzon/ghinstallation"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/golang/gddo"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/avelino/awesome-go"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/travis-ci/travis-build"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/primer/octicons"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "github.com/golang/tools"))
		req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "go.googlesource.com/go"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "go.googlesource.com/tools"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "go.googlesource.com/build"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "upspin.googlesource.com/upspin"))
		//req = req.WithContext(context.WithValue(req.Context(), changes.RepoSpecContextKey, "dmitri.shuralyov.com/font/woff2"))
		req = req.WithContext(context.WithValue(req.Context(), changes.BaseURIContextKey, "/changes"))
		changesApp.ServeHTTP(w, req)
	})
	http.Handle("/changes", issuesHandler)
	http.Handle("/changes/", issuesHandler)

	http.HandleFunc("/login/github", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "Sorry, this is just a demo instance and it doesn't support signing in.")
	})

	emojisHandler := httpgzip.FileServer(emojis.Assets, httpgzip.FileServerOptions{ServeError: httpgzip.Detailed})
	http.Handle("/emojis/", http.StripPrefix("/emojis", emojisHandler))

	printServingAt(*httpFlag)
	err := http.ListenAndServe(*httpFlag, nil)
	if err != nil {
		log.Fatalln("ListenAndServe:", err)
	}
}

func printServingAt(addr string) {
	hostPort := addr
	if strings.HasPrefix(hostPort, ":") {
		hostPort = "localhost" + hostPort
	}
	fmt.Printf("serving at http://%s/\n", hostPort)
}
