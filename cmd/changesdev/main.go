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
*/

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"dmitri.shuralyov.com/changes"
	"dmitri.shuralyov.com/changes/app"
	"dmitri.shuralyov.com/changes/gerritapi"
	"dmitri.shuralyov.com/changes/githubapi"
	"dmitri.shuralyov.com/changes/maintner"
	"github.com/andygrunwald/go-gerrit"
	"github.com/google/go-github/github"
	"github.com/gregjones/httpcache"
	"github.com/shurcooL/githubql"
	"github.com/shurcooL/httpgzip"
	"github.com/shurcooL/reactions/emojis"
	"golang.org/x/build/maintner/godata"
	"golang.org/x/oauth2"
)

var httpFlag = flag.String("http", ":8080", "Listen for HTTP connections on this address.")

func main() {
	flag.Parse()

	var service changes.Service
	switch 2 {
	case 0:
		cacheTransport := httpcache.NewMemoryCacheTransport()
		gerrit, err := gerrit.NewClient("https://go-review.googlesource.com/", &http.Client{Transport: cacheTransport})
		//gerrit, err := gerrit.NewClient("https://upspin-review.googlesource.com/", &http.Client{Transport: cacheTransport})
		if err != nil {
			log.Fatalln(err)
		}

		service = gerritapi.NewService(gerrit)

	case 1:
		corpus, err := godata.Get(context.Background())
		if err != nil {
			log.Fatalln(err)
		}

		service = maintner.NewService(corpus)

	case 2:
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
		ghV3 := github.NewClient(httpClient)
		ghV4 := githubql.NewClient(httpClient)

		var err error
		service, err = githubapi.NewService(ghV3, ghV4, nil)
		if err != nil {
			log.Fatalln(err)
		}
	}

	changesOpt := changesapp.Options{
		HeadPre: `<style type="text/css">
	body {
		margin: 20px;
		font-family: "Helvetica Neue", Helvetica, Arial, sans-serif;
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
		DisableReactions: true,
	}
	changesApp := changesapp.New(service, nil, changesOpt)

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
		//req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, "go.googlesource.com/go"))
		//req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, "go.googlesource.com/tools"))
		//req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, "upspin.googlesource.com/upspin"))
		req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, "github.com/google/go-github"))
		//req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, "github.com/dustin/go-humanize"))
		//req = req.WithContext(context.WithValue(req.Context(), changesapp.RepoSpecContextKey, "github.com/neugram/ng"))
		req = req.WithContext(context.WithValue(req.Context(), changesapp.BaseURIContextKey, "/changes"))
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
