//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ar4mirez/berth/internal/host/local"
	sshhost "github.com/ar4mirez/berth/internal/host/ssh"
	"github.com/ar4mirez/berth/internal/ops"
)

// TestHostRegistry: berth host add|ls|rm|rotate-access against the sshd + dind fixture, through the
// built binary (#44).
func TestHostRegistry(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	target := startSSHTarget(t)
	const fixture = "berth-t-sshd"
	ctx := context.Background()

	home := t.TempDir()
	cfg := filepath.Join(home, "cfg", "berth")
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_HOME="+filepath.Join(home, "state"), "SSH_AUTH_SOCK=", "USER=root")
	berth := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	authorized := func() string { return docker(t, "exec", fixture, "cat", "/root/.ssh/authorized_keys") }
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	ls := func() ops.Hosts {
		t.Helper()
		cmd := exec.Command(bin, "--output", "json", "host", "ls")
		cmd.Env = env
		out, err := cmd.Output() // stdout only: problems go to stderr
		if err != nil {
			t.Fatalf("host ls: %v\n%s", err, out)
		}
		var h ops.Hosts
		if err := json.Unmarshal(out, &h); err != nil {
			t.Fatalf("host ls json: %v\n%s", err, out)
		}
		return h
	}
	target1 := "root@" + target.addr

	// No terminal: it shows the key's fingerprint and how to pass it, and changes nothing.
	out, err := berth("host", "add", "box", target1, "--identity", target.keyFile)
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if err == nil || m == nil {
		t.Fatalf("add without a confirmed fingerprint: %v\n%s", err, out)
	}
	fp := m[1]
	if keys := docker(t, "exec", fixture, "sh", "-c", "for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $f; done"); !strings.Contains(keys, fp) {
		t.Fatalf("the fingerprint shown, %s, isn't one of the host's keys:\n%s", fp, keys)
	}
	if exists(filepath.Join(cfg, "known_hosts")) || exists(filepath.Join(cfg, "hosts.yaml")) {
		t.Fatal("an unconfirmed add left files behind")
	}

	// Another fingerprint than the host's: refused, nothing recorded.
	if out, err := berth("host", "add", "box", target1, "--identity", target.keyFile, "--fingerprint", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err == nil || !strings.Contains(out, "not the expected") {
		t.Fatalf("wrong fingerprint: %v\n%s", err, out)
	}
	if exists(filepath.Join(cfg, "known_hosts")) || exists(filepath.Join(cfg, "keys", "box")) {
		t.Fatal("a refused add left files behind")
	}

	// --read-only refuses.
	if out, err := berth("--read-only", "host", "add", "box", target1, "--fingerprint", fp); err == nil || !strings.Contains(out, "read-only") {
		t.Fatalf("--read-only: %v\n%s", err, out)
	}

	// The add.
	out, err = berth("host", "add", "box", target1, "--identity", target.keyFile, "--fingerprint", fp)
	if err != nil || !strings.Contains(out, "Added box") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	t.Log(out)
	key := filepath.Join(cfg, "keys", "box")
	if fi, err := os.Stat(key); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("berth's key: %v", err)
	}
	if a := authorized(); strings.Count(a, "berth:box") != 1 || !strings.Contains(a, strings.Fields(docker(t, "exec", fixture, "head", "-1", "/root/.ssh/authorized_keys"))[1]) {
		t.Fatalf("authorized_keys after add:\n%s", a)
	}
	if out, err := berth("host", "add", "box", target1, "--fingerprint", fp); err == nil || !strings.Contains(out, "already registered") {
		t.Errorf("adding twice: %v\n%s", err, out)
	}

	// ls: local first, then box, reachable with berth's own key (no agent, no --identity).
	h := ls()
	if len(h.Hosts) != 2 || h.Hosts[0].Name != "local" || h.Hosts[1].Name != "box" {
		t.Fatalf("host ls: %+v", h)
	}
	if b := h.Hosts[1]; !b.Reachable || b.Docker == "" || b.Orgs == nil || *b.Orgs != 0 || b.Home != "/root/.local/share/berth" || b.Error != "" {
		t.Fatalf("box: %+v", b)
	}

	// rm refuses while the host has an org.
	docker(t, "exec", fixture, "sh", "-c", "mkdir -p /root/.local/share/berth/orgs/t-a && touch /root/.local/share/berth/orgs/t-a/org.env")
	if b := ls().Hosts[1]; b.Orgs == nil || *b.Orgs != 1 {
		t.Fatalf("box with an org: %+v", b)
	}
	if out, err := berth("host", "rm", "box"); err == nil || !strings.Contains(out, "t-a") {
		t.Fatalf("rm with an org: %v\n%s", err, out)
	}

	// rotate-access: the new key works, the old one no longer does.
	oldKey := filepath.Join(t.TempDir(), "old")
	b, err := os.ReadFile(key)
	mustDo(t, err)
	mustDo(t, os.WriteFile(oldKey, b, 0o600))
	out, err = berth("host", "rotate-access", "box")
	if err != nil || !strings.Contains(out, "old key") || !strings.Contains(out, "was removed") {
		t.Fatalf("rotate-access: %v\n%s", err, out)
	}
	dial := func(k string) error {
		hh, err := sshhost.Dial(ctx, sshhost.Config{Addr: target.addr, User: "root", KnownHosts: filepath.Join(cfg, "known_hosts"),
			IdentityFiles: []string{k}, NoAgent: true}, local.New())
		if err == nil {
			_ = hh.Close()
		}
		return err
	}
	if err := dial(oldKey); err == nil {
		t.Error("the old key still logs in after rotate-access")
	}
	if err := dial(key); err != nil {
		t.Errorf("the new key: %v", err)
	}
	if a := authorized(); strings.Count(a, "berth:box") != 1 {
		t.Errorf("authorized_keys after rotate-access:\n%s", a)
	}

	// A changed host key is refused, with a clear message.
	kh := filepath.Join(cfg, "known_hosts")
	pinned, err := os.ReadFile(kh)
	mustDo(t, err)
	mustDo(t, os.WriteFile(kh, []byte(knownhosts.Line([]string{knownhosts.Normalize(target.addr)}, randomKey(t))+"\n"), 0o600))
	if b := ls().Hosts[1]; b.Reachable || !strings.Contains(b.Error, "host key changed") {
		t.Errorf("changed host key: %+v", b)
	}
	if out, err := berth("host", "rotate-access", "box"); err == nil || !strings.Contains(out, "host key changed") {
		t.Errorf("rotate-access with a changed host key: %v\n%s", err, out)
	}
	mustDo(t, os.WriteFile(kh, pinned, 0o600))

	// rm --force: forgets the host and revokes berth's key; the org there is untouched.
	out, err = berth("host", "rm", "box", "--force")
	if err != nil || !strings.Contains(out, "Removed box") {
		t.Fatalf("rm --force: %v\n%s", err, out)
	}
	if strings.Contains(authorized(), "berth:box") {
		t.Error("berth's key is still authorized after rm")
	}
	if exists(key) || exists(key+".pub") {
		t.Error("berth's key files remain after rm")
	}
	if b, _ := os.ReadFile(kh); strings.Contains(string(b), knownhosts.Normalize(target.addr)) {
		t.Errorf("known_hosts still lists the host: %s", b)
	}
	if h := ls(); len(h.Hosts) != 1 {
		t.Errorf("after rm: %+v", h)
	}
	docker(t, "exec", fixture, "test", "-f", "/root/.local/share/berth/orgs/t-a/org.env")
}
