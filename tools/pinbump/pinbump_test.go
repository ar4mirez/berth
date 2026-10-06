package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recorded serves the responses in testdata/, by URL; edits replaces a body, as a tampered
// upstream would.
type recorded struct {
	t     *testing.T
	edits map[string]func([]byte) []byte
}

var routes = map[string]string{
	githubAPI + "/repos/cli/cli/releases/latest":                                     "github/cli_cli/release.json",
	"https://github.com/cli/cli/releases/download/v2.102.0/gh_2.102.0_checksums.txt": "github/cli_cli/gh_2.102.0_checksums.txt",
	githubAPI + "/repos/jdx/mise/releases/latest":                                    "github/jdx_mise/release.json",
	"https://github.com/jdx/mise/releases/download/v2026.10.1/SHASUMS256.txt":        "github/jdx_mise/SHASUMS256.txt",
	githubAPI + "/repos/tsl0922/ttyd/releases/latest":                                "github/tsl0922_ttyd/release.json",
	"https://github.com/tsl0922/ttyd/releases/download/1.7.8/SHA256SUMS":             "github/tsl0922_ttyd/SHA256SUMS",
	npmRegistry + "/-/package/@anthropic-ai%2Fclaude-code/dist-tags":                 "npm/dist-tags.json",
	npmRegistry + "/@anthropic-ai%2Fclaude-code/2.1.285":                             "npm/version.json",
	npmRegistry + "/-/npm/v1/keys":                                                   "npm/keys.json",
}

func (r recorded) Get(_ context.Context, u string) ([]byte, error) {
	f, ok := routes[u]
	if !ok {
		r.t.Fatalf("unexpected GET %s", u)
	}
	b, err := os.ReadFile(filepath.Join("testdata", f))
	if err != nil {
		return nil, err
	}
	if e := r.edits[u]; e != nil {
		b = e(b)
	}
	return b, nil
}

func replace(old, new string) func([]byte) []byte {
	return func(b []byte) []byte { return bytes.Replace(b, []byte(old), []byte(new), 1) }
}

var now = time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// The real image/Dockerfile: four pins, each with exactly the ARGs its spec knows, and every
// pinned value well-formed.
func TestDockerfilePins(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := ParsePins(src)
	if err != nil {
		t.Fatal(err)
	}
	var deps []string
	for _, p := range pins {
		deps = append(deps, p.Dep)
		var want []string
		if s, ok := githubSpecs[p.Dep]; ok {
			want = append(want, s.version)
			for a := range s.assets {
				want = append(want, a)
			}
		} else if s, ok := npmSpecs[p.Dep]; ok {
			want = []string{s.version, s.integrity}
		}
		if err := sameArgs(p, want); err != nil {
			t.Error(err)
		}
		for _, a := range p.Args {
			if strings.Contains(a.Name, "SHA256") && len(a.Value) != 64 {
				t.Errorf("%s=%q isn't a sha256", a.Name, a.Value)
			}
		}
	}
	if got := strings.Join(deps, " "); got != "@anthropic-ai/claude-code tsl0922/ttyd jdx/mise cli/cli" {
		t.Errorf("pins: %s", got)
	}
}

func pin(t *testing.T, dep string, args ...string) Pin {
	t.Helper()
	p := Pin{Dep: dep, Datasource: "github-releases"}
	if strings.HasPrefix(dep, "@") {
		p.Datasource = "npm"
	}
	for i := 0; i < len(args); i += 2 {
		p.Args = append(p.Args, Arg{args[i], args[i+1]})
	}
	return p
}

var (
	ghPin = func(t *testing.T) Pin {
		return pin(t, "cli/cli", "GH_VERSION", "v2.101.0", "GH_SHA256_AMD64", "a", "GH_SHA256_ARM64", "b")
	}
	misePin = func(t *testing.T) Pin {
		return pin(t, "jdx/mise", "MISE_VERSION", "v2026.9.14", "MISE_SHA256_AMD64", "a", "MISE_SHA256_ARM64", "b")
	}
	ttydPin = func(t *testing.T) Pin {
		return pin(t, "tsl0922/ttyd", "TTYD_VERSION", "1.7.7", "TTYD_SHA256_AMD64", "a", "TTYD_SHA256_ARM64", "b")
	}
	ccPin = func(t *testing.T) Pin {
		return pin(t, "@anthropic-ai/claude-code", "CLAUDE_CODE_PINNED", "2.1.281", "CLAUDE_CODE_INTEGRITY", "sha512-x")
	}
)

func TestResolve(t *testing.T) {
	for _, c := range []struct {
		pin    Pin
		to     string
		set    map[string]string
		checks string
	}{
		{ghPin(t), "v2.102.0", map[string]string{"GH_VERSION": "v2.102.0",
			"GH_SHA256_AMD64": hashOf("gh-amd64"), "GH_SHA256_ARM64": hashOf("gh-arm64")}, "each one matches the digest GitHub computed"},
		{misePin(t), "v2026.10.1", map[string]string{"MISE_VERSION": "v2026.10.1",
			"MISE_SHA256_AMD64": hashOf("mise-x64"), "MISE_SHA256_ARM64": hashOf("mise-arm64")}, "each one matches the digest GitHub computed"},
		{ttydPin(t), "1.7.8", map[string]string{"TTYD_VERSION": "1.7.8",
			"TTYD_SHA256_AMD64": hashOf("ttyd-x86_64"), "TTYD_SHA256_ARM64": hashOf("ttyd-aarch64")}, "GitHub has no digest"},
		{ccPin(t), "2.1.285", map[string]string{"CLAUDE_CODE_PINNED": "2.1.285",
			"CLAUDE_CODE_INTEGRITY": "sha512-frr0DLmVHSDNjw+hC6ZmXMVQ8yH4nNmVcI4lVFzWt0bdPNZP7clOKsuLcqSdwQabIpe6A3NEc3TJfiBVD8B2Bg=="},
			"verifies with npm's published key SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U"},
	} {
		b, err := Resolve(context.Background(), recorded{t: t}, c.pin, now)
		if err != nil {
			t.Errorf("%s: %v", c.pin.Dep, err)
			continue
		}
		if b.To != c.to || fmt.Sprint(b.Set) != fmt.Sprint(c.set) {
			t.Errorf("%s: to %s, set %v; want %s, %v", c.pin.Dep, b.To, b.Set, c.to, c.set)
		}
		if !strings.Contains(strings.Join(b.Checks, "\n"), c.checks) {
			t.Errorf("%s: checks %q", c.pin.Dep, b.Checks)
		}
	}
}

// Already the latest (or newer, a pin set by hand): nothing to bump.
func TestResolveUpToDate(t *testing.T) {
	for _, p := range []Pin{
		pin(t, "cli/cli", "GH_VERSION", "v2.102.0", "GH_SHA256_AMD64", "a", "GH_SHA256_ARM64", "b"),
		pin(t, "tsl0922/ttyd", "TTYD_VERSION", "1.7.10", "TTYD_SHA256_AMD64", "a", "TTYD_SHA256_ARM64", "b"),
		pin(t, "@anthropic-ai/claude-code", "CLAUDE_CODE_PINNED", "2.1.285", "CLAUDE_CODE_INTEGRITY", "sha512-x"),
	} {
		b, err := Resolve(context.Background(), recorded{t: t}, p, now)
		if err != nil || len(b.Set) != 0 || b.To != b.From {
			t.Errorf("%s: %v, %+v", p.Dep, err, b)
		}
	}
}

// A tampered upstream is refused: the checksum file disagrees with GitHub's digest of an asset or
// of itself, the file lacks an asset or is malformed, or npm's integrity isn't what the registry signed.
func TestResolveRefuses(t *testing.T) {
	const (
		ghSums   = "https://github.com/cli/cli/releases/download/v2.102.0/gh_2.102.0_checksums.txt"
		miseSums = "https://github.com/jdx/mise/releases/download/v2026.10.1/SHASUMS256.txt"
		ttydSums = "https://github.com/tsl0922/ttyd/releases/download/1.7.8/SHA256SUMS"
		ccVer    = npmRegistry + "/@anthropic-ai%2Fclaude-code/2.1.285"
	)
	evil := hashOf("evil")
	for _, c := range []struct {
		name  string
		pin   Pin
		edits map[string]func([]byte) []byte
		want  string
	}{
		{"a mise checksum that isn't GitHub's digest", misePin(t),
			map[string]func([]byte) []byte{miseSums: replace(hashOf("mise-x64"), evil)}, "but GitHub's digest of it is"},
		{"a gh checksum file that isn't GitHub's digest of it", ghPin(t),
			map[string]func([]byte) []byte{ghSums: replace(hashOf("gh-amd64"), evil)}, "but GitHub's digest is"},
		{"no checksum for an asset", ttydPin(t),
			map[string]func([]byte) []byte{ttydSums: replace("ttyd.x86_64", "ttyd.x86")}, "has no checksum for ttyd.x86_64"},
		{"one asset, two sums", ttydPin(t),
			map[string]func([]byte) []byte{ttydSums: func(b []byte) []byte { return append(b, []byte(evil+"  ttyd.x86_64\n")...) }}, "listed twice"},
		{"not a sha256", ttydPin(t),
			map[string]func([]byte) []byte{ttydSums: replace(hashOf("ttyd-x86_64"), "deadbeef")}, "isn't a sha256"},
		{"a release without the downloaded asset", ttydPin(t),
			map[string]func([]byte) []byte{githubAPI + "/repos/tsl0922/ttyd/releases/latest": replace(`"name": "ttyd.aarch64"`, `"name": "ttyd.arm"`)}, "no asset ttyd.aarch64"},
		{"a prerelease", ttydPin(t),
			map[string]func([]byte) []byte{githubAPI + "/repos/tsl0922/ttyd/releases/latest": replace(`"prerelease": false`, `"prerelease": true`)}, "prerelease"},
		{"an integrity the registry didn't sign", ccPin(t),
			map[string]func([]byte) []byte{ccVer: replace("sha512-frr0", "sha512-frr1")}, "no registry signature"},
		{"a signature by an unknown key", ccPin(t),
			map[string]func([]byte) []byte{ccVer: func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("SHA256:DhQ8"), []byte("SHA256:XXXX")) }}, "no registry signature"},
		{"another package's answer", ccPin(t),
			map[string]func([]byte) []byte{ccVer: replace(`"version": "2.1.285"`, `"version": "2.1.284"`)}, "the registry answered for"},
		{"a Dockerfile pin with an unknown ARG", pin(t, "cli/cli", "GH_VERSION", "v2.101.0", "GH_SHA256_AMD64", "a"), nil, "tools/pinbump knows"},
	} {
		_, err := Resolve(context.Background(), recorded{t: t, edits: c.edits}, c.pin, now)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

// An expired npm key verifies nothing published after it expired.
func TestResolveExpiredKey(t *testing.T) {
	keys := npmRegistry + "/-/npm/v1/keys"
	r := recorded{t: t, edits: map[string]func([]byte) []byte{keys: func(b []byte) []byte {
		return bytes.Replace(b, []byte(`"expires": null`), []byte(`"expires": "2026-01-01T00:00:00.000Z"`), 1)
	}}}
	if _, err := Resolve(context.Background(), r, ccPin(t), now); err == nil || !strings.Contains(err.Error(), "no registry signature") {
		t.Errorf("an expired key: %v", err)
	}
}

func TestApply(t *testing.T) {
	src := "FROM x\n# pin: datasource=github-releases depName=cli/cli\nARG GH_VERSION=v1\nARG GH_SHA256_AMD64=old\nARG GH_VERSION_NOTE=v1\nRUN echo GH_VERSION=v1\n"
	got, err := Apply([]byte(src), map[string]string{"GH_VERSION": "v2", "GH_SHA256_AMD64": "new"})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.NewReplacer("ARG GH_VERSION=v1", "ARG GH_VERSION=v2", "=old", "=new").Replace(src); string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, err := Apply([]byte(src), map[string]string{"GH_SHA256_ARM64": "x"}); err == nil {
		t.Error("an ARG that isn't there was accepted")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v2.102.0", "v2.101.0", true}, {"v2.101.0", "v2.101.0", false}, {"v2.99.0", "v2.101.0", false},
		{"v2026.10.1", "v2026.9.14", true}, {"1.7.10", "1.7.7", true}, {"2.1.285", "2.1.281", true},
		{"2.2.0", "2.1.999", true}, {"1.7.7.1", "1.7.7", true}, {"1.7", "1.7.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestProposal(t *testing.T) {
	b, err := Resolve(context.Background(), recorded{t: t}, ghPin(t), now)
	if err != nil {
		t.Fatal(err)
	}
	pr := Proposal(b)
	if pr.Branch != "pinbump/cli" || pr.Title != "image: bump cli to v2.102.0" {
		t.Errorf("%+v", pr)
	}
	for _, s := range []string{"`v2.101.0` to `v2.102.0`", "| `GH_SHA256_AMD64` | `" + hashOf("gh-amd64") + "` |",
		"at its next `berth restart`", "merging this restarts nothing"} {
		if !strings.Contains(pr.Body, s) {
			t.Errorf("the PR body lacks %q:\n%s", s, pr.Body)
		}
	}
}
