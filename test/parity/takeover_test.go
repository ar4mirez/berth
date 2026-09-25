package parity

import (
	"strings"
	"testing"
)

func envLine(r Result, org string) string {
	for _, l := range r.Tree {
		if s, ok := strings.CutPrefix(l, "state/orgs/"+org+"/org.env "); ok {
			return s
		}
	}
	return ""
}

// TestBerthTakeover: takeover only switches MANAGER in org.env (in place, mode kept) and touches no
// container; the real legacy ccenv then refuses the org, and accepts it again after handback.
func TestBerthTakeover(t *testing.T) {
	raw := BerthUnowned(berthBin)
	r := run(t, raw, Scenario{Args: []string{"takeover", "acme"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || len(r.Calls) != 0 || !strings.Contains(r.Stdout, "acme is now berth's (MANAGER=berth). Nothing was restarted") ||
		!strings.HasPrefix(envLine(r, "acme"), `0600 "MANAGER=berth\n# ---- Claude account`) {
		t.Fatalf("takeover: exit %d, calls %v, stdout %q, org.env %s", r.Exit, r.Calls, r.Stdout, envLine(r, "acme"))
	}

	owned := withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: "MANAGER=berth\n" + acmeEnv})
	if l := run(t, Legacy(repoRoot), Scenario{Args: []string{"info", "acme"}, Files: owned}); l.Exit != 1 ||
		!strings.Contains(l.Stderr, "org 'acme' is managed by berth (MANAGER=berth)") {
		t.Errorf("ccenv on a taken-over org: exit %d, stderr %q", l.Exit, l.Stderr)
	}

	// A MANAGER=ccenv line is switched where it is.
	r = run(t, raw, Scenario{Args: []string{"takeover", "acme"}, Files: withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: acmeEnv + "MANAGER=ccenv\n"})})
	if l := envLine(r, "acme"); r.Exit != 0 || !strings.HasSuffix(l, `MANAGER=berth\n"`) || strings.Count(l, "MANAGER=") != 1 {
		t.Errorf("takeover over MANAGER=ccenv: exit %d, org.env %s", r.Exit, l)
	}

	if r = run(t, raw, Scenario{Args: []string{"takeover", "acme"}, Files: owned}); r.Exit != 0 || !strings.Contains(r.Stdout, "already berth's") {
		t.Errorf("takeover twice: exit %d, stdout %q", r.Exit, r.Stdout)
	}

	// Handback: ccenv's again, and legacy accepts it.
	r = run(t, raw, Scenario{Args: []string{"handback", "acme"}, Files: owned, Rules: running("acme")})
	if r.Exit != 0 || len(r.Calls) != 0 || !strings.HasPrefix(envLine(r, "acme"), `0600 "MANAGER=ccenv\n`) {
		t.Errorf("handback: exit %d, calls %v, org.env %s", r.Exit, r.Calls, envLine(r, "acme"))
	}
	back := withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: "MANAGER=ccenv\n" + acmeEnv})
	if l := run(t, Legacy(repoRoot), Scenario{Args: []string{"info", "acme"}, Files: back, Rules: nothingRunning}); l.Exit != 0 {
		t.Errorf("ccenv after handback: exit %d, stderr %q", l.Exit, l.Stderr)
	}
	if r = run(t, raw, Scenario{Args: []string{"handback", "globex"}, Files: twoOrgs}); r.Exit != 1 || !strings.Contains(r.Stderr, "is managed by ccenv") {
		t.Errorf("handback of a ccenv org: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// The state root's own ccenv must refuse berth's orgs, or both tools could manage the org.
	unguarded := merge(twoOrgs, map[string]File{"state/ccenv": {Content: "#!/usr/bin/env bash\n# an old ccenv\n", Mode: 0o755}})
	if r = run(t, raw, Scenario{Args: []string{"takeover", "acme"}, Files: unguarded}); r.Exit != 1 || !strings.Contains(r.Stderr, "no berth_owned guard") ||
		strings.Contains(envLine(r, "acme"), "MANAGER") {
		t.Errorf("takeover with an unguarded ccenv: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	guarded := merge(twoOrgs, map[string]File{"state/ccenv": {Content: "#!/usr/bin/env bash\nberth_owned() { :; }\n", Mode: 0o755}})
	if r = run(t, raw, Scenario{Args: []string{"takeover", "acme"}, Files: guarded}); r.Exit != 0 {
		t.Errorf("takeover with a guarded ccenv: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	for _, args := range [][]string{{"takeover", "acme"}, {"handback", "acme"}} {
		ro := run(t, raw, Scenario{Args: append([]string{"--read-only"}, args...), Files: owned})
		if ro.Exit != 1 || !strings.Contains(ro.Stderr, "read-only mode") || !strings.HasPrefix(envLine(ro, "acme"), `0600 "MANAGER=berth\n# ----`) {
			t.Errorf("--read-only %v: exit %d, stderr %q", args, ro.Exit, ro.Stderr)
		}
	}
}
