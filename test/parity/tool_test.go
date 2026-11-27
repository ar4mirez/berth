package parity

import (
	"regexp"
	"strings"
	"testing"
)

const toolsFile = "state/orgs/acme/config/tools.txt"

// TestToolCommand: `berth tool` (#216, berth-only). A pin is saved for the org's next start; one
// mise can't verify, or that isn't one exact version, is refused before anything is written; an up
// or restart waits for the container to install the pins and fails with the reason when one
// didn't; an org with no pins waits for nothing.
func TestToolCommand(t *testing.T) {
	raw := allCalls()
	r := run(t, raw, Scenario{Args: []string{"tool", "acme", "set", "gadget", "github:acme/gadget", "1.4.2"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "Pinned gadget to github:acme/gadget@1.4.2. Nothing was restarted") {
		t.Fatalf("set: exit %d\nstdout %q\nstderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	if l := treeLine(r, toolsFile); !strings.HasPrefix(l, toolsFile+" 0644 ") || !strings.HasSuffix(l, `gadget github:acme/gadget 1.4.2\n"`) {
		t.Errorf("tools.txt: %s", l)
	}
	if callsMatching(r, regexp.MustCompile(`"compose"|"restart"`)) != 0 {
		t.Errorf("set restarted something:\n%s", strings.Join(r.Calls, "\n"))
	}
	for _, args := range [][]string{
		{"gadget", "npm:gadget", "1.4.2"},
		{"gadget", "github:acme/gadget", "latest"},
		{"gadget", "github:acme/gadget", "^1.4"},
		{"Gadget", "github:acme/gadget", "1.4.2"},
	} {
		r := run(t, raw, Scenario{Args: append([]string{"tool", "acme", "set"}, args...), Files: twoOrgs})
		if r.Exit != 1 || treeLine(r, toolsFile) != "" {
			t.Errorf("%v: exit %d, stderr %q", args, r.Exit, r.Stderr)
		}
	}
	pinned := merge(twoOrgs, map[string]File{toolsFile: {Content: "gadget github:acme/gadget 1.4.2\n", Mode: 0o644}})

	// A restart waits for the container's status, and fails with mise's reason.
	failed := Rule{Bin: "docker", Match: `^exec claude-acme cat /run/berth/tools.status$`, Stdout: "gadget github:acme/gadget@1.4.2 failed mise ERROR GitHub artifact attestation verification failed\ndone\n"}
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: pinned, Rules: running("acme", failed)})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "acme's pinned tools didn't all install: gadget (github:acme/gadget@1.4.2): mise ERROR GitHub artifact attestation verification failed") {
		t.Errorf("restart, a pin that failed: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	ok := Rule{Bin: "docker", Match: `^exec claude-acme cat /run/berth/tools.status$`, Stdout: "gadget github:acme/gadget@1.4.2 ok\ndone\n"}
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: pinned, Rules: running("acme", ok)})
	if r.Exit != 0 {
		t.Errorf("restart, installed: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"--output", "json", "tool", "acme"}, Files: pinned, Rules: running("acme", ok)})
	if r.Exit != 0 || !strings.Contains(r.Stdout, `"installed": true`) {
		t.Errorf("ls, installed: exit %d, stdout %q", r.Exit, r.Stdout)
	}
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || callsMatching(r, regexp.MustCompile(`tools\.status`)) != 0 {
		t.Errorf("restart without pins: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}

	// rm, and an unknown one.
	r = run(t, raw, Scenario{Args: []string{"tool", "acme", "rm", "gadget"}, Files: pinned})
	if r.Exit != 0 || treeLine(r, toolsFile) != "" {
		t.Errorf("rm: exit %d, file %q", r.Exit, treeLine(r, toolsFile))
	}
	r = run(t, raw, Scenario{Args: []string{"tool", "acme", "rm", "nope"}, Files: pinned})
	if r.Exit != 1 || !strings.Contains(r.Stderr, `acme has no pinned tool "nope"`) {
		t.Errorf("rm, unknown: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}
