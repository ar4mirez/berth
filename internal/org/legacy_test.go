package org

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// legacy runs ccenv's own envval/setval/next_port, extracted verbatim from legacy/ccenv, so each
// test compares Go with the real thing rather than with our reading of it. Bash is the thing under
// test here; the runner is this Go test.
type legacy struct {
	funcs string
	orgs  string
}

// newLegacy returns nil (and the test checks Go only) when bash or awk isn't installed.
func newLegacy(t *testing.T, orgs string) *legacy {
	t.Helper()
	for _, bin := range []string{"bash", "awk", "grep", "cut", "tail", "mktemp"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Logf("no %s: checking Go only", bin)
			return nil
		}
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "legacy", "ccenv"))
	if err != nil {
		t.Fatal(err)
	}
	var funcs []string
	for _, name := range []string{"envval", "setval", "next_port"} {
		funcs = append(funcs, extractFunc(t, string(src), name))
	}
	return &legacy{funcs: strings.Join(funcs, "\n"), orgs: orgs}
}

// extractFunc returns the definition of name(): a one-liner, or everything up to the first line
// that is exactly "}".
func extractFunc(t *testing.T, src, name string) string {
	t.Helper()
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, name+"() {") {
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(l), "}") && strings.Count(l, "{") == strings.Count(l, "}") {
			return l
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "}" {
				return strings.Join(lines[i:j+1], "\n")
			}
		}
	}
	t.Fatalf("legacy/ccenv: %s() not found", name)
	return ""
}

// run executes script after the functions, with ccenv's shell options and $ORGS, plus extra env.
func (l *legacy) run(t *testing.T, script string, env ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+l.funcs+"\n"+script)
	cmd.Env = append(os.Environ(), append([]string{"ORGS=" + l.orgs, "LC_ALL=C"}, env...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("legacy %q: %v\n%s", script, err, errb.String())
	}
	return out.String(), errb.String()
}

func TestExtractFunc(t *testing.T) {
	src := "a() { one; }\nb() {  # c\n  x\n  if y; then z; fi\n}\nc() {\n}\n"
	if got := extractFunc(t, src, "a"); got != "a() { one; }" {
		t.Errorf("one-liner: %q", got)
	}
	if got := extractFunc(t, src, "b"); got != "b() {  # c\n  x\n  if y; then z; fi\n}" {
		t.Errorf("block: %q", got)
	}
}
