// gerritchanges is a sample program that serves Gerrit changes.
//
// E.g., try http://localhost:8080/changes/33158.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/andygrunwald/go-gerrit"
	"github.com/gorilla/mux"
	"github.com/gregjones/httpcache"
	"github.com/shurcooL/httpgzip"
	"github.com/shurcooL/issues"
	"github.com/shurcooL/issuesapp"
	"github.com/shurcooL/reactions/emojis"

	gerritissues "github.com/shurcooL/issues/gerritapi"
)

var httpFlag = flag.String("http", ":8080", "Listen for HTTP connections on this address.")

func main() {
	flag.Parse()

	cacheTransport := httpcache.NewMemoryCacheTransport()
	//gerrit, err := gerrit.NewClient("https://go-review.googlesource.com/", &http.Client{Transport: cacheTransport})
	gerrit, err := gerrit.NewClient("https://upspin-review.googlesource.com/", &http.Client{Transport: cacheTransport})
	if err != nil {
		log.Fatalln(err)
	}

	service := gerritissues.NewService(gerrit, nil)

	issuesOpt := issuesapp.Options{
		HeadPre: `<style type="text/css">
	body {
		margin: 20px;
		font-family: "Helvetica Neue", Helvetica, Arial, sans-serif;
		font-size: 14px;
		line-height: initial;
		color: #373a3c;
	}
	a {
		color: #0275d8;
		text-decoration: none;
	}
	a:focus, a:hover {
		color: #014c8c;
		text-decoration: underline;
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
	issuesApp := issuesapp.New(service, nil, issuesOpt)

	r := mux.NewRouter()

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
		req = req.WithContext(context.WithValue(req.Context(), issuesapp.RepoSpecContextKey, issues.RepoSpec{URI: "upspin"}))
		req = req.WithContext(context.WithValue(req.Context(), issuesapp.BaseURIContextKey, "/changes"))
		issuesApp.ServeHTTP(w, req)
	})
	r.Path("/changes").Handler(issuesHandler)
	r.PathPrefix("/changes/").Handler(issuesHandler)

	r.HandleFunc("/login/github", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "Sorry, this is just a demo instance and it doesn't support signing in.")
	})

	emojisHandler := httpgzip.FileServer(emojis.Assets, httpgzip.FileServerOptions{ServeError: httpgzip.Detailed})
	r.PathPrefix("/emojis/").Handler(http.StripPrefix("/emojis", emojisHandler))

	printServingAt(*httpFlag)
	err = http.ListenAndServe(*httpFlag, r)
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
