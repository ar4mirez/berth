//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLease: the same org on this machine and on the sshd + dind fixture (box); only the lease
// holder starts, and --take-lease stops the other host's copy first (#47).
func TestLease(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	target := startSSHTarget(t)
	const fixture, o = "berth-t-sshd", "t-lease"
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_SPELLINGS=ccenv", "BERTH_HOME="+filepath.Join(home, "state"), "SSH_AUTH_SOCK=", "BERTH_PUBLISHED_IMAGE="+publishedImage)
	berth := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := berth(args...)
		if err != nil {
			t.Fatalf("berth %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	refused := func(want string, args ...string) {
		t.Helper()
		if out, err := berth(args...); err == nil || !strings.Contains(out, want) {
			t.Fatalf("berth %s: want a refusal with %q, got %v\n%s", strings.Join(args, " "), want, err, out)
		}
	}
	running := func(where string) bool {
		t.Helper()
		args := []string{"inspect", "-f", "{{.State.Running}}", "claude-" + o}
		if where == "box" {
			args = append([]string{"exec", fixture, "docker"}, args...)
		}
		b, _ := exec.Command("docker", args...).Output()
		return strings.TrimSpace(string(b)) == "true"
	}
	t.Cleanup(func() { _, _ = berth("down", o); _, _ = berth("down", o+"@box") })

	out, _ := berth("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile)
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	must("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1])

	// The stub image earlier tests leave under berth's tag (see TestRemoteLifecycle).
	_ = exec.Command("docker", "rmi", "-f", strings.TrimSpace(must("image-tag"))).Run()

	must("init", o, "--name", "Test User", "--email", "test@example.com")
	must("init", o+"@box", "--name", "Test User", "--email", "test@example.com")

	// On both hosts, no lease yet: berth wants to be told which runs it.
	refused("also on box", "up", o)
	must("up", o, "--take-lease")
	if !running("local") || running("box") {
		t.Fatal("after up --take-lease here: only this machine's copy should run")
	}
	// Only the holder starts.
	refused("runs on local", "up", o+"@box")
	refused("runs on local", "restart", o+"@box")
	if running("box") {
		t.Fatal("a refused up started box's copy")
	}
	// --take-lease stops the old host's copy first.
	out = must("up", o+"@box", "--take-lease")
	if !strings.Contains(out, "Stopping "+o+" on local first") {
		t.Errorf("take-lease didn't announce the stop:\n%s", out)
	}
	if running("local") || !running("box") {
		t.Fatalf("after up %s@box --take-lease: local running=%v, box running=%v", o, running("local"), running("box"))
	}
	refused("runs on box", "restart", o)
	b, err := os.ReadFile(filepath.Join(home, "cfg", "berth", "leases.yaml"))
	mustDo(t, err)
	if !strings.Contains(string(b), o+": box") {
		t.Errorf("leases.yaml:\n%s", b)
	}
	if marker := docker(t, "exec", fixture, "cat", "/home/ops/.local/share/berth/berth/lease/"+o); !strings.HasPrefix(marker, "active: box") {
		t.Errorf("marker on box: %q", marker)
	}
	must("down", o+"@box")
}
