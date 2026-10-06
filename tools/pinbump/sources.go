package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Fetcher gets a URL's body: the network in the workflow, recorded responses in the tests.
type Fetcher interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// githubSpec says where a GitHub-released tool's checksums are: the release's own checksum file,
// and the asset each checksum ARG is for. In names, {tag} is the release tag and {v} the tag
// without a leading v. The asset names are the ones image/Dockerfile downloads.
type githubSpec struct {
	version  string
	sumsFile string
	assets   map[string]string // checksum ARG -> asset name
}

var githubSpecs = map[string]githubSpec{
	"cli/cli": {"GH_VERSION", "gh_{v}_checksums.txt",
		map[string]string{"GH_SHA256_AMD64": "gh_{v}_linux_amd64.tar.gz", "GH_SHA256_ARM64": "gh_{v}_linux_arm64.tar.gz"}},
	"jdx/mise": {"MISE_VERSION", "SHASUMS256.txt",
		map[string]string{"MISE_SHA256_AMD64": "mise-{tag}-linux-x64", "MISE_SHA256_ARM64": "mise-{tag}-linux-arm64"}},
	"tsl0922/ttyd": {"TTYD_VERSION", "SHA256SUMS",
		map[string]string{"TTYD_SHA256_AMD64": "ttyd.x86_64", "TTYD_SHA256_ARM64": "ttyd.aarch64"}},
}

// npmSpec: the version ARG, the integrity ARG, and the dist-tag followed. Claude Code follows
// `stable`, its slower channel, so a week's bump isn't whatever shipped last night.
type npmSpec struct{ version, integrity, distTag string }

var npmSpecs = map[string]npmSpec{
	"@anthropic-ai/claude-code": {"CLAUDE_CODE_PINNED", "CLAUDE_CODE_INTEGRITY", "stable"},
}

const (
	githubAPI   = "https://api.github.com"
	npmRegistry = "https://registry.npmjs.org"
)

// Bump is what a pin should become. Set is empty when the pin is already the latest.
type Bump struct {
	Dep, Datasource string
	From, To        string
	Set             map[string]string // ARG -> new value
	Release         string            // the release's page
	Checks          []string          // how the checksums were verified, for the PR
}

// Resolve finds a pin's latest version and its verified checksums.
func Resolve(ctx context.Context, f Fetcher, p Pin, now time.Time) (Bump, error) {
	var names []string
	switch p.Datasource {
	case "github-releases":
		s, ok := githubSpecs[p.Dep]
		if !ok {
			return Bump{}, fmt.Errorf("%s: no GitHub spec in tools/pinbump", p.Dep)
		}
		names = append(names, s.version)
		for a := range s.assets {
			names = append(names, a)
		}
		if err := sameArgs(p, names); err != nil {
			return Bump{}, err
		}
		return resolveGitHub(ctx, f, p, s)
	case "npm":
		s, ok := npmSpecs[p.Dep]
		if !ok {
			return Bump{}, fmt.Errorf("%s: no npm spec in tools/pinbump", p.Dep)
		}
		if err := sameArgs(p, []string{s.version, s.integrity}); err != nil {
			return Bump{}, err
		}
		return resolveNpm(ctx, f, p, s, now)
	}
	return Bump{}, fmt.Errorf("%s: unknown datasource %q", p.Dep, p.Datasource)
}

// sameArgs: the Dockerfile's pin block has exactly the ARGs the spec knows, so a renamed or added
// checksum fails here, not as a half-updated pin.
func sameArgs(p Pin, want []string) error {
	var got []string
	for _, a := range p.Args {
		got = append(got, a.Name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		return fmt.Errorf("%s: the Dockerfile pins %v, tools/pinbump knows %v", p.Dep, got, want)
	}
	return nil
}

type ghAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"` // "sha256:<hex>", GitHub's own; empty on assets uploaded before it existed
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

func resolveGitHub(ctx context.Context, f Fetcher, p Pin, s githubSpec) (Bump, error) {
	b := Bump{Dep: p.Dep, Datasource: p.Datasource, From: p.Value(s.version)}
	body, err := f.Get(ctx, githubAPI+"/repos/"+p.Dep+"/releases/latest")
	if err != nil {
		return b, err
	}
	var r ghRelease
	if err := json.Unmarshal(body, &r); err != nil {
		return b, fmt.Errorf("%s: the latest release: %w", p.Dep, err)
	}
	if r.Tag == "" || r.Draft || r.Prerelease {
		return b, fmt.Errorf("%s: the latest release %q is a draft, a prerelease or has no tag", p.Dep, r.Tag)
	}
	b.To, b.Release = r.Tag, r.HTMLURL
	if !Newer(r.Tag, b.From) {
		b.To = b.From
		return b, nil
	}
	name := func(t string) string {
		return strings.NewReplacer("{tag}", r.Tag, "{v}", strings.TrimPrefix(r.Tag, "v")).Replace(t)
	}
	assets := map[string]ghAsset{}
	for _, a := range r.Assets {
		assets[a.Name] = a
	}
	sf, ok := assets[name(s.sumsFile)]
	if !ok {
		return b, fmt.Errorf("%s %s: no checksum file %s in the release", p.Dep, r.Tag, name(s.sumsFile))
	}
	raw, err := f.Get(ctx, sf.URL)
	if err != nil {
		return b, err
	}
	if err := matchDigest(sf, raw); err != nil {
		return b, fmt.Errorf("%s %s: %w", p.Dep, r.Tag, err)
	}
	sums, err := parseSums(raw)
	if err != nil {
		return b, fmt.Errorf("%s %s: %s: %w", p.Dep, r.Tag, sf.Name, err)
	}
	b.Set = map[string]string{s.version: r.Tag}
	digests := 0
	for arg, t := range s.assets {
		a, ok := assets[name(t)]
		if !ok {
			return b, fmt.Errorf("%s %s: no asset %s in the release (image/Dockerfile downloads it)", p.Dep, r.Tag, name(t))
		}
		sum, ok := sums[a.Name]
		if !ok {
			return b, fmt.Errorf("%s %s: %s has no checksum for %s", p.Dep, r.Tag, sf.Name, a.Name)
		}
		if a.Digest != "" {
			if a.Digest != "sha256:"+sum {
				return b, fmt.Errorf("%s %s: %s says %s for %s, but GitHub's digest of it is %s: refusing",
					p.Dep, r.Tag, sf.Name, sum, a.Name, a.Digest)
			}
			digests++
		}
		b.Set[arg] = sum
	}
	b.Checks = append(b.Checks, fmt.Sprintf("the sha256 of each asset is from the release's own `%s`", sf.Name))
	switch {
	case digests == len(s.assets):
		b.Checks = append(b.Checks, "each one matches the digest GitHub computed for that asset at upload")
	case digests > 0:
		b.Checks = append(b.Checks, fmt.Sprintf("%d of %d match GitHub's asset digest; GitHub has none for the others", digests, len(s.assets)))
	default:
		b.Checks = append(b.Checks, "GitHub has no digest for these assets (uploaded before it computed them), so the checksum file is the only source")
	}
	return b, nil
}

// matchDigest checks a downloaded asset against GitHub's digest of it, when there is one.
func matchDigest(a ghAsset, body []byte) error {
	if a.Digest == "" {
		return nil
	}
	sum := sha256.Sum256(body)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != a.Digest {
		return fmt.Errorf("%s: downloaded %s, but GitHub's digest is %s: refusing", a.Name, got, a.Digest)
	}
	return nil
}

// parseSums reads a sha256sum-style file: "<hex>  <name>", where the name may start with "*"
// (binary mode) or "./". A malformed line, or one name with two different sums, is an error.
func parseSums(raw []byte) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: want <sha256> <name>", n)
		}
		sum := strings.ToLower(f[0])
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			return nil, fmt.Errorf("line %d: %q isn't a sha256", n, f[0])
		}
		name := strings.TrimPrefix(strings.TrimPrefix(f[1], "*"), "./")
		if old, ok := sums[name]; ok && old != sum {
			return nil, fmt.Errorf("%s is listed twice, with different sums", name)
		}
		sums[name] = sum
	}
	return sums, sc.Err()
}

type npmKeys struct {
	Keys []struct {
		Expires *time.Time `json:"expires"`
		KeyID   string     `json:"keyid"`
		Key     string     `json:"key"`
	} `json:"keys"`
}

type npmVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    struct {
		Integrity  string `json:"integrity"`
		Signatures []struct {
			Sig   string `json:"sig"`
			KeyID string `json:"keyid"`
		} `json:"signatures"`
	} `json:"dist"`
}

// resolveNpm takes the dist-tag's version and its integrity, which the registry signs: the
// signature over "<name>@<version>:<integrity>" must verify with one of npm's published keys.
func resolveNpm(ctx context.Context, f Fetcher, p Pin, s npmSpec, now time.Time) (Bump, error) {
	b := Bump{Dep: p.Dep, Datasource: p.Datasource, From: p.Value(s.version)}
	pkg := url.PathEscape(p.Dep)
	body, err := f.Get(ctx, npmRegistry+"/-/package/"+pkg+"/dist-tags")
	if err != nil {
		return b, err
	}
	var tags map[string]string
	if err := json.Unmarshal(body, &tags); err != nil {
		return b, fmt.Errorf("%s: dist-tags: %w", p.Dep, err)
	}
	v := tags[s.distTag]
	if v == "" {
		return b, fmt.Errorf("%s: no dist-tag %q", p.Dep, s.distTag)
	}
	b.To, b.Release = v, "https://www.npmjs.com/package/"+p.Dep+"/v/"+v
	if !Newer(v, b.From) {
		b.To = b.From
		return b, nil
	}
	if body, err = f.Get(ctx, npmRegistry+"/"+pkg+"/"+url.PathEscape(v)); err != nil {
		return b, err
	}
	var pv npmVersion
	if err := json.Unmarshal(body, &pv); err != nil {
		return b, fmt.Errorf("%s@%s: %w", p.Dep, v, err)
	}
	if pv.Name != p.Dep || pv.Version != v || !strings.HasPrefix(pv.Dist.Integrity, "sha512-") {
		return b, fmt.Errorf("%s@%s: the registry answered for %s@%s, integrity %q", p.Dep, v, pv.Name, pv.Version, pv.Dist.Integrity)
	}
	if body, err = f.Get(ctx, npmRegistry+"/-/npm/v1/keys"); err != nil {
		return b, err
	}
	var keys npmKeys
	if err := json.Unmarshal(body, &keys); err != nil {
		return b, fmt.Errorf("npm's signing keys: %w", err)
	}
	msg := sha256.Sum256([]byte(pv.Name + "@" + pv.Version + ":" + pv.Dist.Integrity))
	verified := ""
	for _, sig := range pv.Dist.Signatures {
		for _, k := range keys.Keys {
			if k.KeyID != sig.KeyID || (k.Expires != nil && !k.Expires.After(now)) {
				continue
			}
			der, err1 := base64.StdEncoding.DecodeString(k.Key)
			raw, err2 := base64.StdEncoding.DecodeString(sig.Sig)
			if err1 != nil || err2 != nil {
				continue
			}
			pub, err := x509.ParsePKIXPublicKey(der)
			if ec, ok := pub.(*ecdsa.PublicKey); err == nil && ok && ecdsa.VerifyASN1(ec, msg[:], raw) {
				verified = k.KeyID
			}
		}
	}
	if verified == "" {
		return b, fmt.Errorf("%s@%s: no registry signature over its integrity verifies with npm's keys: refusing", p.Dep, v)
	}
	b.Set = map[string]string{s.version: v, s.integrity: pv.Dist.Integrity}
	b.Checks = []string{
		fmt.Sprintf("the integrity is the registry's `dist.integrity` for %s@%s (the `%s` dist-tag)", p.Dep, v, s.distTag),
		fmt.Sprintf("its registry signature verifies with npm's published key %s", verified),
		"the image build checks it again (`npm view … dist.integrity`), and npm checks the tarball against it",
	}
	return b, nil
}
