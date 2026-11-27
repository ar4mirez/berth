package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// Pinned tools (#216): one exact version of a tool, installed with the image's mise when the org
// starts, so a program in the org matches what talks to it from outside. The list is
// orgs/<org>/config/tools.txt (the container reads it at /config/tools.txt): a change applies at
// the org's next up or restart. mise installs each with its checks on (paranoid mode, GitHub
// artifact attestations and SLSA provenance where the release has them, checksums); one that
// fails stops the start with the reason, never a silent skip.

// ToolsStatus is where the entrypoint says how each pinned tool installed, one line each:
// "<name> <source>@<version> ok" or "... failed <reason>", then "done" on a line of its own.
const ToolsStatus = "/run/berth/tools.status"

// ToolsFile is an org's list of pinned tools.
func ToolsFile(s System, org string) string {
	return path.Join(s.OrgsDir(), org, "config", "tools.txt")
}

// PinnedTool is one pinned tool.
type PinnedTool struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version"`
	// Installed is whether the running container installed this version at its start.
	Installed bool `json:"installed"`
}

// Tools is `tool <org> ls` (berth.tools/v1).
type Tools struct {
	Schema string       `json:"schema"`
	Org    string       `json:"org"`
	Tools  []PinnedTool `json:"tools"`
}

var (
	toolName    = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	toolSource  = regexp.MustCompile(`^(github|aqua):[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	toolVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?([-+][0-9A-Za-z][0-9A-Za-z.-]*)?$`)
)

// CheckTool checks a pinned tool: a name like the others ([a-z0-9-]{1,32}); a source that is a
// mise backend whose downloads mise verifies (github:owner/repo or aqua:owner/repo); and an exact
// version, at least major.minor.patch (0.6.0, v1.2.3-rc.1), never a range, a prefix like 1 or 1.4
// (mise would take the newest that matches), or latest.
func CheckTool(t PinnedTool) error {
	switch {
	case !toolName.MatchString(t.Name):
		return Usage("the tool's name %q: lowercase letters, digits and dashes, up to 32", t.Name)
	case !toolSource.MatchString(t.Source):
		return Usage("source %q: a mise backend whose downloads it verifies, github:owner/repo or aqua:owner/repo", t.Source)
	case !toolVersion.MatchString(t.Version):
		return Usage("version %q: one exact version, major.minor.patch as in 0.6.0 (no range, no prefix, no latest)", t.Version)
	}
	return nil
}

// ToolsHeader starts tools.txt.
func ToolsHeader(org string) string {
	return "# Tools installed at " + org + "'s start, one exact version each, managed with: " + Tool + " tool " + org + " set|rm ...\n# <name> <mise source> <version>\n"
}

// ReadTools is an org's list as saved, in name order ([] when it has none). A line that isn't a
// valid tool is left out, as the entrypoint leaves it out.
func ReadTools(s System, org string) ([]PinnedTool, error) {
	b, err := s.ReadFile(ToolsFile(s, org))
	if errors.Is(err, fs.ErrNotExist) {
		return []PinnedTool{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []PinnedTool{}
	for _, l := range lines(b) {
		f := strings.Fields(l)
		if len(f) != 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		t := PinnedTool{Name: f[0], Source: f[1], Version: f[2]}
		if CheckTool(t) == nil {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b PinnedTool) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// EncodeTools is tools.txt for the list.
func EncodeTools(org string, ts []PinnedTool) []byte {
	var b strings.Builder
	b.WriteString(ToolsHeader(org))
	slices.SortFunc(ts, func(a, b PinnedTool) int { return strings.Compare(a.Name, b.Name) })
	for _, t := range ts {
		fmt.Fprintf(&b, "%s %s %s\n", t.Name, t.Source, t.Version)
	}
	return []byte(b.String())
}

// ToolResult is how one tool installed at the container's start.
type ToolResult struct {
	Name, Spec string
	OK         bool
	Reason     string
}

// ParseToolsStatus reads ToolsStatus: each tool's result, and whether the entrypoint is done.
func ParseToolsStatus(raw string) (map[string]ToolResult, bool) {
	out := map[string]ToolResult{}
	done := false
	for _, l := range strings.Split(raw, "\n") {
		l = strings.TrimSpace(l)
		if l == "done" {
			done = true
			continue
		}
		f := strings.SplitN(l, " ", 4)
		if len(f) < 3 {
			continue
		}
		r := ToolResult{Name: f[0], Spec: f[1], OK: f[2] == "ok"}
		if len(f) == 4 {
			r.Reason = f[3]
		}
		out[r.Name] = r
	}
	return out, done
}

// ReadToolsStatus is the running container's ToolsStatus ("" when there is none).
func ReadToolsStatus(ctx context.Context, s System, org string) string {
	out, _ := s.Capture(ctx, true, "docker", "exec", contract.Container(org), "cat", ToolsStatus)
	return out
}

// GetTools is `tool <org> ls`.
func GetTools(ctx context.Context, s System, org string) (Tools, error) {
	out := Tools{Schema: "berth.tools/v1", Org: org, Tools: []PinnedTool{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	ts, err := ReadTools(s, org)
	if err != nil || len(ts) == 0 {
		return out, err
	}
	if Running(ctx, s, org) {
		res, _ := ParseToolsStatus(ReadToolsStatus(ctx, s, org))
		for i, t := range ts {
			r, ok := res[t.Name]
			ts[i].Installed = ok && r.OK && r.Spec == t.Source+"@"+t.Version
		}
	}
	out.Tools = ts
	return out, nil
}
