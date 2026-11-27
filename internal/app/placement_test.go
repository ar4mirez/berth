package app

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/ar4mirez/berth/internal/ops"
	"github.com/ar4mirez/berth/internal/placement"
)

// fakeDocker answers the docker calls the refresher makes, for one running org whose container
// and processes the test changes, and records the tokens it is given.
type fakeDocker struct {
	mu        sync.Mutex
	container string
	procs     string
	tokens    []string
	starts    []string
	// gate, when set, holds each berth-proc status until all it counts have asked.
	gate *sync.WaitGroup
	// started is whether berth-proc started runner; file is its token file in the container, and
	// lost counts its removals once it had started.
	started bool
	file    string
	lost    int
}

func (f *fakeDocker) Run(_ context.Context, c host.Cmd) error {
	if f.gate != nil && strings.HasSuffix(strings.Join(c.Args, " "), "berth-proc status") {
		f.gate.Done()
		f.gate.Wait()
	}
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
	case strings.HasSuffix(args, "berth-proc start runner"):
		if f.gate != nil && f.started {
			_, _ = io.WriteString(c.Stderr, "berth-proc: a process named runner exists: stop it first\n")
			return &host.ExitError{Code: 3}
		}
		f.started = true
		b, _ := io.ReadAll(c.Stdin)
		f.starts = append(f.starts, string(b))
		out = `{"name":"runner","state":"running","pid":7,"started_at":"2026-11-27T18:00:01Z","exit_code":null,"restarts":0,"argv":["runner"],"env_names":[]}`
	case strings.Contains(args, "berth-placement"):
		b, _ := io.ReadAll(c.Stdin)
		f.tokens = append(f.tokens, c.Args[len(c.Args)-1]+" "+strings.TrimSpace(string(b)))
		f.file = strings.TrimSpace(string(b))
	case strings.HasSuffix(args, "rm -f "+PlacementDir+"/runner"):
		f.file = ""
		if f.started {
			f.lost++
		}
	default:
		return &host.ExitError{Code: 1}
	}
	if c.Stdout != nil {
		_, _ = io.WriteString(c.Stdout, out)
	}
	return nil
}

// placementApp is an app for one running org, t-proc, whose docker is f.
func placementApp(t *testing.T, f *fakeDocker) *App {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(filepath.Join(state, "orgs", "t-proc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "orgs", "t-proc", "org.env"), []byte("MANAGER=berth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := local.New()
	h.Exec = f
	return New(config.State{Home: config.Home{Path: state}}, h, nil, &bytes.Buffer{}, &bytes.Buffer{}, func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	})
}

// TestPlacementRefresher: berth serve's refresher gives every running process of a running org
// that was started with placement a token for its current container, gives it again only when the
// token is near its end or the container is a new one, and leaves exited processes and ones
// started without placement alone (#216). Each token verifies with the keys berth lists. Each
// round is a heartbeat a start with placement counts on.
func TestPlacementRefresher(t *testing.T) {
	f := &fakeDocker{container: "c1", procs: `[{"name":"runner","state":"running","pid":7,"started_at":"2026-11-27T18:00:01Z","exit_code":null,"restarts":0,"argv":["runner"],"env_names":["BERTH_PLACEMENT_FILE"]},` +
		`{"name":"plain","state":"running","pid":8,"started_at":"2026-11-27T18:00:01Z","exit_code":null,"restarts":0,"argv":["plain"],"env_names":["API_KEY"]},` +
		`{"name":"done","state":"exited","pid":null,"started_at":null,"exit_code":0,"restarts":0,"argv":["true"],"env_names":["BERTH_PLACEMENT_FILE"]}]`}
	a := placementApp(t, f)
	var log bytes.Buffer
	r := &PlacementRefresher{}
	ctx := context.Background()

	if a.placementRefreshed() {
		t.Fatal("a heartbeat before any round")
	}
	r.Once(ctx, a, &log)
	r.Once(ctx, a, &log) // the token is fresh: nothing new
	if len(f.tokens) != 1 || !strings.HasPrefix(f.tokens[0], "runner ") {
		t.Fatalf("tokens after two rounds: %v (log %q)", f.tokens, log.String())
	}
	if !a.placementRefreshed() {
		t.Error("no heartbeat after a round")
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

// TestPlacementStart: a start with placement gets its token before berth-proc starts it, and is
// told where it is; a start without placement gets neither. With placement and nothing to keep the
// token fresh (no berth serve, or one gone), or for an org on a registered host, the start is
// refused with a state error, and neither a token nor the process is made (#216).
func TestPlacementStart(t *testing.T) {
	ctx := context.Background()
	f := &fakeDocker{container: "c1", procs: "[]"}
	a := placementApp(t, f)
	spec := func(placement bool) ops.ProcSpec {
		return ops.ProcSpec{Argv: []string{"runner"}, Placement: placement}
	}

	if _, err := a.ProcStart(ctx, "t-proc", "runner", spec(false)); err != nil || len(f.tokens) != 0 || len(f.starts) != 1 || strings.Contains(f.starts[0], PlacementEnv) {
		t.Fatalf("without placement: %v, tokens %v, starts %v", err, f.tokens, f.starts)
	}
	refused := func(what string) {
		t.Helper()
		f.tokens, f.starts = nil, nil
		_, err := a.ProcStart(ctx, "t-proc", "runner", spec(true))
		if !ops.IsKind(err, ops.KindState) || len(f.tokens) != 0 || len(f.starts) != 0 {
			t.Errorf("%s: %v, tokens %v, starts %v", what, err, f.tokens, f.starts)
		}
	}
	refused("no berth serve")
	if err := a.beat(); err != nil {
		t.Fatal(err)
	}
	alive := pidAlive
	t.Cleanup(func() { pidAlive = alive })
	pidAlive = func(int) bool { return false }
	refused("a berth serve that ended")
	pidAlive = alive

	f.tokens, f.starts = nil, nil
	if _, err := a.ProcStart(ctx, "t-proc", "runner", spec(true)); err != nil || len(f.tokens) != 1 || len(f.starts) != 1 ||
		!strings.Contains(f.starts[0], `"`+PlacementEnv+`":"`+PlacementDir+`/runner"`) {
		t.Fatalf("with placement: %v, tokens %v, starts %v", err, f.tokens, f.starts)
	}

	a.dropBeat()
	if a.placementRefreshed() {
		t.Error("the heartbeat outlived its serve")
	}
	if err := a.beat(); err != nil {
		t.Fatal(err)
	}
	a.HostName = "box" // reached over ssh: no host API answered
	refused("an org on a registered host")
}

// TestPlacementStartRace: two --placement starts of one name at once both pass the check for a
// name in use; berth-proc starts one and refuses the other (exit 3). The one refused leaves the
// token file alone, as it is the running process's: exactly one process runs, and its token is
// there throughout and verifies, naming it (#216).
func TestPlacementStartRace(t *testing.T) {
	ctx := context.Background()
	f := &fakeDocker{container: "c1", procs: "[]", gate: &sync.WaitGroup{}}
	a := placementApp(t, f)
	if err := a.beat(); err != nil {
		t.Fatal(err)
	}
	f.gate.Add(2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := a.ProcStart(ctx, "t-proc", "runner", ops.ProcSpec{Argv: []string{"runner"}, Placement: true})
			errs <- err
		}()
	}
	var ok, inUse int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ops.ErrProcInUse):
			inUse++
		default:
			t.Errorf("a start: %v", err)
		}
	}
	if ok != 1 || inUse != 1 || len(f.starts) != 1 {
		t.Fatalf("started %d, refused as in use %d, berth-proc starts %d", ok, inUse, len(f.starts))
	}
	if f.lost != 0 || f.file == "" {
		t.Fatalf("the running process's token was removed %d times; file %q", f.lost, f.file)
	}
	keys, err := a.PlacementKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := placement.Verify(f.file, keys.Keys, time.Now()); err != nil || c.Org != "t-proc" || c.Process != "runner" || c.Container != "c1" {
		t.Errorf("the running process's token: %+v %v", c, err)
	}
}
