package host

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
)

// fakeExec answers commands from a table keyed by argv joined with spaces. Missing entries
// behave like a command that isn't installed.
type fakeExec map[string]string

func (f fakeExec) Run(_ context.Context, c Cmd) error {
	out, ok := f[strings.Join(c.Args, " ")]
	if !ok {
		return errors.New(c.Args[0] + ": not found")
	}
	if code, isExit := strings.CutPrefix(out, "exit:"); isExit {
		_, _ = io.WriteString(c.Stderr, "failed")
		return &ExitError{Args: c.Args, Code: len(code)}
	}
	_, _ = io.WriteString(c.Stdout, out)
	return nil
}

func TestFacts(t *testing.T) {
	ex := fakeExec{
		"id -u":           "1000\n",
		"id -g":           "1001\n",
		"uname -m":        "aarch64\n",
		"tailscale ip -4": "100.64.0.7\n",
	}
	got, err := ExecFacts{Exec: ex}.Facts(context.Background())
	want := Facts{UID: 1000, GID: 1001, Arch: "arm64", TailscaleIP: "100.64.0.7"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}

	// Tailscale not installed, or installed but down: both mean no Tailscale IP, not an error.
	delete(ex, "tailscale ip -4")
	if got, err := (ExecFacts{Exec: ex}).Facts(context.Background()); err != nil || got.TailscaleIP != "" {
		t.Errorf("no tailscale: %+v, %v", got, err)
	}
	ex["tailscale ip -4"] = "exit:1"
	if got, err := (ExecFacts{Exec: ex}).Facts(context.Background()); err != nil || got.TailscaleIP != "" {
		t.Errorf("tailscale down: %+v, %v", got, err)
	}

	ex["id -u"] = "root\n"
	if _, err := (ExecFacts{Exec: ex}).Facts(context.Background()); err == nil {
		t.Error("non-numeric uid must be an error")
	}
}

func TestDockerArch(t *testing.T) {
	for in, want := range map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64", "riscv64": "riscv64"} {
		if got := dockerArch(in); got != want {
			t.Errorf("dockerArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSS(t *testing.T) {
	out := `LISTEN 0      128       0.0.0.0:2222  0.0.0.0:*
LISTEN 0      4096   127.0.0.1:2290  0.0.0.0:*
LISTEN 0      128          [::]:22     [::]:*
LISTEN 0      128             *:7790       *:*
garbage
`
	if got, want := parseSS(out), []int{2222, 2290, 22, 7790}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseNetstat(t *testing.T) {
	linux := `Active Internet connections (servers and established)
Proto Recv-Q Send-Q Local Address           Foreign Address         State
tcp        0      0 0.0.0.0:22              0.0.0.0:*               LISTEN
tcp        0      0 127.0.0.1:2290          0.0.0.0:*               LISTEN
tcp        0      0 10.0.0.5:22             10.0.0.9:51000          ESTABLISHED
tcp6       0      0 :::7790                 :::*                    LISTEN
udp        0      0 0.0.0.0:68              0.0.0.0:*
`
	if got, want := parseNetstat(linux), []int{22, 2290, 7790}; !slices.Equal(got, want) {
		t.Errorf("linux: got %v, want %v", got, want)
	}
	darwin := `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
tcp4       0      0  127.0.0.1.2290         *.*                    LISTEN
tcp46      0      0  *.22                   *.*                    LISTEN
tcp6       0      0  ::1.631                *.*                    LISTEN
tcp4       0      0  192.168.1.4.52000      17.0.0.1.443           ESTABLISHED
`
	if got, want := parseNetstat(darwin), []int{2290, 22, 631}; !slices.Equal(got, want) {
		t.Errorf("darwin: got %v, want %v", got, want)
	}
}

func TestPortsInUse(t *testing.T) {
	sock := fakeEngine(t, `[{"Ports":[{"IP":"127.0.0.1","PrivatePort":2222,"PublicPort":2291,"Type":"tcp"},
		{"PrivatePort":53,"PublicPort":5353,"Type":"udp"},{"PrivatePort":80,"Type":"tcp"}]}]`)

	// ss missing: falls back to netstat; engine ports are merged in, sorted, deduplicated.
	ex := fakeExec{"netstat -an": "tcp 0 0 0.0.0.0:2291 0.0.0.0:* LISTEN\ntcp 0 0 0.0.0.0:22 0.0.0.0:* LISTEN\n"}
	got, err := ExecFacts{Exec: ex, Docker: sock}.PortsInUse(context.Background())
	if want := []int{22, 2291}; err != nil || !slices.Equal(got, want) {
		t.Errorf("got %v, %v; want %v", got, err, want)
	}
	if _, err := (ExecFacts{Exec: fakeExec{}}).PortsInUse(context.Background()); err == nil {
		t.Error("with neither ss nor netstat, PortsInUse must fail rather than report no ports")
	}
}

// unixDocker dials a unix socket, standing in for a host's engine.
type unixDocker string

func (s unixDocker) DialEngine(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", string(s))
}

// fakeEngine serves /_ping and /containers/json on a unix socket.
func fakeEngine(t *testing.T, containers string) unixDocker {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ping":
			_, _ = io.WriteString(w, "OK")
		case "/containers/json":
			_, _ = io.WriteString(w, containers)
		default:
			http.Error(w, `{"message":"page not found"}`, http.StatusNotFound)
		}
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return unixDocker(sock)
}

func TestEngine(t *testing.T) {
	e := NewEngine(fakeEngine(t, "[]"))
	if err := e.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := e.GetJSON(context.Background(), "/nope", &v); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404 should be an error naming the status: %v", err)
	}
}

type fsMode = fs.FileMode

// memFS records which methods reached it.
type memFS struct {
	FS
	calls []string
}

func (m *memFS) WriteFile(string, []byte, fsMode) error {
	m.calls = append(m.calls, "WriteFile")
	return nil
}
func (m *memFS) WriteFileAtomic(string, []byte, fsMode) error {
	m.calls = append(m.calls, "WriteFileAtomic")
	return nil
}
func (m *memFS) Chmod(string, fsMode) error    { m.calls = append(m.calls, "Chmod"); return nil }
func (m *memFS) MkdirAll(string, fsMode) error { m.calls = append(m.calls, "MkdirAll"); return nil }
func (m *memFS) Rename(string, string) error   { m.calls = append(m.calls, "Rename"); return nil }
func (m *memFS) Remove(string) error           { m.calls = append(m.calls, "Remove"); return nil }
func (m *memFS) ReadFile(string) ([]byte, error) {
	m.calls = append(m.calls, "ReadFile")
	return nil, nil
}

func TestGuard(t *testing.T) {
	m := &memFS{}
	h := New("test", m, nil, nil, nil, nil)
	if Guard(h, config.State{}) != h {
		t.Error("a writable state must return the host unchanged")
	}
	g := Guard(h, config.State{ReadOnly: true})
	writes := map[string]func() error{
		"WriteFile":       func() error { return g.FS.WriteFile("/x", nil, 0o600) },
		"WriteFileAtomic": func() error { return g.FS.WriteFileAtomic("/x", nil, 0o600) },
		"Chmod":           func() error { return g.FS.Chmod("/x", 0o600) },
		"MkdirAll":        func() error { return g.FS.MkdirAll("/x", 0o700) },
		"Rename":          func() error { return g.FS.Rename("/x", "/y") },
		"Remove":          func() error { return g.FS.Remove("/x") },
	}
	for name, call := range writes {
		if err := call(); !errors.Is(err, config.ErrReadOnly) {
			t.Errorf("%s under read-only: %v", name, err)
		}
	}
	if _, err := g.FS.ReadFile("/x"); err != nil {
		t.Errorf("reads must pass through: %v", err)
	}
	if !slices.Equal(m.calls, []string{"ReadFile"}) {
		t.Errorf("only the read may reach the real FS, got %v", m.calls)
	}
	if h.FS != FS(m) {
		t.Error("Guard must not modify the original host")
	}
}
