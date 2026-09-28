package host

import (
	"context"
	"slices"
	"testing"
)

type recordExec struct{ calls [][]string }

func (r *recordExec) Run(_ context.Context, c Cmd) error {
	r.calls = append(r.calls, c.Args)
	return nil
}

func TestWithEngine(t *testing.T) {
	rec := &recordExec{}
	h := New("local", nil, rec, nil, nil, nil)
	if WithEngine(h, "") != h || WithEngine(h, EngineDocker) != h || h.EngineName() != "docker" {
		t.Fatal("docker must be h itself")
	}
	p := WithEngine(h, EnginePodman)
	if p.EngineName() != "podman" || h.EngineName() != "docker" {
		t.Fatalf("engine names: %s %s", p.EngineName(), h.EngineName())
	}
	_ = p.Exec.Run(context.Background(), Cmd{Args: []string{"docker", "compose", "-f", "x", "up"}})
	_ = p.Exec.Run(context.Background(), Cmd{Args: []string{"id", "-u"}})
	if !slices.Equal(rec.calls[0], []string{"podman", "compose", "-f", "x", "up"}) || !slices.Equal(rec.calls[1], []string{"id", "-u"}) {
		t.Errorf("calls: %v", rec.calls)
	}
	if CheckEngine("podman") != nil || CheckEngine("") != nil || CheckEngine("lxc") == nil {
		t.Error("CheckEngine")
	}
}

func TestParsePodmanPorts(t *testing.T) {
	got, err := parsePodmanPorts([]byte(`[{"Names":["claude-acme"],"Ports":[{"host_ip":"127.0.0.1","container_port":2222,"host_port":2201,"range":1,"protocol":"tcp"},{"host_port":7701,"range":2}]},{"Ports":null}]`))
	if err != nil || !slices.Equal(got, []int{2201, 7701, 7702}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := parsePodmanPorts([]byte("\n")); err != nil || got != nil {
		t.Errorf("empty: %v %v", got, err)
	}
}

// infoExec answers `podman info` with rootless, and records the rest.
type infoExec struct {
	rootless string
	calls    [][]string
}

func (r *infoExec) Run(_ context.Context, c Cmd) error {
	if len(c.Args) > 1 && c.Args[1] == "info" {
		_, _ = c.Stdout.Write([]byte(r.rootless + "\n"))
		return nil
	}
	r.calls = append(r.calls, c.Args)
	return nil
}

func TestRootlessPodmanRun(t *testing.T) {
	ctx := context.Background()
	for _, rl := range []string{"true", "false"} {
		rec := &infoExec{rootless: rl}
		p := WithEngine(New("local", nil, rec, nil, nil, nil), EnginePodman)
		_ = p.Exec.Run(ctx, Cmd{Args: []string{"docker", "run", "--rm", "img", "true"}})
		_ = p.Exec.Run(ctx, Cmd{Args: []string{"docker", "exec", "c", "true"}})
		want := []string{"podman", "run", "--rm", "img", "true"}
		if rl == "true" {
			want = []string{"podman", "run", "--userns=keep-id", "--user", "0:0", "--rm", "img", "true"}
		}
		if !slices.Equal(rec.calls[0], want) || !slices.Equal(rec.calls[1], []string{"podman", "exec", "c", "true"}) {
			t.Errorf("rootless=%s: %v", rl, rec.calls)
		}
		if p.Rootless(ctx) != (rl == "true") {
			t.Errorf("Rootless() with %s", rl)
		}
	}
	if New("local", nil, &infoExec{rootless: "true"}, nil, nil, nil).Rootless(ctx) {
		t.Error("Docker is never rootless Podman")
	}
}
