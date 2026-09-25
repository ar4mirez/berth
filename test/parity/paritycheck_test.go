package parity

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBerthParityCheck: `berth parity-check` (the host's pre-cutover check) finds nothing to report
// on the fixture orgs, where every read command is known to match, and reports a difference when
// ccenv's output differs.
func TestBerthParityCheck(t *testing.T) {
	rules := running("acme", acmeRepoRunning...)
	r := run(t, BerthUnowned(berthBin), Scenario{Args: []string{"parity-check", "--legacy", filepath.Join(repoRoot, "legacy", "ccenv")},
		Files: merge(repoOrg, withQuarantine), Rules: rules})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "ok    repo ls acme\n") || !strings.Contains(r.Stdout, "18 checks, 0 differ\n") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", r.Exit, r.Stdout, r.Stderr)
	}

	fake := map[string]File{"home/fake-ccenv": {Content: "#!/bin/sh\necho \"ccenv: something else\" >&2\nexit 3\n", Mode: 0o755}}
	r = run(t, BerthUnowned(berthBin), Scenario{Args: []string{"parity-check", "--legacy", "./fake-ccenv", "globex"}, Files: merge(twoOrgs, fake)})
	if r.Exit != 1 || !strings.Contains(r.Stdout, "DIFF  info globex\n") || !strings.Contains(r.Stdout, "- exit 3\n") ||
		!strings.Contains(r.Stdout, "- <tool>: something else") || !strings.Contains(r.Stdout, "10 checks, 10 differ\n") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", r.Exit, r.Stdout, r.Stderr)
	}
}
