//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestHostAPI: a registered host that runs its own `berth serve` is asked for whole operations
// over its socket, forwarded through ssh (#148): the output is the ssh path's, the host's audit
// log shows it came through its API, and a host with no server is driven over ssh as before.
func TestHostAPI(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	// The host's berth: the same build, for the host's platform (BERTH_HOST_BIN when this machine's differs).
	hostBin := os.Getenv("BERTH_HOST_BIN")
	if hostBin == "" {
		hostBin = bin
	}
	target := startSSHTarget(t)
	const fixture, o = "berth-t-sshd", "t-hostapi"
	home := t.TempDir()
	base := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_SPELLINGS=ccenv", "BERTH_HOME="+filepath.Join(home, "state"), "SSH_AUTH_SOCK=", "BERTH_PUBLISHED_IMAGE="+publishedImage)
	berthEnv := func(extra []string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(append([]string{}, base...), extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := berthEnv(nil, args...)
		if err != nil {
			t.Fatalf("berth %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	onHost := func(args ...string) string {
		b, _ := exec.Command("docker", append([]string{"exec", "-u", "ops", fixture}, args...)...).CombinedOutput()
		return string(b)
	}
	audit := func() string { return onHost("sh", "-c", "cat ~/.local/share/berth/audit.log 2>/dev/null") }
	t.Cleanup(func() { _, _ = berthEnv(nil, "down", o+"@box") })

	out, _ := berthEnv(nil, "host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--no-guard")
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	must("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1], "--no-guard")
	must("init", o+"@box", "--name", "Test User", "--email", "test@example.com")
	must("up", o+"@box")

	// No server on the host yet: ssh, as before, and nothing in an audit log there.
	viaSSH := must("fw", o+"@box", "allow", "ssh-path.example")
	if !strings.Contains(viaSSH, "allowed: ssh-path.example") || audit() != "" {
		t.Fatalf("without a server: %q, the host's audit log: %q", viaSSH, audit())
	}
	showSSH := must("fw", o+"@box", "show")

	// The host gets berth, and runs its API.
	if b, err := exec.Command("docker", "cp", hostBin, fixture+":/usr/local/bin/berth").CombinedOutput(); err != nil {
		t.Fatalf("docker cp: %v %s", err, b)
	}
	if b, err := exec.Command("docker", "exec", "-d", "-u", "ops", "-e", "HOME=/home/ops", fixture, "berth", "serve").CombinedOutput(); err != nil {
		t.Fatalf("starting berth serve on the host: %v %s", err, b)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(onHost("sh", "-c", "ls ~/.config/berth/"), "berth.sock") {
		if time.Now().After(deadline) {
			t.Fatalf("berth serve never listened on the host: %s", onHost("sh", "-c", "ls -la ~/.config/berth/; berth --version"))
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The same read, now through the host's berth: the same answer.
	if showAPI := must("fw", o+"@box", "show"); showAPI != showSSH {
		t.Errorf("fw show through the host's API:\n%s\nover ssh:\n%s", showAPI, showSSH)
	}
	// A write goes through it too, and the host's audit log says so.
	if out := must("fw", o+"@box", "allow", "api-path.example"); !strings.Contains(out, "allowed: api-path.example") {
		t.Errorf("fw allow through the host's API: %s", out)
	}
	if a := audit(); !strings.Contains(a, `"via":"api"`) || !strings.Contains(a, `"cli fw allow"`) || !strings.Contains(a, "api-path.example") {
		t.Errorf("the host's audit log doesn't show the API: %q", a)
	}
	if out := must("repo", "ls", o+"@box"); !strings.Contains(out, "no repos registered") {
		t.Errorf("repo ls through the host's API: %s", out)
	}
	// An error keeps its message and exit code.
	if out, err := berthEnv(nil, "fw", "t-nope@box", "show"); err == nil || !strings.Contains(out, "unknown org 't-nope'") {
		t.Errorf("an unknown org through the host's API: %v %s", err, out)
	}
	// What depends on this machine keeps the ssh path: info names the host as this machine knows it.
	if out := must("info", o+"@box"); !strings.Contains(out, "claude-"+o) {
		t.Errorf("info: %s", out)
	}
	// Asked not to, berth uses ssh: one more entry in the firewall, none in the audit log.
	before := strings.Count(audit(), "\n")
	if out, err := berthEnv([]string{"BERTH_HOST_API=off"}, "fw", o+"@box", "allow", "off.example"); err != nil || !strings.Contains(out, "allowed: off.example") {
		t.Errorf("BERTH_HOST_API=off: %v %s", err, out)
	}
	if after := strings.Count(audit(), "\n"); after != before {
		t.Errorf("BERTH_HOST_API=off still went through the API: %d audit lines, then %d", before, after)
	}
	// The server gone, its socket left behind or not: ssh again.
	onHost("sh", "-c", "pkill -f 'berth serve'; sleep 1; ls ~/.config/berth/")
	if out := must("fw", o+"@box", "allow", "after.example"); !strings.Contains(out, "allowed: after.example") {
		t.Errorf("after the server stopped: %s", out)
	}
	if show := must("fw", o+"@box", "show"); !strings.Contains(show, "ssh-path.example") || !strings.Contains(show, "api-path.example") || !strings.Contains(show, "after.example") {
		t.Errorf("the allowlist after all three paths:\n%s", show)
	}
}
