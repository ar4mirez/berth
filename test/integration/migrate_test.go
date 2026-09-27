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

// TestMigrateHost: `migrate <org> box` moves an org from this machine to the sshd + dind host: a
// rehearsal while it runs, then (with --yes) the switch; nothing written after the rehearsal is
// lost, the lease moves, the old copy is kept, stopped. A failure after the stop starts the org
// where it was again (#50).
func TestMigrateHost(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	target := startSSHTarget(t)
	const fixture = "berth-t-sshd"
	const remoteOrgs = "/home/ops/.local/share/berth/orgs/"
	home := t.TempDir()
	state := filepath.Join(home, "state")
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_HOME="+state, "SSH_AUTH_SOCK=", "BERTH_PUBLISHED_IMAGE="+publishedImage)
	berthEnv := func(extra []string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(append([]string{}, env...), extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	berth := func(args ...string) (string, error) { return berthEnv(nil, args...) }
	must := func(args ...string) string {
		t.Helper()
		out, err := berth(args...)
		if err != nil {
			t.Fatalf("berth %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	running := func(where, o string) bool {
		args := []string{"inspect", "-f", "{{.State.Running}}", "claude-" + o}
		if where == "box" {
			args = append([]string{"exec", fixture, "docker"}, args...)
		}
		b, _ := exec.Command("docker", args...).Output()
		return strings.TrimSpace(string(b)) == "true"
	}
	lease := func(o string) string {
		b, _ := os.ReadFile(filepath.Join(home, "cfg", "berth", "leases.yaml"))
		m := regexp.MustCompile(`(?m)^\s+` + o + `: (\S+)$`).FindStringSubmatch(string(b))
		if m == nil {
			return ""
		}
		return m[1]
	}
	t.Cleanup(func() {
		for _, o := range []string{"t-mig", "t-mig2"} {
			_, _ = berth("down", o)
			_, _ = berth("down", o+"@box")
		}
	})

	out, _ := berth("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--no-guard")
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	must("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1], "--no-guard")
	_ = exec.Command("docker", "rmi", "-f", strings.TrimSpace(must("image-tag"))).Run() // the stub (TestRemoteLifecycle)

	must("init", "t-mig", "--name", "Test User", "--email", "test@example.com")
	// In ~/.config (home-config): /workspace is swept of anything that isn't a registered repo.
	ws := filepath.Join(state, "orgs", "t-mig", "home-config")
	mustDo(t, os.MkdirAll(filepath.Join(ws, "notes"), 0o755))
	mustDo(t, os.WriteFile(filepath.Join(ws, "notes", "hello.txt"), []byte("hello from here\n"), 0o644))
	must("up", "t-mig")

	// Without --yes (and no terminal): the rehearsal only. It keeps running here.
	out = must("migrate", "t-mig", "box")
	if !strings.Contains(out, "Rehearsal done") || !strings.Contains(out, "Not switched") || !running("local", "t-mig") || running("box", "t-mig") {
		t.Fatalf("rehearsal:\n%s", out)
	}
	// Written after the rehearsal: the final copy, with the org stopped, must take it.
	mustDo(t, os.WriteFile(filepath.Join(ws, "notes", "late.txt"), []byte("written after the rehearsal\n"), 0o644))

	out = must("migrate", "t-mig", "box", "--yes")
	t.Log(out)
	if !strings.Contains(out, "Migrated: t-mig runs on box") {
		t.Fatalf("migrate --yes:\n%s", out)
	}
	if running("local", "t-mig") || !running("box", "t-mig") {
		t.Fatalf("after the switch: here %v, box %v", running("local", "t-mig"), running("box", "t-mig"))
	}
	if l := lease("t-mig"); l != "box" {
		t.Errorf("lease: %q", l)
	}
	if _, err := os.Stat(filepath.Join(state, "orgs", "t-mig", "org.env")); err != nil {
		t.Errorf("the old copy wasn't kept: %v", err)
	}
	for f, want := range map[string]string{"hello.txt": "hello from here\n", "late.txt": "written after the rehearsal\n"} {
		if got := docker(t, "exec", fixture, "cat", remoteOrgs+"t-mig/home-config/notes/"+f); got != want {
			t.Errorf("%s on box: %q", f, got)
		}
	}
	if _, err := berth("up", "t-mig"); err == nil {
		t.Error("the old copy started here, though box holds the lease")
	}

	// A failure after the stop (at the final copy, or at the start there) starts it here again.
	must("init", "t-mig2", "--name", "Test User", "--email", "test@example.com")
	must("up", "t-mig2")
	for _, step := range []string{"final", "start"} {
		out, err := berthEnv([]string{"BERTH_TEST_MIGRATE_FAIL=" + step}, "migrate", "t-mig2", "box", "--yes")
		if err == nil || !strings.Contains(out, "runs on local again") {
			t.Fatalf("a failure at %s: %v\n%s", step, err, out)
		}
		if !running("local", "t-mig2") || running("box", "t-mig2") {
			t.Fatalf("after a failure at %s: here %v, box %v", step, running("local", "t-mig2"), running("box", "t-mig2"))
		}
		if l := lease("t-mig2"); l != "local" {
			t.Errorf("after a failure at %s, the lease is on %q", step, l)
		}
	}
}
