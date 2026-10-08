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

	"github.com/ar4mirez/berth/internal/contract"
)

// TestNativeCommandLine: berth's own command line (#140) against a real engine. The other tests
// here run ccenv's spellings (BERTH_SPELLINGS=ccenv), as the parity suite does; this one runs what
// people type: groups, verbs first, the everyday shortcuts, the default org, a removed spelling,
// and a command through a running `berth serve`.
func TestNativeCommandLine(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	engine := os.Getenv("BERTH_ENGINE")
	if engine == "" {
		engine = "docker"
	}
	const o = "t-native"
	home, err := os.MkdirTemp("", "berth-native-")
	mustDo(t, err)
	state, sock := filepath.Join(home, "state"), filepath.Join(home, "b.sock")
	env := append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"), "BERTH_HOME="+state,
		"BERTH_PUBLISHED_IMAGE="+publishedImage, "BERTH_SOCKET="+sock)
	for i, kv := range env {
		if strings.HasPrefix(kv, "BERTH_SPELLINGS=") {
			env[i] = "BERTH_SPELLINGS="
		}
	}
	if engine != "podman" {
		env = append(env, "HOME="+home)
	}
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
	has := func(what, out string, wants ...string) {
		t.Helper()
		for _, w := range wants {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q in:\n%s", what, w, out)
			}
		}
	}
	eng := func(args ...string) (string, error) {
		b, err := exec.Command(engine, args...).CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	tag := strings.TrimSpace(must("image-tag"))
	t.Cleanup(func() {
		_, _ = berth("down", o)
		_, _ = eng("run", "--rm", "-v", state+":/s", "--entrypoint", "rm", tag, "-rf", "/s/orgs", "/s/backups")
		if engine == "podman" {
			_ = exec.Command("podman", "unshare", "rm", "-rf", home).Run()
		}
		_ = os.RemoveAll(home)
	})

	// A removed spelling names its replacement and creates nothing.
	if out, err := berth("init", o); err == nil || !strings.Contains(out, "'init' is now 'berth org create'") {
		t.Fatalf("berth init: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(state, "orgs", o)); err == nil {
		t.Fatal("the removed spelling created the org")
	}

	has("org create", must("org", "create", o, "--name", "Test User", "--email", "test@example.com"), "berth up "+o, "berth account signin "+o)
	// Ports of its own: on a machine with orgs of another state root, the first free ones berth sees
	// in this one may be theirs.
	envFile := filepath.Join(state, "orgs", o, "org.env")
	b, err := os.ReadFile(envFile)
	mustDo(t, err)
	b = regexp.MustCompile(`(?m)^SSH_PORT=.*$`).ReplaceAll(b, []byte("SSH_PORT=2391"))
	b = regexp.MustCompile(`(?m)^TTYD_PORT=.*$`).ReplaceAll(b, []byte("TTYD_PORT=7891"))
	b = regexp.MustCompile(`(?m)^BIND_ADDR=.*$`).ReplaceAll(b, []byte("BIND_ADDR=127.0.0.1"))
	mustDo(t, os.WriteFile(envFile, b, 0o600))
	has("up", must("up", o), "Started")
	has("org ls", must("org", "ls"), o, "up")
	has("ls", must("ls"), o, "up")
	has("info", must("info", o), "claude-"+o, "running", "berth org password show "+o)
	deadline := time.Now().Add(3 * time.Minute)
	for st, _ := eng("exec", "claude-"+o, "cat", contract.FirewallStatus); !strings.HasPrefix(st, "on"); st, _ = eng("exec", "claude-"+o, "cat", contract.FirewallStatus) {
		if time.Now().After(deadline) {
			t.Fatalf("the firewall never came up: %q", st)
		}
		time.Sleep(2 * time.Second)
	}

	// Verbs first.
	has("fw allow", must("fw", "allow", o, "example.com"), "allowed: example.com")
	has("fw show", must("fw", "show", o), "example.com", "live: on")
	has("fw test", must("fw", "test", o, "example.com"), "ALLOWED  https://example.com")
	has("fw deny", must("fw", "deny", o, "example.com"), "removed: example.com")
	// The org first still works, as the instructions inside older images say it.
	has("fw <org> allow", must("fw", o, "allow", "example.org"), "allowed: example.org")
	has("env ls", must("env", "ls", o), "berth env set "+o+" KEY")
	has("pkg ls", must("pkg", "ls", o), "berth pkg add "+o)
	has("repo add", must("repo", "add", o, "acme/widgets", "--no-clone"), "Registered github.com/acme/widgets")
	has("repo ls", must("repo", "ls", o), "widgets")
	has("account whoami", must("account", "whoami", o), o, "MISSING")
	has("org password show", must("org", "password", "show", o), "user: node", "pass: ")
	if out := must("org", "exec", o, "--", "sh", "-c", "id -un; hostname"); !strings.Contains(out, "node") {
		t.Errorf("org exec: %s", out)
	}
	has("--output json", must("--output", "json", "fw", "show", o), `"schema": "berth.firewall/v1"`, `"example.org"`)

	// The default org, and a verb with the org left out.
	has("org use", must("org", "use", o), "Default org: "+o)
	has("fw show, the default org", must("fw", "show"), "the default org: berth org use", "example.org")
	has("org use --clear", must("org", "use", "--clear"), "No default org now")
	if out, err := berth("fw", "show"); err == nil || !strings.Contains(out, "fw show: which org?") {
		t.Errorf("fw show with no org and no default: %v %s", err, out)
	}

	// --help runs nothing: the org is still up after `berth down --help` and `berth org destroy --help`.
	must("down", "--help")
	must("org", "destroy", o, "--help")
	has("ls after --help", must("ls"), "up")

	// Through a running server.
	srv := exec.Command(bin, "serve")
	srv.Env = env
	mustDo(t, srv.Start())
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	for i := 0; ; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		} else if i > 100 {
			t.Fatal("berth serve never listened")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if direct, via := must("fw", "show", o), must("--via-daemon", "fw", "show", o); direct != via {
		t.Errorf("--via-daemon fw show:\n%s\ndirectly:\n%s", via, direct)
	}
	has("--via-daemon fw allow", must("--via-daemon", "fw", "allow", o, "via.example"), "allowed: via.example")
	if b, err := os.ReadFile(filepath.Join(state, "audit.log")); err != nil || !strings.Contains(string(b), `"cli fw allow"`) {
		t.Errorf("the audit log after a write through the server: %v %s", err, b)
	}

	// Backups, under their group.
	pub := regexp.MustCompile(`age1[0-9a-z]+`).FindString(must("backup", "keygen"))
	if pub == "" {
		t.Fatal("backup keygen printed no public key")
	}
	out := must("backup", "create", o)
	f := regexp.MustCompile(`Wrote (\S+\.tar\.zst\.age)`).FindStringSubmatch(out)
	if f == nil {
		t.Fatalf("backup create: %s", out)
	}
	must("backup", "restore", f[1], "--as", o+"-r", "--no-start", "--no-rehydrate")
	has("backup schedule status", must("backup", "schedule", "status"), "berth backup schedule on")
	has("org ls, after the restore", must("org", "ls"), o+"-r")

	// The lifecycle's end, with the shortcuts and the group.
	has("restart", must("restart", o), "Started")
	must("down", o)
	has("ls after down", must("ls"), "down")
	has("org destroy", must("org", "destroy", o+"-r", "--yes"), "Destroyed "+o+"-r")
	has("org destroy", must("org", "destroy", o, "--yes"), "Destroyed "+o)
	if out := must("org", "ls"); strings.Contains(out, o) {
		t.Errorf("orgs remain: %s", out)
	}
}
