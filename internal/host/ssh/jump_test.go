package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ar4mirez/berth/internal/host/local"
)

func signer(t *testing.T) gossh.Signer {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := gossh.NewSignerFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// sshServer serves SSH on a local port with hostKey, to client alone. A session's sftp subsystem
// serves this machine's files; a direct-tcpip channel goes to forward(host:port), which the
// server records.
type sshServer struct {
	addr string
	mu   sync.Mutex
	asks []string
}

func (s *sshServer) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asks...)
}

func serveSSH(t *testing.T, hostKey gossh.Signer, client gossh.PublicKey, forward func(string) string) *sshServer {
	t.Helper()
	cfg := &gossh.ServerConfig{PublicKeyCallback: func(_ gossh.ConnMetadata, k gossh.PublicKey) (*gossh.Permissions, error) {
		if string(k.Marshal()) != string(client.Marshal()) {
			return nil, errors.New("unknown key")
		}
		return nil, nil
	}}
	cfg.AddHostKey(hostKey)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	s := &sshServer{addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.handle(c, cfg, forward)
		}
	}()
	return s
}

func (s *sshServer) handle(c net.Conn, cfg *gossh.ServerConfig, forward func(string) string) {
	_, chans, reqs, err := gossh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	go gossh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "direct-tcpip":
			var p struct {
				Host       string
				Port       uint32
				OriginHost string
				OriginPort uint32
			}
			if gossh.Unmarshal(nc.ExtraData(), &p) != nil || forward == nil {
				_ = nc.Reject(gossh.Prohibited, "no forwarding")
				continue
			}
			to := net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))
			s.mu.Lock()
			s.asks = append(s.asks, to)
			s.mu.Unlock()
			out, err := net.Dial("tcp", forward(to))
			if err != nil {
				_ = nc.Reject(gossh.ConnectionFailed, err.Error())
				continue
			}
			ch, rq, err := nc.Accept()
			if err != nil {
				_ = out.Close()
				continue
			}
			go gossh.DiscardRequests(rq)
			go func() { _, _ = io.Copy(ch, out); _ = ch.CloseWrite() }()
			go func() { _, _ = io.Copy(out, ch); _ = out.Close() }()
		case "session":
			ch, rq, err := nc.Accept()
			if err != nil {
				continue
			}
			go func() {
				for r := range rq {
					var sub struct{ Name string }
					if r.Type == "subsystem" && gossh.Unmarshal(r.Payload, &sub) == nil && sub.Name == "sftp" {
						_ = r.Reply(true, nil)
						srv, err := sftp.NewServer(ch)
						if err == nil {
							_ = srv.Serve()
						}
						_ = ch.Close()
						return
					}
					_ = r.Reply(false, nil)
				}
			}()
		default:
			_ = nc.Reject(gossh.UnknownChannelType, "no")
		}
	}
}

// TestDialThroughJump: a host behind a jump host is reached through a direct-tcpip channel that
// the jump host opens to the host's own address, never dialed from here, and each hop's key is
// checked against berth's known_hosts on its own (#99).
func TestDialThroughJump(t *testing.T) {
	client, jumpKey, targetKey := signer(t), signer(t), signer(t)
	target := serveSSH(t, targetKey, client.PublicKey(), nil)
	// The jump host knows the host as box.internal:22, a name this machine can't resolve.
	jump := serveSSH(t, jumpKey, client.PublicKey(), func(to string) string {
		if to == "box.internal:22" {
			return target.addr
		}
		return "127.0.0.1:1"
	})

	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	pin := func(lines ...string) {
		t.Helper()
		if err := os.WriteFile(kh, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	jumpLine := knownhosts.Line([]string{knownhosts.Normalize(jump.addr)}, jumpKey.PublicKey())
	targetLine := knownhosts.Line([]string{knownhosts.Normalize("box.internal:22")}, targetKey.PublicKey())
	file := filepath.Join(dir, "hello")
	if err := os.WriteFile(file, []byte("through the jump host\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := func(timeout time.Duration) Config {
		return Config{Addr: "box.internal", User: "ops", KnownHosts: kh, Signers: []gossh.Signer{client}, NoAgent: true, Timeout: timeout,
			Jump: &Config{Addr: jump.addr, User: "jump", Signers: []gossh.Signer{client}, NoAgent: true}}
	}
	ctx := context.Background()

	// Both keys pinned: files on the host, through the channel the jump host opened.
	pin(jumpLine, targetLine)
	h, err := Dial(ctx, cfg(5*time.Second), local.New())
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.FS.ReadFile(file)
	_ = h.Close()
	if err != nil || string(b) != "through the jump host\n" {
		t.Errorf("read through the jump host: %q, %v", b, err)
	}
	if got := jump.asked(); len(got) != 1 || got[0] != "box.internal:22" {
		t.Errorf("the jump host was asked for %v, want [box.internal:22]", got)
	}

	// The jump host's key unknown: refused at the jump host, named as such.
	pin(targetLine)
	_, err = Dial(ctx, cfg(5*time.Second), local.New())
	var ue *UnknownHostError
	if !errors.As(err, &ue) || ue.Host != jump.addr || !strings.HasPrefix(err.Error(), "ssh jump host jump@") {
		t.Errorf("unknown jump host key: %v", err)
	}
	// The host's key unknown: refused at the host, with its address as the jump host knows it.
	pin(jumpLine)
	_, err = Dial(ctx, cfg(5*time.Second), local.New())
	if !errors.As(err, &ue) || ue.Host != "box.internal:22" {
		t.Errorf("unknown host key: %v", err)
	}
	// A host key that changed is refused, whatever the jump host's.
	pin(jumpLine, knownhosts.Line([]string{knownhosts.Normalize("box.internal:22")}, signer(t).PublicKey()))
	if _, err = Dial(ctx, cfg(5*time.Second), local.New()); !errors.Is(err, ErrHostKeyChanged) {
		t.Errorf("changed host key: %v", err)
	}

	// A host that never answers through the jump host: the handshake still times out.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	quiet := serveSSH(t, jumpKey, client.PublicKey(), func(string) string { return silent.Addr().String() })
	c := cfg(time.Second)
	c.Jump.Addr = quiet.addr
	pin(knownhosts.Line([]string{knownhosts.Normalize(quiet.addr)}, jumpKey.PublicKey()), targetLine)
	start := time.Now()
	if _, err = Dial(ctx, c, local.New()); err == nil || time.Since(start) > 4*time.Second {
		t.Errorf("a silent host behind the jump host: %v after %s", err, time.Since(start))
	}
}
