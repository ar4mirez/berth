// Package upgradetest serves fake berth releases for tests of `berth upgrade` (#42).
package upgradetest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/upgrade"
)

// FakeRelease serves a release (the GitHub API's JSON and its assets) from an httptest server.
type FakeRelease struct {
	Tag      string
	Binary   []byte // the "berth" file in the archive
	Tamper   bool   // serve an archive that doesn't match checksums.txt
	Server   *httptest.Server
	Requests []string
}

func archiveOf(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("readme")}, {name, data}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Start serves the release; the archive is for goos/goarch.
func (f *FakeRelease) Start(t *testing.T, goos, goarch string) *upgrade.Client {
	t.Helper()
	name := upgrade.Archive(strings.TrimPrefix(f.Tag, "v"), goos, goarch)
	archive := archiveOf(t, "berth", f.Binary)
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if f.Tamper {
		archive = append(archive, 0)
	}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	assets := map[string][]byte{"checksums.txt": []byte(sums), "checksums.txt.sigstore.json": []byte(`{"fake":"bundle"}`), name: archive}
	release := func(w http.ResponseWriter, _ *http.Request) {
		var as []map[string]string
		for n := range assets {
			as = append(as, map[string]string{"name": n, "browser_download_url": f.Server.URL + "/dl/" + n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": f.Tag, "assets": as})
	}
	mux.HandleFunc("/releases/latest", release)
	mux.HandleFunc("/releases/tags/"+f.Tag, release)
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/dl/")
		f.Requests = append(f.Requests, n)
		b, ok := assets[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	return &upgrade.Client{API: f.Server.URL, HTTP: f.Server.Client()}
}

// FakeVerifier accepts or rejects every bundle, and records what it saw.
type FakeVerifier struct {
	Reject bool
	Tags   []string
}

func (v *FakeVerifier) Verify(_ context.Context, checksums, bundle []byte, tag string) error {
	v.Tags = append(v.Tags, tag)
	if v.Reject || len(checksums) == 0 || len(bundle) == 0 {
		return errors.New("bad signature")
	}
	return nil
}
