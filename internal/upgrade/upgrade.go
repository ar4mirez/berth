// Package upgrade fetches, verifies and unpacks a berth release (#42). It does no installing or
// linking itself (internal/app does that through host.FS): it turns a release tag into a verified
// binary.
package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Repo is where berth is released.
const Repo = "ar4mirez/berth"

// Identity is what a release's signature certificate must name: berth's release workflow, run by
// GitHub Actions, at that release's tag (docs/releases.md).
func Identity(tag string) (subject, issuer string) {
	return "https://github.com/" + Repo + "/.github/workflows/release.yml@refs/tags/" + tag,
		"https://token.actions.githubusercontent.com"
}

// Verifier checks the Sigstore bundle signed over a release's checksums.txt.
type Verifier interface {
	Verify(ctx context.Context, checksums, bundle []byte, tag string) error
}

// Client fetches releases from the GitHub API (or a test server).
type Client struct {
	API  string // e.g. https://api.github.com/repos/ar4mirez/berth
	HTTP *http.Client
}

// NewClient is the client for berth's own releases.
func NewClient() *Client {
	return &Client{API: "https://api.github.com/repos/" + Repo, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// Release is a published release: its tag and its assets' download URLs by name.
type Release struct {
	Tag    string
	Assets map[string]string
}

// Get returns a release: the latest when tag is "".
func (c *Client) Get(ctx context.Context, tag string) (Release, error) {
	u := c.API + "/releases/latest"
	if tag != "" {
		u = c.API + "/releases/tags/" + tag
	}
	b, err := c.fetch(ctx, u, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	var r struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Release{}, fmt.Errorf("reading the release: %w", err)
	}
	rel := Release{Tag: r.TagName, Assets: map[string]string{}}
	for _, a := range r.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// Download fetches a release asset by name.
func (c *Client) Download(ctx context.Context, rel Release, name string) ([]byte, error) {
	u, ok := rel.Assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Tag, name)
	}
	return c.fetch(ctx, u, "application/octet-stream")
}

func (c *Client) fetch(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// Archive is the archive name for a version (without the v) and platform, as goreleaser names it.
func Archive(version, goos, goarch string) string {
	return fmt.Sprintf("berth_%s_%s_%s.tar.gz", version, goos, goarch)
}

// Fetch downloads a release's archive for this platform and returns its berth binary, after
// verifying the signature over checksums.txt and the archive's checksum. Nothing is returned
// unless both hold.
func Fetch(ctx context.Context, c *Client, v Verifier, rel Release, goos, goarch string) ([]byte, error) {
	name := Archive(strings.TrimPrefix(rel.Tag, "v"), goos, goarch)
	sums, err := c.Download(ctx, rel, "checksums.txt")
	if err != nil {
		return nil, err
	}
	bundle, err := c.Download(ctx, rel, "checksums.txt.sigstore.json")
	if err != nil {
		return nil, err
	}
	if err := v.Verify(ctx, sums, bundle, rel.Tag); err != nil {
		return nil, fmt.Errorf("the signature on %s's checksums.txt doesn't verify: %w", rel.Tag, err)
	}
	archive, err := c.Download(ctx, rel, name)
	if err != nil {
		return nil, err
	}
	if err := CheckSum(sums, name, archive); err != nil {
		return nil, err
	}
	return Extract(archive, "berth")
}

// CheckSum checks data against name's line in a checksums.txt ("<sha256>  <name>").
func CheckSum(sums []byte, name string, data []byte) error {
	got := sha256.Sum256(data)
	for _, l := range strings.Split(string(sums), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[1] == name {
			if f[0] != hex.EncodeToString(got[:]) {
				return fmt.Errorf("%s doesn't match its checksum", name)
			}
			return nil
		}
	}
	return fmt.Errorf("checksums.txt has no line for %s", name)
}

// Extract returns a file from a .tar.gz archive.
func Extract(archive []byte, file string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the archive has no %s", file)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == file && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
}
