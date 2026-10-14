package host

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// apiEngine is a Docker engine API on a unix socket that answers from state and records requests.
type apiEngine struct {
	mu       sync.Mutex
	sock     string
	calls    []string
	created  map[string]any
	images   map[string]bool
	networks map[string]bool
	running  map[string]bool
	logs     []byte
}

func (f *apiEngine) DialEngine(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", f.sock)
}

func newAPIEngine(t *testing.T) *apiEngine {
	t.Helper()
	f := &apiEngine{sock: filepath.Join(t.TempDir(), "docker.sock"), images: map[string]bool{}, networks: map[string]bool{}, running: map[string]bool{}}
	l, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)} // #nosec G112 -- a test server
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

func (f *apiEngine) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	notFound := func() { w.WriteHeader(http.StatusNotFound); _, _ = io.WriteString(w, `{"message":"no such thing"}`) }
	name := func(prefix, suffix string) string { return strings.TrimSuffix(strings.TrimPrefix(p, prefix), suffix) }
	switch {
	case r.Method == "GET" && strings.HasPrefix(p, "/images/"):
		if !f.images[name("/images/", "/json")] {
			notFound()
			return
		}
		_, _ = io.WriteString(w, `{}`)
	case r.Method == "GET" && strings.HasPrefix(p, "/networks/"):
		if !f.networks[name("/networks/", "")] {
			notFound()
			return
		}
		_, _ = io.WriteString(w, `{}`)
	case r.Method == "POST" && p == "/networks/create":
		var b struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.networks[b.Name] = true
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"n1"}`)
	case r.Method == "DELETE" && strings.HasPrefix(p, "/networks/"):
		delete(f.networks, name("/networks/", ""))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "GET" && strings.HasSuffix(p, "/json") && strings.HasPrefix(p, "/containers/"):
		running, ok := f.running[name("/containers/", "/json")]
		if !ok {
			notFound()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": running}})
	case r.Method == "POST" && p == "/containers/create":
		_ = json.NewDecoder(r.Body).Decode(&f.created)
		f.running[r.URL.Query().Get("name")] = false
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"`+r.URL.Query().Get("name")+`"}`)
	case r.Method == "POST" && strings.HasSuffix(p, "/start"):
		f.running[name("/containers/", "/start")] = true
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && strings.HasSuffix(p, "/stop"):
		n := name("/containers/", "/stop")
		if _, ok := f.running[n]; !ok {
			notFound()
			return
		}
		f.running[n] = false
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "DELETE" && strings.HasPrefix(p, "/containers/"):
		n := name("/containers/", "")
		if _, ok := f.running[n]; !ok {
			notFound()
			return
		}
		delete(f.running, n)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "GET" && strings.HasSuffix(p, "/logs"):
		_, _ = w.Write(f.logs)
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

// recExec answers `cat <env file>` with orgEnv and records every other command.
type recExec struct {
	orgEnv string
	ran    [][]string
}

func (r *recExec) Run(_ context.Context, c Cmd) error {
	if c.Args[0] == "cat" {
		_, _ = io.WriteString(c.Stdout, r.orgEnv)
		return nil
	}
	r.ran = append(r.ran, c.Args)
	return nil
}

// TestComposeThroughEngineAPI: with BERTH_ENGINE_API=1, an org's compose up, down and logs go to
// the engine's API, making the network, container and labels compose would (#43); other commands
// still run as they are.
func TestComposeThroughEngineAPI(t *testing.T) {
	ctx := context.Background()
	f := newAPIEngine(t)
	ex := &recExec{orgEnv: "# acme\nSSH_PORT=2201\nTTYD_PORT=7601\nGIT_USER_NAME=\"Ada L\"\nMEM_LIMIT=256m\nCPUS=0.5\n"}
	h := withEngineAPI(&Host{Exec: ex, Docker: f})
	env := []string{"ORG=acme", "ORG_DIR=/s/orgs/acme", "BIND_ADDR=127.0.0.1", "HOST_UID=1000", "HOST_GID=1000",
		"CLAUDE_ENV_IMAGE=berth/claude-env", "IMAGE_TAG=0123456789ab", "CLAUDE_ENV_IMAGE_DIR=/s/berth/image"}
	compose := func(verb ...string) (string, error) {
		var out, errb bytes.Buffer
		err := h.Exec.Run(ctx, Cmd{Args: append([]string{"docker", "compose", "-f", "/s/berth/compose.yml", "--env-file", "/s/orgs/acme/org.env"}, verb...),
			Env: env, Stdout: &out, Stderr: &errb})
		return out.String() + errb.String(), err
	}

	// up, with no image yet: built (compose.yml's build:), then the network, the container, started.
	out, err := compose("up", "-d", "--force-recreate")
	if err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if len(ex.ran) != 1 || strings.Join(ex.ran[0], " ") != "docker build -t berth/claude-env:0123456789ab --build-arg USER_UID=1000 --build-arg USER_GID=1000 --build-arg CLAUDE_CODE_VERSION= /s/berth/image" {
		t.Errorf("the build: %q", ex.ran)
	}
	if !f.networks["claude-acme_default"] || !f.running["claude-acme"] {
		t.Errorf("network %v, running %v\n%s", f.networks, f.running, strings.Join(f.calls, "\n"))
	}
	b, _ := json.Marshal(f.created)
	for _, want := range []string{
		`"Image":"berth/claude-env:0123456789ab"`, `"Hostname":"acme"`,
		`"Env":["SSH_PORT=2201","TTYD_PORT=7601","GIT_USER_NAME=Ada L","MEM_LIMIT=256m","CPUS=0.5","ORG=acme","HOST_UID=1000","HOST_GID=1000"]`,
		`"com.docker.compose.project":"claude-acme"`, `"com.docker.compose.service":"claude"`, `"com.docker.compose.oneoff":"False"`,
		`"Binds":["/s/orgs/acme/workspace:/workspace:rw",`, `"/s/orgs/acme/ssh:/opt/claude-secrets/ssh:ro"`,
		`"PortBindings":{"2222/tcp":[{"HostIp":"127.0.0.1","HostPort":"2201"}],"7681/tcp":[{"HostIp":"127.0.0.1","HostPort":"7601"}]}`,
		`"CapAdd":["NET_ADMIN","NET_RAW"]`, `"RestartPolicy":{"Name":"unless-stopped"}`, `"Memory":268435456`, `"NanoCpus":500000000`,
		`"Init":false`, `"NetworkMode":"claude-acme_default"`, `"Aliases":["claude","claude-acme"]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("create lacks %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "UsernsMode") || strings.Contains(string(b), `"User"`) {
		t.Errorf("a user namespace for Docker:\n%s", b)
	}

	// up again: the image is there (no build), and the running container is made again.
	f.images["berth/claude-env:0123456789ab"] = true
	f.calls, ex.ran = nil, nil
	if out, err := compose("up", "-d", "--force-recreate"); err != nil || !strings.Contains(out, "Recreate") {
		t.Fatalf("recreate: %v\n%s", err, out)
	}
	calls := strings.Join(f.calls, "\n")
	if len(ex.ran) != 0 || !strings.Contains(calls, "POST /containers/claude-acme/stop") || !strings.Contains(calls, "DELETE /containers/claude-acme?force=1") ||
		strings.Contains(calls, "/networks/create") || strings.Index(calls, "DELETE /containers") > strings.Index(calls, "/containers/create") {
		t.Errorf("recreate: built %q\n%s", ex.ran, calls)
	}
	// up without --force-recreate leaves a running container alone; --build builds.
	f.calls = nil
	if out, err := compose("up", "-d", "--build"); err != nil || !strings.Contains(out, "Running") || len(ex.ran) != 1 || slices.Contains(f.calls, "POST /containers/create?name=claude-acme") {
		t.Errorf("up of a running one: %v %q %q\n%s", err, out, ex.ran, strings.Join(f.calls, "\n"))
	}

	// logs: the stream's frames, to stdout and stderr.
	frame := func(stream byte, s string) []byte {
		h := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
		return append(h, s...)
	}
	f.logs = append(frame(1, "ready\n"), frame(2, "warning\n")...)
	var lo, le bytes.Buffer
	err = h.Exec.Run(ctx, Cmd{Args: []string{"docker", "compose", "-f", "/s/berth/compose.yml", "--env-file", "/s/orgs/acme/org.env", "logs", "-f"}, Env: env, Stdout: &lo, Stderr: &le})
	if err != nil || lo.String() != "ready\n" || le.String() != "warning\n" || !slices.Contains(f.calls, "GET /containers/claude-acme/logs?follow=1&stderr=1&stdout=1") {
		t.Errorf("logs: %v %q %q\n%s", err, lo.String(), le.String(), strings.Join(f.calls, "\n"))
	}

	// down: the container and the network go; down again is fine.
	for range 2 {
		if out, err := compose("down"); err != nil || f.running["claude-acme"] || f.networks["claude-acme_default"] {
			t.Errorf("down: %v %s %v %v", err, out, f.running, f.networks)
		}
	}

	// Everything else runs as it is, compose's other verbs included.
	ex.ran = nil
	for _, args := range [][]string{{"docker", "ps"}, {"docker", "compose", "version", "--short"}, {"docker", "compose", "-f", "/x.yml", "--env-file", "/e", "ps"}} {
		_ = h.Exec.Run(ctx, Cmd{Args: args})
	}
	if len(ex.ran) != 3 {
		t.Errorf("passed through: %q", ex.ran)
	}
}

func TestMemBytesAndEnvFile(t *testing.T) {
	for in, want := range map[string]int64{"8g": 8 << 30, "256m": 256 << 20, "512M": 512 << 20, "1024": 1024, "2gb": 2 << 30, "64k": 64 << 10} {
		if got, err := memBytes(in); err != nil || got != want {
			t.Errorf("memBytes(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "lots", "-1g", "0"} {
		if _, err := memBytes(bad); err == nil {
			t.Errorf("memBytes(%q) accepted", bad)
		}
	}
	got := envFileVars([]byte("# c\n\nA=1\nexport B='two words'\nC=\"x\"\nnot a line\nD=\n"))
	want := [][2]string{{"A", "1"}, {"B", "two words"}, {"C", "x"}, {"D", ""}}
	if !slices.Equal(got, want) {
		t.Errorf("envFileVars: %q", got)
	}
}
