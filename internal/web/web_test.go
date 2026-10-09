package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func get(t *testing.T, method, path string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)
	return w
}

// TestServesTheFiles: the page at "/", each file under its name with a type a browser will run,
// and the headers that keep the page to its own origin.
func TestServesTheFiles(t *testing.T) {
	for path, kind := range map[string]string{
		"/": "text/html; charset=utf-8", "/index.html": "text/html; charset=utf-8", "/app.js": "text/javascript; charset=utf-8",
		"/app.css": "text/css; charset=utf-8", "/icon.svg": "image/svg+xml", "/vendor/alpine-csp.min.js": "text/javascript; charset=utf-8",
	} {
		w := get(t, "GET", path)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != kind || w.Body.Len() == 0 {
			t.Errorf("GET %s: %d %q, %d bytes", path, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
		for h, want := range map[string]string{
			"Content-Security-Policy": Policy, "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-cache",
		} {
			if got := w.Header().Get(h); got != want {
				t.Errorf("GET %s: %s is %q, want %q", path, h, got, want)
			}
		}
		etag := w.Header().Get("ETag")
		if again := get(t, "GET", path, "If-None-Match", etag); etag == "" || again.Code != http.StatusNotModified {
			t.Errorf("GET %s with its ETag %q: %d, want 304", path, etag, again.Code)
		}
	}
	for _, c := range [][2]string{{"GET", "/nope"}, {"GET", "/vendor/README.md"}, {"GET", "/static/app.js"}, {"GET", "/../web.go"}, {"POST", "/"}, {"DELETE", "/app.js"}} {
		if w := get(t, c[0], c[1]); w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c[0], c[1], w.Code)
		}
	}
}

// TestPolicy: scripts and styles from this origin only, nothing inline or evaluated, no framing.
func TestPolicy(t *testing.T) {
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(Policy, want) {
			t.Errorf("the policy lacks %q", want)
		}
	}
	for _, not := range []string{"unsafe-inline", "unsafe-eval", "*", "http:", "https:"} {
		if strings.Contains(Policy, not) {
			t.Errorf("the policy has %q", not)
		}
	}
}

var (
	// What a page loads: src and href attributes, and url() in a stylesheet.
	loads  = regexp.MustCompile(`\s(?:src|href)="([^"]*)"|url\(([^)]*)\)`)
	inline = regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>\s*[^<\s]|<style[\s>]|\sstyle="|\son[a-z]+="|javascript:`)
)

// TestLoadsNothingFromElsewhere: every file the page loads is one of its own, and nothing in it is
// inline (the policy would refuse it, and the page would break without saying so).
func TestLoadsNothingFromElsewhere(t *testing.T) {
	files := Files()
	for name, b := range files {
		if !strings.HasSuffix(name, ".html") && !strings.HasSuffix(name, ".css") {
			continue
		}
		for _, m := range loads.FindAllStringSubmatch(string(b), -1) {
			ref := strings.Trim(m[1]+m[2], `'" `)
			if strings.HasPrefix(ref, "#") { // a place in the page
				continue
			}
			if strings.Contains(ref, ":") || strings.HasPrefix(ref, "/") {
				t.Errorf("%s loads %q: not one of the UI's own files, by a relative path", name, ref)
			} else if _, ok := files[ref]; !ok {
				t.Errorf("%s loads %q, which isn't there", name, ref)
			}
		}
		if strings.HasSuffix(name, ".html") {
			if m := inline.FindString(string(b)); m != "" {
				t.Errorf("%s has something inline, which the policy refuses: %q", name, m)
			}
		}
	}
	// Nothing evaluated, nothing written as markup, and no storage that outlives the tab.
	app := string(files["app.js"])
	for _, not := range []string{"eval(", "new Function", "innerHTML", "document.cookie", "localStorage"} {
		if strings.Contains(app, not) {
			t.Errorf("app.js has %q", not)
		}
	}
}

// TestVendored: a vendored script is the published file, byte for byte (static/vendor/README.md).
func TestVendored(t *testing.T) {
	want := map[string]string{
		"vendor/alpine-csp.min.js": "0d18d7f8d7910e2e0212f0f056b12f50bebc3abb7d88d2f7c7cb4c336fe4519a",
	}
	files := Files()
	for name, b := range files {
		if !strings.HasPrefix(name, "vendor/") || strings.HasSuffix(name, ".md") {
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want[name] {
			t.Errorf("%s is %s, want %q: update the table in vendor/README.md and here together", name, got, want[name])
		}
	}
	for name := range want {
		if _, ok := files[name]; !ok {
			t.Errorf("%s is gone", name)
		}
	}
}
