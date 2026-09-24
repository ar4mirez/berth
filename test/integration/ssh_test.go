//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
	sshhost "github.com/ar4mirez/berth/internal/host/ssh"
)

// sshTarget is the sshd + dind fixture container, seen from the test.
type sshTarget struct {
	addr    string // 127.0.0.1:<published port>
	hostKey gossh.PublicKey
	keyFile string // the client's private key, for TTY commands (the ssh binary)
}

func startSSHTarget(t *testing.T) *sshTarget {
	t.Helper()
	requireDocker(t)
	const img, name = "berth-test-sshd", "berth-t-sshd"

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	mustDo(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	mustDo(t, err)
	block, err := gossh.MarshalPrivateKey(priv, "berth integration test")
	mustDo(t, err)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	mustDo(t, os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600))

	docker(t, "build", "-q", "-t", img, "testdata/sshd")
	_ = exec.Command("docker", "rm", "-f", name).Run()
	docker(t, "run", "-d", "--privileged", "--name", name,
		"-e", "AUTHORIZED_KEY="+strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))),
		"-e", "DOCKER_TLS_CERTDIR=", "-p", "127.0.0.1::22", img)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
			t.Logf("fixture logs:\n%s", out)
		}
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	// Wait for the inner dockerd (sshd starts first, so it is up by then).
	deadline := time.Now().Add(90 * time.Second)
	for exec.Command("docker", "exec", name, "docker", "info").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("dind never came up")
		}
		time.Sleep(time.Second)
	}
	hk, _, _, _, err := gossh.ParseAuthorizedKey([]byte(docker(t, "exec", name, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	mustDo(t, err)
	return &sshTarget{addr: strings.TrimSpace(docker(t, "port", name, "22/tcp")), hostKey: hk, keyFile: keyFile}
}

func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Fatalf("no Docker daemon: %v", err) // fail, don't skip: this test only runs where Docker is expected
	}
}

func (s *sshTarget) config(knownHosts string) sshhost.Config {
	return sshhost.Config{Addr: s.addr, User: "root", KnownHosts: knownHosts, IdentityFiles: []string{s.keyFile}, NoAgent: true}
}

// pin writes a known_hosts that trusts key for the fixture.
func (s *sshTarget) pin(t *testing.T, key gossh.PublicKey) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "known_hosts")
	mustDo(t, os.WriteFile(p, []byte(knownhosts.Line([]string{knownhosts.Normalize(s.addr)}, key)+"\n"), 0o600))
	return p
}

func randomKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	mustDo(t, err)
	k, err := gossh.NewPublicKey(pub)
	mustDo(t, err)
	return k
}

func TestSSHHost(t *testing.T) {
	target := startSSHTarget(t)
	ctx := context.Background()
	operator := local.New()

	t.Run("host key policy", func(t *testing.T) {
		kh := filepath.Join(t.TempDir(), "berth", "known_hosts")
		if _, err := sshhost.Dial(ctx, target.config(kh), operator); !errors.Is(err, sshhost.ErrUnknownHost) {
			t.Errorf("unknown host: %v, want ErrUnknownHost", err)
		}
		if _, err := os.Stat(kh); !errors.Is(err, fs.ErrNotExist) {
			t.Error("refusing an unknown host must not create known_hosts")
		}

		cfg := target.config(target.pin(t, randomKey(t)))
		cfg.AcceptNewHostKey = true
		if _, err := sshhost.Dial(ctx, cfg, operator); !errors.Is(err, sshhost.ErrHostKeyChanged) {
			t.Errorf("changed key with AcceptNewHostKey: %v, want ErrHostKeyChanged", err)
		}

		cfg = target.config(kh)
		cfg.AcceptNewHostKey = true
		h, err := sshhost.Dial(ctx, cfg, operator)
		if err != nil {
			t.Fatalf("AcceptNewHostKey: %v", err)
		}
		_ = h.Close()
		b, err := os.ReadFile(kh)
		mustDo(t, err)
		// The server has rsa, ecdsa and ed25519 keys; with nothing on file it may pick any.
		if !strings.HasPrefix(string(b), knownhosts.Normalize(target.addr)+" ") {
			t.Errorf("recorded %q", b)
		}
		// Recorded: now it connects without the flag.
		h, err = sshhost.Dial(ctx, target.config(kh), operator)
		if err != nil {
			t.Fatalf("recorded key: %v", err)
		}
		_ = h.Close()
	})

	// Pin only ed25519: the client must negotiate that key type out of the server's three.
	h, err := sshhost.Dial(ctx, target.config(target.pin(t, target.hostKey)), operator)
	if err != nil {
		t.Fatalf("dial with pinned ed25519 key: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	sh := func(t *testing.T, script string) string {
		t.Helper()
		var out bytes.Buffer
		if err := h.Exec.Run(ctx, host.Cmd{Args: []string{"sh", "-c", script}, Stdout: &out}); err != nil {
			t.Fatalf("%s: %v", script, err)
		}
		return strings.TrimSpace(out.String())
	}

	t.Run("fs", func(t *testing.T) {
		dir := "/root/berth-t/orgs/acme"
		mustDo(t, h.FS.MkdirAll(dir, 0o700))
		if got := sh(t, "stat -c %a /root/berth-t /root/berth-t/orgs "+dir); got != "700\n700\n700" {
			t.Errorf("MkdirAll modes %q, want 700 each (exact, regardless of the remote umask)", got)
		}
		env := dir + "/org.env"
		mustDo(t, h.FS.WriteFile(env, []byte("SSH_PORT=2290\n"), 0o600))
		ino := sh(t, "stat -c '%i %a' "+env)
		if !strings.HasSuffix(ino, " 600") {
			t.Errorf("created as %q, want mode 600", ino)
		}
		mustDo(t, h.FS.WriteFile(env, []byte("SSH_PORT=2291\n"), 0o644))
		if got := sh(t, "stat -c '%i %a' "+env); got != ino {
			t.Errorf("in-place rewrite changed inode/mode: %q -> %q", ino, got)
		}
		if b, err := h.FS.ReadFile(env); err != nil || string(b) != "SSH_PORT=2291\n" {
			t.Errorf("ReadFile: %q, %v", b, err)
		}

		fw := dir + "/firewall.txt"
		mustDo(t, h.FS.WriteFile(fw, []byte("old"), 0o644))
		before := sh(t, "stat -c %i "+fw)
		mustDo(t, h.FS.WriteFileAtomic(fw, []byte("mode on\n"), 0o600))
		if got := sh(t, "stat -c '%i %a' "+fw); strings.HasPrefix(got, before+" ") || !strings.HasSuffix(got, " 600") {
			t.Errorf("atomic write: %q (old inode %s); want a new inode with mode 600", got, before)
		}
		if got := sh(t, "ls -A "+dir); got != "firewall.txt\norg.env" {
			t.Errorf("leftover temp files: %q", got)
		}

		mustDo(t, h.FS.Chmod(fw, 0o640))
		if fi, err := h.FS.Stat(fw); err != nil || fi.Mode().Perm() != 0o640 {
			t.Errorf("Stat after Chmod: %v, %v", fi, err)
		}
		mustDo(t, h.FS.Rename(fw, env)) // replaces an existing file (POSIX rename)
		entries, err := h.FS.ReadDir(dir)
		if err != nil || len(entries) != 1 || entries[0].Name() != "org.env" || entries[0].IsDir() {
			t.Errorf("ReadDir: %v, %v", entries, err)
		}
		if _, err := h.FS.ReadFile(dir + "/missing"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("missing file: %v, want fs.ErrNotExist", err)
		}
		if err := h.FS.Remove("/root/berth-t/orgs"); err == nil {
			t.Error("Remove of a non-empty directory must fail")
		}
		mustDo(t, h.FS.Remove(env))
		mustDo(t, h.FS.Remove(dir))
	})

	t.Run("exec", func(t *testing.T) {
		var out bytes.Buffer
		err := h.Exec.Run(ctx, host.Cmd{
			Args:   []string{"sh", "-c", `printf '%s|%s|%s|' "$PWD" "$T" "$1"; cat`, "argv0", "it's $HOME"},
			Env:    []string{"T=a b$c"},
			Dir:    "/tmp",
			Stdin:  strings.NewReader("in"),
			Stdout: &out,
		})
		if want := "/tmp|a b$c|it's $HOME|in"; err != nil || out.String() != want {
			t.Errorf("got %q, %v; want %q", out.String(), err, want)
		}
		var ee *host.ExitError
		if err := h.Exec.Run(ctx, host.Cmd{Args: []string{"sh", "-c", "exit 7"}}); !errors.As(err, &ee) || ee.Code != 7 {
			t.Errorf("exit 7: %v", err)
		}
		if err := h.Exec.Run(ctx, host.Cmd{Args: []string{"sh", "-c", "kill -TERM $$"}}); !errors.As(err, &ee) || ee.Code != 143 {
			t.Errorf("SIGTERM: %v, want code 143", err)
		}
		// Unlike local exec, a missing binary is reported by the remote shell: exit 127.
		if err := h.Exec.Run(ctx, host.Cmd{Args: []string{"berth-no-such-binary"}}); !errors.As(err, &ee) || ee.Code != 127 {
			t.Errorf("missing binary: %v, want code 127", err)
		}

		tctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := h.Exec.Run(tctx, host.Cmd{Args: []string{"sleep", "30"}}); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*time.Second {
			t.Errorf("cancelled command: %v after %s", err, time.Since(start))
		}
	})

	t.Run("tty uses the ssh binary and keeps the exit code", func(t *testing.T) {
		var stderr bytes.Buffer
		err := h.Exec.Run(ctx, host.Cmd{Args: []string{"sh", "-c", "exit 3"}, TTY: true, Stdin: strings.NewReader(""), Stderr: &stderr})
		var ee *host.ExitError
		if !errors.As(err, &ee) || ee.Code != 3 {
			t.Errorf("got %v, want exit 3 (stderr: %s)", err, stderr.String())
		}
	})

	t.Run("lock", func(t *testing.T) {
		const name = "/root/berth-t.lock"
		first, err := h.FS.Lock(ctx, name)
		mustDo(t, err)

		wctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if _, err := h.FS.Lock(wctx, name); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second lock while held: %v", err)
		}
		got := make(chan error, 1)
		go func() {
			u, err := h.FS.Lock(ctx, name)
			if err == nil {
				err = u.Unlock()
			}
			got <- err
		}()
		time.Sleep(300 * time.Millisecond)
		mustDo(t, first.Unlock())
		select {
		case err := <-got:
			mustDo(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("waiter never got the lock after Unlock")
		}
	})

	t.Run("docker over streamlocal", func(t *testing.T) {
		e := host.NewEngine(h.Docker)
		mustDo(t, e.Ping(ctx))
		var v struct {
			APIVersion string `json:"ApiVersion"`
		}
		mustDo(t, e.GetJSON(ctx, "/version", &v))
		if v.APIVersion == "" {
			t.Error("no ApiVersion from the remote engine")
		}
	})

	t.Run("facts", func(t *testing.T) {
		f, err := h.Facts.Facts(ctx)
		mustDo(t, err)
		if want := (host.Facts{UID: 0, GID: 0, Arch: runtime.GOARCH}); f != want {
			t.Errorf("got %+v, want %+v", f, want)
		}
		// A container published on the remote engine counts as a port in use there.
		const port = 2295
		sh(t, "docker run -d --name t-ports -p "+strconv.Itoa(port)+":80 busybox:1.37 sleep 300 >/dev/null")
		defer sh(t, "docker rm -f t-ports >/dev/null")
		ports, err := h.Facts.PortsInUse(ctx)
		mustDo(t, err)
		for _, p := range []int{22, port} {
			if !slices.Contains(ports, p) {
				t.Errorf("port %d not reported in use: %v", p, ports)
			}
		}
	})
}
