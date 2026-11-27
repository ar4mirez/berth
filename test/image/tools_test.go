package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeMise is mise as berth-tools runs it: it records its arguments and the checks it was given,
// and fails a tool whose name has "bad" in it, as a failed attestation would.
const fakeMise = `#!/usr/bin/env bash
echo "$* paranoid=$MISE_PARANOID attest=$MISE_GITHUB_ATTESTATIONS slsa=$MISE_SLSA cosign=$MISE_AQUA_COSIGN" >> "$FAKE_LOG"
case "$*" in *bad*) echo "mise ERROR GitHub artifact attestation verification failed"; echo "for $*"; exit 1 ;; esac
`

// TestBerthTools: image/berth-tools, which installs an org's pinned tools at its start (#216). Each
// is installed with mise's checks on; one that fails is reported with mise's reason, never
// skipped; a line that isn't a pin is reported, not run; the status ends with "done".
func TestBerthTools(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getuid() == 0 {
		t.Skip("berth-tools runs in the Linux image, as the container's user")
	}
	script, err := filepath.Abs(filepath.Join(dir, "berth-tools"))
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	mise, log, list, status := filepath.Join(d, "mise"), filepath.Join(d, "mise.log"), filepath.Join(d, "tools.txt"), filepath.Join(d, "tools.status")
	if err := os.WriteFile(mise, []byte(fakeMise), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte("# a header\ngadget github:acme/gadget 1.4.2\nbad aqua:acme/bad 2.0.0\nevil github:acme/evil 1.0;id\nwidget github:acme/widget v0.1.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(script)
	c.Env = append(os.Environ(), "BERTH_TOOLS_LIST="+list, "BERTH_TOOLS_STATUS="+status, "BERTH_TOOLS_MISE="+mise, "FAKE_LOG="+log)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("berth-tools: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(status)
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 5 || lines[0] != "gadget github:acme/gadget@1.4.2 ok" ||
		!strings.HasPrefix(lines[1], "bad aqua:acme/bad@2.0.0 failed mise ERROR GitHub artifact attestation verification failed for use --global aqua:acme/bad@2.0.0") ||
		!strings.HasPrefix(lines[2], "evil - failed not a valid pin") || lines[3] != "widget github:acme/widget@v0.1.0 ok" || lines[4] != "done" {
		t.Errorf("status:\n%s", got)
	}
	ran, _ := os.ReadFile(log)
	if strings.Contains(string(ran), "evil") || strings.Count(string(ran), "paranoid=1 attest=1 slsa=1 cosign=1") != 3 ||
		!strings.Contains(string(ran), "use --global github:acme/gadget@1.4.2") {
		t.Errorf("mise ran:\n%s", ran)
	}
}
