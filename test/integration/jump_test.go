//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
	sshhost "github.com/ar4mirez/berth/internal/host/ssh"
	"github.com/ar4mirez/berth/internal/ops"
)

// TestHostThroughJump: a host only a bastion can reach (#99). The sshd + dind fixture sits on a
// Docker network this machine can't reach, as box.internal; a second sshd, the bastion, is the
// only thing published. berth host add pins both keys, puts its key on the bastion allowed only
// to forward to the host, and everything after goes through the bastion: host ls, a TTY command
// through the operator's ssh binary, rotate-access and rm.
func TestHostThroughJump(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	requireDocker(t)
	const network, target, bastion = "berth-t-jumpnet", "berth-t-jtarget", "berth-t-bastion"

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	mustDo(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	mustDo(t, err)
	block, err := gossh.MarshalPrivateKey(priv, "berth integration test")
	mustDo(t, err)
	opKey := filepath.Join(t.TempDir(), "id_ed25519")
	mustDo(t, os.WriteFile(opKey, pem.EncodeToMemory(block), 0o600))
	authorizedKey := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey())))

	docker(t, "build", "-q", "-t", "berth-test-sshd", "testdata/sshd")
	docker(t, "build", "-q", "-t", "berth-test-bastion", "testdata/bastion")
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", "-v", target, bastion).Run()
		_ = exec.Command("docker", "network", "rm", network).Run()
	}
	cleanup()
	t.Cleanup(func() {
		if t.Failed() {
			for _, c := range []string{target, bastion} {
				out, _ := exec.Command("docker", "logs", "--tail", "30", c).CombinedOutput()
				t.Logf("%s logs:\n%s", c, out)
			}
		}
		cleanup()
	})
	docker(t, "network", "create", network)
	docker(t, "run", "-d", "--privileged", "--name", target, "--network", network, "--network-alias", "box.internal",
		"-v", "/var/lib/docker", "-e", "AUTHORIZED_KEY="+authorizedKey, "-e", "DOCKER_TLS_CERTDIR=", "berth-test-sshd")
	docker(t, "run", "-d", "--name", bastion, "--network", network, "-p", "127.0.0.1::22",
		"-e", "AUTHORIZED_KEY="+authorizedKey, "berth-test-bastion")
	deadline := time.Now().Add(90 * time.Second)
	for exec.Command("docker", "exec", target, "docker", "info").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("dind never came up")
		}
		time.Sleep(time.Second)
	}
	jumpAddr := strings.TrimSpace(docker(t, "port", bastion, "22/tcp"))
	fingerprints := func(c string) string {
		return docker(t, "exec", c, "sh", "-c", "for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf $f; done")
	}

	home := t.TempDir()
	cfg := filepath.Join(home, "cfg", "berth")
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_SPELLINGS=ccenv", "BERTH_HOME="+filepath.Join(home, "state"), "SSH_AUTH_SOCK=", "USER=root", "BERTH_PUBLISHED_IMAGE="+publishedImage)
	berth := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	onBastion := func() string { return docker(t, "exec", bastion, "cat", "/home/jump/.ssh/authorized_keys") }
	add := []string{"host", "add", "box", "root@box.internal", "--jump", "jump@" + jumpAddr, "--identity", opKey, "--no-guard"}

	// No terminal: the bastion's key is asked about first, with the flag that takes it.
	out, err := berth(add...)
	m := regexp.MustCompile(`--jump-fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if err == nil || m == nil || !strings.Contains(fingerprints(bastion), m[1]) {
		t.Fatalf("add, the bastion's key unconfirmed: %v\n%s", err, out)
	}
	add = append(add, "--jump-fingerprint", m[1])
	// Then the host's, which this machine only sees through the bastion.
	out, err = berth(add...)
	m = regexp.MustCompile(` --fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if err == nil || m == nil || !strings.Contains(fingerprints(target), m[1]) {
		t.Fatalf("add, the host's key unconfirmed: %v\n%s", err, out)
	}
	add = append(add, "--fingerprint", m[1])

	out, err = berth(add...)
	if err != nil || !strings.Contains(out, "Added box") || !strings.Contains(out, "jump@"+jumpAddr) {
		t.Fatalf("add through the bastion: %v\n%s", err, out)
	}
	t.Log(out)
	line := `restrict,port-forwarding,permitopen="box.internal:22",command="false" ssh-ed25519 `
	if a := onBastion(); strings.Count(a, "berth:box") != 1 || !strings.Contains(a, line) {
		t.Fatalf("the bastion's authorized_keys after add:\n%s", a)
	}

	// host ls reaches it through the bastion, with berth's key alone.
	cmd := exec.Command(bin, "--output", "json", "host", "ls")
	cmd.Env = env
	raw, err := cmd.Output()
	var hs ops.Hosts
	if err != nil || json.Unmarshal(raw, &hs) != nil || len(hs.Hosts) != 2 {
		t.Fatalf("host ls: %v\n%s", err, raw)
	}
	if b := hs.Hosts[1]; !b.Reachable || b.Docker == "" || b.Jump != "jump@"+jumpAddr || b.Error != "" {
		t.Fatalf("box: %+v", b)
	}

	// berth's key may forward through the bastion, and do nothing else there.
	key := filepath.Join(cfg, "keys", "box")
	port := jumpAddr[strings.LastIndex(jumpAddr, ":")+1:]
	ssh := exec.Command("ssh", "-i", key, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null", "-p", port, "jump@127.0.0.1", "echo", "shell")
	if o, err := ssh.CombinedOutput(); err == nil || strings.Contains(string(o), "shell") {
		t.Errorf("berth's key ran a command on the bastion: %v\n%s", err, o)
	}

	// A TTY command goes through the operator's ssh binary, through the bastion, with berth's
	// known_hosts for both hops: the exit code comes back.
	ctx := context.Background()
	kh := filepath.Join(cfg, "known_hosts")
	h, err := sshhost.Dial(ctx, sshhost.Config{Addr: "box.internal", User: "root", KnownHosts: kh, IdentityFiles: []string{key}, NoAgent: true,
		Jump: &sshhost.Config{Addr: jumpAddr, User: "jump", IdentityFiles: []string{key}, NoAgent: true}}, local.New())
	if err != nil {
		t.Fatalf("dial through the bastion: %v", err)
	}
	var stderr bytes.Buffer
	err = h.Exec.Run(ctx, host.Cmd{Args: []string{"sh", "-c", "exit 3"}, TTY: true, Stdin: strings.NewReader(""), Stderr: &stderr})
	_ = h.Close()
	var ee *host.ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Errorf("a TTY command through the bastion: %v\n%s", err, stderr.String())
	}

	// rotate-access replaces berth's key on the host and on the bastion, with the operator's
	// access to the bastion.
	before := onBastion()
	if out, err := berth("host", "rotate-access", "box"); err == nil || !strings.Contains(out, "your own access") {
		t.Errorf("rotate-access without access to the bastion: %v\n%s", err, out)
	}
	if onBastion() != before {
		t.Fatal("a refused rotate-access changed the bastion")
	}
	out, err = berth("host", "rotate-access", "box", "--identity", opKey)
	if err != nil || !strings.Contains(out, "removed from box and from the jump host") {
		t.Fatalf("rotate-access: %v\n%s", err, out)
	}
	if a := onBastion(); a == before || strings.Count(a, "berth:box") != 1 || !strings.Contains(a, line) {
		t.Fatalf("the bastion's authorized_keys after rotate-access:\n%s", a)
	}
	if out, err := berth("--output", "json", "host", "ls"); err != nil || !strings.Contains(out, `"reachable": true`) {
		t.Fatalf("host ls with the new key: %v\n%s", err, out)
	}

	// rm takes berth's key off both, and forgets both host keys.
	out, err = berth("host", "rm", "box", "--identity", opKey)
	if err != nil || strings.Contains(out, "by hand") {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if a := onBastion(); strings.Contains(a, "berth:box") {
		t.Errorf("the bastion's authorized_keys after rm:\n%s", a)
	}
	if b, _ := os.ReadFile(kh); len(bytes.TrimSpace(b)) != 0 {
		t.Errorf("known_hosts after rm:\n%s", b)
	}
}
