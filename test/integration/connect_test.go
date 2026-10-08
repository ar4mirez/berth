//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestConnectTunnel: a host added with --bind localhost gives its new orgs BIND_ADDR=localhost;
// info shows the tunnel; berth connect opens it, and the org's browser terminal answers on this
// machine's 127.0.0.1 (#58).
func TestConnectTunnel(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	target := startSSHTarget(t)
	const fixture, o = "berth-t-sshd", "t-net"
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
	t.Cleanup(func() { _, _ = berth("down", o+"@box") })

	out, _ := berth("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile)
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	if out, err := berth("host", "add", "box", "ops@"+target.addr, "--bind", "vpn0"); err == nil || !strings.Contains(out, "unknown bind mode") {
		t.Errorf("a bad --bind: %v\n%s", err, out)
	}
	must("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1], "--no-guard", "--bind", "localhost")

	// The next steps name the org as it must be typed: with its host (#159).
	if out := must("init", o+"@box", "--name", "Test User", "--email", "test@example.com"); !strings.Contains(out, "berth up "+o+"@box\n") || !strings.Contains(out, "repo add "+o+"@box ") {
		t.Errorf("init's next steps don't name the host:\n%s", out)
	}
	if env := docker(t, "exec", fixture, "cat", "/home/ops/.local/share/berth/orgs/"+o+"/org.env"); !strings.Contains(env, "\nBIND_ADDR=localhost\n") {
		t.Fatalf("the host's default bind didn't reach the org:\n%s", env)
	}
	must("up", o+"@box")
	info := must("info", o+"@box")
	tp := regexp.MustCompile(`http://127\.0\.0\.1:(\d+)`).FindStringSubmatch(info)
	if tp == nil || !strings.Contains(info, "berth connect "+o+"@box") || !strings.Contains(info, "ssh -N -L") {
		t.Fatalf("info for a localhost org on a host:\n%s", info)
	}

	cmd := exec.Command(bin, "connect", o+"@box")
	cmd.Env = env
	var cout strings.Builder
	cmd.Stdout, cmd.Stderr = &cout, &cout
	// Its own process group, as a terminal's Ctrl-C would reach: berth and the ssh it runs.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	mustDo(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
	})

	// ttyd answers (401: it wants the password) once the tunnel is up and the org has started.
	deadline := time.Now().Add(90 * time.Second)
	for {
		code, _ := exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "-m", "3", "http://127.0.0.1:"+tp[1]).Output()
		if c := string(code); c == "401" || c == "200" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing answered on 127.0.0.1:%s through the tunnel; connect said:\n%s", tp[1], cout.String())
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(cout.String(), "Tunnel to "+o+" on box") {
		t.Errorf("connect's output:\n%s", cout.String())
	}
}
