package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// The console is a third thing.
//
// It is neither the product nor the VMS: it is the operator's window onto
// both. So the server that serves it has to be able to reach the product's API
// and the VMS's control surface, and it does that by PROXYING rather than by
// asking the browser to.
//
// The browser asking the VMS directly would mean a cross-origin request from a
// page on :8080 to a device on :8010, with the device's credential in the
// browser — a credential that then lives in every browser on the machine and in
// every proxy log between. The proxy keeps the credential on the server, where
// it belongs.
//
// The two upstreams are named by environment variable and default to the
// addresses the product and the emulator use out of the box:
//
//	SP_API_URL  the product's API          http://127.0.0.1:8443
//	SP_VMS_URL  the VMS emulator           http://127.0.0.1:8010
//
// A wrong upstream is a refused startup, not a page of errors: a console that
// renders its own chrome and then fails every fetch is a console that looks
// broken rather than one that says what it cannot reach.

// apiURL is the product's API.
func apiURL() string { return envOr("SP_API_URL", "http://127.0.0.1:8443") }

// vmsURL is the VMS emulator's control surface.
func vmsURL() string { return envOr("SP_VMS_URL", "http://127.0.0.1:8010") }

// registerProxies wires the console's routes to the two upstreams.
//
// `/v1/` is the product's API and its paths are passed through unchanged: the
// API's own routes begin with `/v1`, and the console's front end asks for
// exactly those.
//
// `/vms/` is the VMS's CONTROL surface, and its paths are rewritten: the
// emulator serves it under `/__control`, and the console asks for `/vms`. The
// rewrite is the whole reason the prefix is different — a path the platform
// owns and a path a device owns should not look alike.
func registerProxies() {
	api := proxyTo("/v1/", apiURL(), "", nil)
	vms := proxyTo("/vms/", vmsURL(), "/__control", func(r *http.Request) {
		// The VMS's credential stays on the server. The browser never sees it.
		if token := strings.TrimSpace(os.Getenv("SP_VMS_TOKEN")); token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	})
	http.Handle("/v1/", api)
	http.Handle("/vms/", vms)
}

// proxyTo builds a reverse proxy for one prefix.
//
// `rewrite` replaces the prefix in the outgoing path, and `decorate` gets the
// last word on the request before it goes out.
func proxyTo(prefix, target, rewrite string, decorate func(*http.Request)) http.Handler {
	base, err := url.Parse(target)
	if err != nil {
		log.Printf("console: the upstream %q is not a URL, so %s is not proxied: %v", target, prefix, err)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "console: this upstream is not configured", http.StatusBadGateway)
		})
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			rest := strings.TrimPrefix(pr.In.URL.Path, prefix)
			pr.Out.URL.Path = strings.TrimRight(rewrite+"/"+rest, "/")
			pr.Out.Host = base.Host
			if decorate != nil {
				decorate(pr.Out)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// The upstream's absence is reported as an upstream's absence, with
			// its address, because "the page is broken" is the least useful thing
			// a console can tell an operator.
			log.Printf("console: %s is unreachable: %v", base.String(), err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"` +
				"the " + base.String() + " upstream did not answer. Is it running?\"," +
				`"upstream":"` + base.String() + `"}`))
		},
	}
	return proxy
}

// envOr reads an environment variable or takes a default.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
