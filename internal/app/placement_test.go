package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
	"github.com/ar4mirez/berth/internal/placement"
)

// fakeDocker answers the docker calls the refresher makes, for one running org whose container
// and processes the test changes, and records the tokens it is given.
type fakeDocker struct {
	mu        sync.Mutex
	container string
	procs     string
	tokens    []string
}

func (f *fakeDocker) Run(_ context.Context, c host.Cmd) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	args := strings.Join(c.Args, " ")
	out := ""
	switch {
	case args == "docker ps --format {{.Names}}":
		out = "claude-t-proc\n"
	case strings.HasSuffix(args, "inspect -f {{.Id}} claude-t-proc"):
		out = f.container + "\n"
	case strings.HasSuffix(args, "inspect -f {{.State.StartedAt}} claude-t-proc"):
		out = "2026-11-27T18:00:00Z\n"
	case strings.HasSuffix(args, "berth-proc status"):
		out = f.procs
	case strings.Contains(args, "berth-placement"):
		b, _ := io.ReadAll(c.Stdin)
		f.tokens = append(f.tokens, c.Args[len(c.Args)-1]+" "+strings.TrimSpace(string(b)))
	default:
		return &host.ExitError{Code: 1}
	}
	if c.Stdout != nil {
		_, _ = io.WriteString(c.Stdout, out)
	}
	return nil
}

// TestPlacementRefresher: berth serve's refresher gives every running process of a running org a
// token for its current container, gives it again only when the token is near its end or the
// container is a new one, and leaves exited processes alone (#216). Each token verifies with the
// keys berth lists.
func TestPlacementRefresher(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(filepath.Join(state, "orgs", "t-proc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "orgs", "t-proc", "org.env"), []byte("MANAGER=berth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeDocker{container: "c1", procs: `[{"name":"runner","state":"running","pid":7,"started_at":"2026-11-27T18:00:01Z","exit_code":null,"restarts":0,"argv":["runner"],"env_names":[]},` +
		`{"name":"done","state":"exited","pid":null,"started_at":null,"exit_code":0,"restarts":0,"argv":["true"],"env_names":[]}]`}
	h := local.New()
	h.Exec = f
	a := New(config.State{Home: config.Home{Path: state}}, h, nil, &bytes.Buffer{}, &bytes.Buffer{}, func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	})
	var log bytes.Buffer
	r := &PlacementRefresher{}
	ctx := context.Background()

	r.Once(ctx, a, &log)
	r.Once(ctx, a, &log) // the token is fresh: nothing new
	if len(f.tokens) != 1 || !strings.HasPrefix(f.tokens[0], "runner ") {
		t.Fatalf("tokens after two rounds: %v (log %q)", f.tokens, log.String())
	}
	keys, err := a.PlacementKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := placement.Verify(strings.TrimPrefix(f.tokens[0], "runner "), keys.Keys, time.Now())
	if err != nil || c.Org != "t-proc" || c.Container != "c1" || c.Process != "runner" || c.ContainerStartedAt != "2026-11-27T18:00:00Z" {
		t.Fatalf("the token: %+v %v", c, err)
	}
	// A recreated container: a token for the new one at once.
	f.container = "c2"
	r.Once(ctx, a, &log)
	if len(f.tokens) != 2 {
		t.Fatalf("after a recreate: %v", f.tokens)
	}
	if c, err := placement.Verify(strings.TrimPrefix(f.tokens[1], "runner "), keys.Keys, time.Now()); err != nil || c.Container != "c2" {
		t.Errorf("the new container's token: %+v %v", c, err)
	}
	// A token near its end is replaced.
	for k := range r.expires {
		r.expires[k] = time.Now().Add(placementRefresh - time.Second)
	}
	r.Once(ctx, a, &log)
	if len(f.tokens) != 3 {
		t.Errorf("a token near its end wasn't replaced: %v", f.tokens)
	}
	// A process that is gone is forgotten.
	f.procs = "[]"
	r.Once(ctx, a, &log)
	if len(r.expires) != 0 || len(f.tokens) != 3 {
		t.Errorf("after the process ended: %v %v", r.expires, f.tokens)
	}
}
