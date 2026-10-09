// Package web is berth's web UI (#167): a dashboard in the browser, as files in the binary.
//
// It is a client of the API (internal/api) and nothing more: every view is a document the API
// returns, and every action one of its endpoints, so --read-only, a token's scope, `confirm` and
// the audit log hold here as for any caller. The files carry no data and are served without a
// token; what they show needs one.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:static
var static embed.FS

// Policy is the Content-Security-Policy every file is served with: scripts, styles and requests
// from this origin only, nothing inline, and no page may frame it.
const Policy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// kinds are the types the files are served as: berth's own table, so a machine's mime.types can't
// change what a browser is told.
var kinds = map[string]string{
	".html": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8",
	".svg": "image/svg+xml",
}

type file struct {
	body []byte
	etag string
	kind string
}

// Files are the UI's files by path ("index.html", "app.js", …).
func Files() map[string][]byte {
	out := map[string][]byte{}
	_ = fs.WalkDir(static, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := static.ReadFile(p)
		out[strings.TrimPrefix(p, "static/")] = b
		return err
	})
	return out
}

// Handler serves the UI: "/" is the page, and every other path one of its files. A path that
// isn't one is 404.
func Handler() http.Handler {
	files := map[string]file{}
	for name, b := range Files() {
		if strings.HasSuffix(name, ".md") {
			continue // notes for whoever maintains the files
		}
		sum := sha256.Sum256(b)
		kind := kinds[path.Ext(name)]
		files["/"+name] = file{body: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, kind: kind}
	}
	files["/"] = files["/index.html"]
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f, ok := files[req.URL.Path]
		if !ok || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
			http.NotFound(w, req)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", Policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// Asked for again each time, and answered 304 until berth is upgraded: a page from one
		// version never runs against another's API.
		h.Set("Cache-Control", "no-cache")
		h.Set("ETag", f.etag)
		if f.kind != "" {
			h.Set("Content-Type", f.kind)
		}
		http.ServeContent(w, req, "", time.Time{}, bytes.NewReader(f.body))
	})
}
