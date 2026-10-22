package main

// One path, one owner.
//
// This file is the console server's route table, and it exists because the
// server used to have two of them that disagreed.
//
// The SHOWCASE (the demo: vanilla pages under web/static) and the CONSOLE (the
// React SPA under web/spa) both wanted to be "/". The SPA won, because it was
// registered last, and it won with a catch-all for a file that is not in the
// repository: web/static/spa/index.html is build output and is gitignored. So
// every path of the five-minute judge track - /, /live, /pipeline, /trust,
// /census, /operations, /lab, /ask, /chief - returned 404 with an empty body,
// while the pages themselves sat in the tree, complete, unreachable.
//
// The fix is not "register the SPA later" or "serve the pages from disk as
// well". It is ONE table, one owner per path, and a test that fails when two
// owners appear or one goes missing.
//
// The split is:
//
//	/            the showcase hub - the front door is the demo
//	/live ...    the acts of docs/showcase/narration-judges.md
//	/console     the console (the React SPA) and every path under it
//
// The console lives under a prefix so that the two surfaces can never claim
// the same path again, and so that a judge who lands on the root gets the
// demo rather than the product.

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// The showcase's assets are compiled into the binary.
//
// The pattern names the files rather than the directory, because the directory
// once held the SPA's build output: a pattern of `static` would have embedded
// whatever was lying in the working copy at build time, which is exactly the
// kind of "it worked on my machine" this file is trying to remove.
//
//go:embed static/*.html static/*.css static/*.js static/*.json static/*.png static/fonts/*
var staticFS embed.FS

// The console's own directory: a committed placeholder at the root, and the
// bundle under dist/ when `npm run build` has been run in web/spa.
//
//go:embed consoleui
var consoleFS embed.FS

// showcasePage is one page of the demo and the file that renders it.
type showcasePage struct {
	Path string
	File string
}

// showcasePages is the demo's route table.
//
// Every entry names a file in web/static. The list is THE list: there is no
// second copy of it in a launcher script, in a comment or in a template, and
// TestEveryShowcasePageIsServed walks this table and fails when a file is
// missing, when a path is claimed twice, or when a page exists in the tree
// that nothing serves.
//
// `/fleet` was in this list until it was found to call four endpoint families
// - /api/fleet/*, /api/operator/*, /api/search/chat - that exist nowhere in
// this repository. A page whose data layer was never written is a page that
// renders four empty tables and four console errors, so it was deleted rather
// than shipped. The gate below is what stops the next one.
var showcasePages = []showcasePage{
	{Path: "/", File: "hub.html"},
	{Path: "/live", File: "live.html"},
	{Path: "/pipeline", File: "pipeline.html"},
	{Path: "/trust", File: "trust.html"},
	{Path: "/census", File: "census.html"},
	{Path: "/operations", File: "operations.html"},
	{Path: "/lab", File: "lab.html"},
	{Path: "/ask", File: "ask.html"},
	{Path: "/chief", File: "chief.html"},
}

// demoActs are the URLs the judge track visits, in the order it visits them.
//
// They are the rehearsal's list: scripts/demo-rehearsal.ps1 fetches every one
// of them and fails if any is not a 200 with an HTML body. The list lives
// here, next to the table that owns them, and the rehearsal reads it from the
// running server's own route table rather than from a second copy in a
// script.
var demoActs = []string{
	"/", "/live", "/pipeline", "/trust", "/census", "/operations", "/lab", "/ask", "/chief",
}

// consolePrefix is where the console (the React SPA) lives.
//
// The SPA's own route table is web/spa/src/components/Shell.tsx, and it is the
// only list of console routes that exists: the server serves the shell for
// every path under this prefix, and the client decides what the path means.
// The Go side keeps no copy of the client's routes, so the two cannot drift.
const consolePrefix = "/console"

// consoleShellPath is where the built bundle's index.html lands.
const consoleShellPath = "consoleui/dist/index.html"

// consolePlaceholderPath is the committed page served when the bundle has not
// been built. It says what to run, in one line, instead of answering 404 with
// an empty body.
const consolePlaceholderPath = "consoleui/index.html"

// registerPageRoutes installs the two page owners on a mux.
//
// The root space belongs to the showcase's dispatcher and the console's space
// belongs to the console; nothing else in the server answers a page. The API,
// the proxies and the static files are registered by their own files and are
// more specific patterns, so they win - which is the point of putting the
// dispatcher at "/" rather than registering each page separately.
func registerPageRoutes(mux *http.ServeMux) {
	mux.HandleFunc(consolePrefix, serveConsole())
	mux.HandleFunc(consolePrefix+"/", serveConsole())
	mux.Handle("/", serveShowcase())
}

// serveShowcase answers the root space: the demo's pages, and an honest 404
// for everything else.
//
// The 404 lists the pages that DO exist. A judge who mistypes a path is
// better served by "there is no /pipelne; here is what there is" than by a
// bare 404, and a developer reading the demo's output is better served by the
// same list.
func serveShowcase() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		file, ok := showcaseFileFor(r.URL.Path)
		if !ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><title>VISION — not found</title>"+
				"<body style=\"font:14px/1.6 system-ui,sans-serif;background:#0a0c0f;color:#e8ecf1;padding:40px\">"+
				"<h1 style=\"font-size:20px\">No page at %s</h1><p style=\"color:#8b95a5\">The pages this server serves:</p><ul>",
				template.HTMLEscapeString(r.URL.Path))
			for _, p := range showcasePages {
				_, _ = fmt.Fprintf(w, "<li><a style=\"color:#4c9be8\" href=\"%s\">%s</a></li>", p.Path, p.Path)
			}
			_, _ = fmt.Fprint(w, "<li><a style=\"color:#4c9be8\" href=\"/console\">/console</a></li></ul></body>")
			return
		}
		serveEmbedded(w, r, "static/"+file)
	}
}

// showcaseFileFor is the table lookup. Exported to the test, which is the
// point: the test and the server read the same table.
func showcaseFileFor(urlPath string) (string, bool) {
	for _, p := range showcasePages {
		if p.Path == urlPath {
			return p.File, true
		}
	}
	return "", false
}

// serveConsole answers the console's space.
//
// A path under the prefix is either one of the bundle's own files - the
// JavaScript, the CSS, the icons - or a client-side route, in which case the
// shell is served and the client router takes it from there. The shell is
// served for the prefix itself as well, so /console is not a redirect and
// there is no second URL for the same page.
func serveConsole() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, consolePrefix)
		rest = strings.TrimPrefix(rest, "/")
		if rest != "" {
			if body, ok := consoleAsset(rest); ok {
				serveBytes(w, r, rest, body)
				return
			}
		}
		body, built := consoleShell()
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write(body)
			return
		}
		serveBytes(w, r, "index.html", body)
	}
}

// consoleShell returns the shell to serve and whether it is the real bundle.
//
// The false is the important half: a machine that has never run `npm run build`
// gets a page that says so and names the command, not a 404 and not an empty
// document that looks like a broken product.
func consoleShell() ([]byte, bool) {
	if body, err := consoleFS.ReadFile(consoleShellPath); err == nil {
		return body, true
	}
	body, err := consoleFS.ReadFile(consolePlaceholderPath)
	if err != nil {
		log.Printf("console: neither the built bundle nor the placeholder is present: %v", err)
		return []byte("<!doctype html><meta charset=\"utf-8\"><title>VISION</title>" +
			"<body style=\"background:#0a0c0f;color:#e8ecf1;font:14px system-ui\">The console has not been built. " +
			"Run <code>npm ci &amp;&amp; npm run build</code> in <code>web/spa</code>.</body>"), false
	}
	return body, false
}

// consoleAsset reads one file of the console bundle.
//
// The bundle's files live under dist/; the committed placeholder's own
// stylesheet lives at the root. Both are embedded, so the lookup works
// whether or not the bundle was built.
func consoleAsset(name string) ([]byte, bool) {
	if !fs.ValidPath(name) {
		return nil, false
	}
	if body, err := consoleFS.ReadFile(path.Join("consoleui/dist", name)); err == nil {
		return body, true
	}
	if body, err := consoleFS.ReadFile(path.Join("consoleui", name)); err == nil {
		return body, true
	}
	return nil, false
}

// serveEmbedded serves one file of the showcase's assets.
func serveEmbedded(w http.ResponseWriter, r *http.Request, name string) {
	body, err := staticFS.ReadFile(name)
	if err != nil {
		log.Printf("console: asset %s is missing from the binary: %v", name, err)
		http.Error(w, "asset missing from this build", http.StatusInternalServerError)
		return
	}
	serveBytes(w, r, name, body)
}

// serveBytes writes one file with the content type its name implies.
//
// The modification time is the zero time rather than the build time: the
// bytes are compiled into the binary, and a modification time that says when
// the binary was built is a claim about a thing nobody asked about.
func serveBytes(w http.ResponseWriter, r *http.Request, name string, body []byte) {
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

// staticSub is the showcase's asset tree, rooted at the directory the embed
// named, so that a file server serves it under /static/.
func staticSub() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("console: the embedded assets are not usable: %v", err)
	}
	return sub
}
