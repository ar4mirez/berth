// Package repopolicy is berth's copy of the in-container repo allowlist rules: the canonical repo
// form (docs/repo-policy.md) and the repos.txt registry. image/repo-policy.sh and
// image/repo-guard.js implement the same rules, and canon_test.go runs testdata/canon.tsv through
// all three and fails if any of them disagrees.
package repopolicy

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`) // at least one dot
	segRE  = regexp.MustCompile(`^[a-z0-9._~-]+$`)
)

var defaultPort = map[string]int{"ssh": 22, "git+ssh": 22, "ssh+git": 22, "https": 443, "http": 80}

// Canon returns the canonical form "host/path" of a repo reference, or "" if it has none (which
// matches nothing, so the reference is denied). host is the host a bare path is on ("" = work it out:
// a dotted first segment, else github.com); URLs and scp-like references carry their own.
func Canon(ref, host string) string {
	if !printable(ref) || ref == "" || !printable(host) {
		return ""
	}
	u, host := lowerASCII(ref), lowerASCII(host)
	var path string
	if scheme, rest, ok := strings.Cut(u, "://"); ok { // URL
		def, known := defaultPort[scheme]
		if !known {
			return ""
		}
		auth, p, ok := strings.Cut(rest, "/")
		if !ok {
			return ""
		}
		path = p
		auth = auth[strings.LastIndexByte(auth, '@')+1:]
		if i := strings.LastIndexByte(auth, ':'); i >= 0 {
			port := auth[i+1:]
			auth = auth[:i]
			// 1 to 5 digits only: Atoi alone would also take "+22".
			if n, err := strconv.Atoi(port); err != nil || len(port) > 5 || strings.Trim(port, "0123456789") != "" || n != def {
				return ""
			}
		}
		if host = auth; host == "" {
			return ""
		}
	} else if before, after, ok := strings.Cut(u, ":"); ok && !strings.Contains(before, "/") { // scp-like
		path = after
		if host = before[strings.LastIndexByte(before, '@')+1:]; host == "" {
			return ""
		}
	} else { // bare path
		path = u
	}

	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if host == "" {
		if len(segs) >= 2 && strings.Contains(segs[0], ".") {
			host, segs = segs[0], segs[1:]
		} else {
			host = "github.com"
		}
	}
	if len(segs) == 0 {
		return ""
	}
	last := segs[len(segs)-1]
	for strings.HasSuffix(last, ".git") && len(last) > 4 {
		last = strings.TrimSuffix(last, ".git")
	}
	segs[len(segs)-1] = last
	for _, s := range segs {
		if !segRE.MatchString(s) || s == "." || s == ".." {
			return ""
		}
	}
	if !hostRE.MatchString(host) {
		return ""
	}
	return host + "/" + strings.Join(segs, "/")
}

// printable reports whether s is printable ASCII without spaces ('!' to '~'). "" is printable.
func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return true
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Entry is one registered repo in repos.txt.
type Entry struct {
	Dir string
	// URL as written, or "local" for a repo with no remote yet.
	URL string
	// Branch, or "" for the remote's default.
	Branch string
	// Canon is URL's canonical form ("local/<dir>" for local), or "" if it has none: the dir is
	// still registered (the workspace sweep leaves it alone) but it allows no repo.
	Canon string
}

// Entries parses repos.txt like repo_entries: one "<dir> <url> [branch]" per line, fields split on
// spaces and tabs, a trailing CR ignored, "#" comments and lines without a URL skipped, and a last
// line without a newline kept.
func Entries(data []byte) []Entry {
	var out []Entry
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.FieldsFunc(strings.TrimSuffix(line, "\r"), func(r rune) bool { return r == ' ' || r == '\t' })
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		e := Entry{Dir: f[0], URL: f[1]}
		if len(f) > 2 {
			e.Branch = f[2]
		}
		if e.URL == "local" {
			e.Canon = "local/" + e.Dir
		} else {
			e.Canon = Canon(e.URL, "")
		}
		out = append(out, e)
	}
	return out
}

// Allowed reports whether canon is registered. "" is never allowed.
func Allowed(entries []Entry, canon string) bool {
	if canon == "" {
		return false
	}
	for _, e := range entries {
		if e.Canon == canon {
			return true
		}
	}
	return false
}
