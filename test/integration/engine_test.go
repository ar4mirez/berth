//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/contract"
)

// TestEngineLifecycle: an org's life on this machine's engine ($BERTH_ENGINE: docker by default,
// podman in the podman jobs, rootless and rootful): init, up, the firewall allowing and blocking,
// repo add, files the container writes staying the operator's, backup, restore, down (#57).
func TestEngineLifecycle(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	engine := os.Getenv("BERTH_ENGINE")
	if engine == "" {
		engine = "docker"
	}
	const o = "t-eng"
	// Not t.TempDir: files the container made (sshd's keys) may not be removable by this user, and
	// that mustn't fail the test; they're removed through the engine, best effort.
	home, err := os.MkdirTemp("", "berth-engine-")
	mustDo(t, err)
	state := filepath.Join(home, "state")
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_HOME="+state, "BERTH_PUBLISHED_IMAGE="+publishedImage)
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
	if engine == "docker" {
		_, _ = eng("rmi", "-f", tag) // the stub image earlier tests leave under berth's tag
	}
	t.Logf("engine: %s, uid %d", engine, os.Getuid())

	must("init", o, "--name", "Test User", "--email", "test@example.com")
	must("up", o)
	diag := func() string {
		st, _ := eng("inspect", "-f", "{{json .State}}", "claude-"+o)
		logs, _ := eng("logs", "--tail", "40", "claude-"+o)
		return st + "\n" + logs
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		st, _ := eng("exec", "claude-"+o, "cat", contract.FirewallStatus)
		if strings.HasPrefix(st, "on") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the firewall never came up (%q):\n%s", st, diag())
		}
		time.Sleep(2 * time.Second)
	}

	// The firewall: an allowed host answers, another doesn't.
	must("fw", o, "allow", "example.com")
	if code, err := eng("exec", "claude-"+o, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "-m", "20", "https://example.com"); err != nil || code != "200" {
		t.Errorf("allowed egress: %s %v", code, err)
	}
	if code, err := eng("exec", "claude-"+o, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "-m", "8", "https://www.wikipedia.org"); err == nil && code == "200" {
		t.Errorf("egress that isn't allowed got through (%s)", code)
	}
	must("repo", "add", o, "acme/widgets", "--no-clone")

	// Files the container writes in the bind mounts are the operator's (rootless Podman: keep-id).
	if _, err := eng("exec", "-u", "node", "claude-"+o, "sh", "-c", "echo hi > /home/node/.claude/berth-engine-test"); err != nil {
		t.Fatalf("writing as the container user: %v\n%s", err, diag())
	}
	fi, err := os.Stat(filepath.Join(state, "orgs", o, "claude", "berth-engine-test"))
	mustDo(t, err)
	if uid := fi.Sys().(*syscall.Stat_t).Uid; int(uid) != os.Getuid() {
		t.Errorf("a file the container user wrote is owned by uid %d here, not %d", uid, os.Getuid())
	}

	// Backup and restore run in the engine too.
	pub := regexp.MustCompile(`age1[0-9a-z]+`).FindString(must("keygen"))
	if pub == "" {
		t.Fatal("keygen printed no public key")
	}
	out := must("backup", o)
	f := regexp.MustCompile(`Wrote (\S+\.age)`).FindStringSubmatch(out)
	if f == nil {
		t.Fatalf("backup:\n%s", out)
	}
	must("restore", f[1], "--as", o+"-r", "--no-start", "--no-rehydrate")
	if _, err := os.Stat(filepath.Join(state, "orgs", o+"-r", "org.env")); err != nil {
		t.Errorf("the restored org: %v", err)
	}

	must("down", o)
	if ps, _ := eng("ps", "-aq", "--filter", fmt.Sprintf("name=^claude-%s$", o)); ps != "" {
		t.Errorf("a container remains after down: %s", ps)
	}
}
