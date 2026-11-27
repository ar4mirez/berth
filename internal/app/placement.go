package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
	"github.com/ar4mirez/berth/internal/placement"
)

// Placement proofs (#216, internal/placement, docs/decisions/216-placement-proof.md): a supervised
// process gets a short-lived token, signed by a key berth keeps in its own configuration on the
// host (never in a container), that says which org's container it runs in. It is written into the
// container at PlacementDir/<process>, readable by the container's user and nothing outside it;
// never in an answer, an event, a log or the audit log. Only a process started with placement gets
// one (`proc start --placement`, placement: true), and only while a berth serve keeps it fresh.

// PlacementDir is where a process's token is in its container.
const PlacementDir = "/run/berth/placement"

// PlacementEnv names the file a process finds its token in.
const PlacementEnv = "BERTH_PLACEMENT_FILE"

// placementRefresh is how long before its end a token is replaced.
const placementRefresh = 5 * time.Minute

// PlacementInterval is how often berth serve's refresher goes round. A start with placement
// counts on it while its heartbeat is younger than two rounds.
const PlacementInterval = time.Minute

// PlacementStore is this machine's signing keys, in berth's configuration (0600).
func (a *App) PlacementStore() placement.Store {
	return placement.Store{FS: a.Operator.FS, Path: path.Join(a.hostPaths().Dir, "placement", "keys.json")}
}

// PlacementKeys is `berth placement keys`: the public keys a consumer verifies tokens with.
func (a *App) PlacementKeys(ctx context.Context) (ops.PlacementKeys, error) {
	out := ops.PlacementKeys{Schema: "berth.placement-keys/v1", Keys: []placement.PublicKey{}}
	get := func() ([]placement.PublicKey, error) { return a.PlacementStore().PublicKeys(ctx) }
	if a.State.ReadOnly {
		get = a.PlacementStore().PublicKeysReadOnly // making or retiring a key writes
	}
	ks, err := get()
	if err != nil {
		return out, err
	}
	out.Keys = append(out.Keys, ks...)
	return out, nil
}

// Placement is `berth placement [keys]`.
func (a *App) Placement(ctx context.Context, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "keys") {
		return fmt.Errorf("usage: %s placement keys", Tool)
	}
	ks, err := a.PlacementKeys(ctx)
	if err != nil {
		return err
	}
	return a.printPlacementKeys(ks)
}

func (a *App) printPlacementKeys(ks ops.PlacementKeys) error {
	if a.Output == OutputJSON {
		return a.writeJSON(ks)
	}
	for _, k := range ks.Keys {
		sayf(a.Stdout, "%s  %s  %s  (until %s)\n", k.ID, k.Alg, k.PublicKey, k.NotAfter)
	}
	return nil
}

// placementBeat is the file berth serve's refresher says it runs in: its pid and its last round.
func (a *App) placementBeat() string { return path.Join(a.State.Home.Path, "placement-serve.json") }

type serveBeat struct {
	PID int       `json:"pid"`
	At  time.Time `json:"at"`
}

// pidAlive reports whether a process of that pid runs on this machine.
var pidAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// placementRefreshed reports whether a berth serve that isn't read-only keeps this state root's
// tokens fresh: its heartbeat names a live process, from within two of its rounds.
func (a *App) placementRefreshed() bool {
	b, err := a.Host.FS.ReadFile(a.placementBeat())
	var beat serveBeat
	if err != nil || json.Unmarshal(b, &beat) != nil || beat.PID <= 0 {
		return false
	}
	return pidAlive(beat.PID) && time.Since(beat.At) < 2*PlacementInterval
}

// placementReady refuses a start with placement wherever its token couldn't be written and kept
// fresh, or the start wouldn't happen: so a process never runs believing it has a proof it hasn't,
// and no token is written for a start that won't happen.
func (a *App) placementReady(ctx context.Context, o, name string) error {
	switch {
	case a.HostName != "":
		return &ops.Error{Kind: ops.KindState, Code: 1,
			Msg:  fmt.Sprintf("a placement token for %s@%s is signed on %s, by its %s serve, which can't be reached", o, a.HostName, a.HostName, Tool),
			Hint: "start " + Tool + " serve on " + a.HostName + ", or start the process without --placement"}
	case a.Host.EngineName() == host.EngineApple:
		return &ops.Error{Kind: ops.KindState, Code: 1,
			Msg:  "Apple's container reports no start time, which a placement token names",
			Hint: "start the process without --placement"}
	case !a.placementRefreshed():
		return &ops.Error{Kind: ops.KindState, Code: 1,
			Msg:  "placement needs " + Tool + " serve: nothing would keep the token fresh past its 15 minutes",
			Hint: "start " + Tool + " serve (not --read-only) for this state root, or start the process without --placement"}
	}
	// An image that predates supervised processes, or a name in use, is said as such, before a
	// token is written.
	ps, err := ops.GetProcesses(ctx, a, o)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(ps.Processes, func(p ops.ProcessInfo) bool { return p.Name == name }) {
		return ops.State("a process named %s exists: stop it first", name)
	}
	return nil
}

// issuePlacement writes a new token for o's process name into its container and returns when it
// expires. It names the container that runs now: its id and when it started.
func (a *App) issuePlacement(ctx context.Context, o, name string) (time.Time, error) {
	id, err := a.capture(ctx, true, "docker", "inspect", "-f", "{{.Id}}", contract.Container(o))
	if err != nil {
		return time.Time{}, fmt.Errorf("can't tell %s's container: %w", o, err)
	}
	started := ops.StartedAt(ctx, a, o)
	if started == nil {
		return time.Time{}, fmt.Errorf("can't tell when %s's container started", o)
	}
	tok, c, err := a.PlacementStore().Issue(ctx, placement.Claims{Org: o, Container: strings.TrimSpace(id), ContainerStartedAt: *started, Process: name})
	if err != nil {
		return time.Time{}, err
	}
	// As the container's user: a directory of its own, a file only it reads, replaced whole.
	script := `set -e; umask 077; mkdir -p "$1"; chmod 700 "$1"; cat > "$1/.$2.tmp"; mv -f "$1/.$2.tmp" "$1/$2"`
	var errb bytes.Buffer
	if err := a.Host.Exec.Run(ctx, host.Cmd{
		Args:  []string{"docker", "exec", "-i", "-u", ops.ProcUser, contract.Container(o), "sh", "-c", script, "berth-placement", PlacementDir, name},
		Stdin: strings.NewReader(tok + "\n"), Stdout: io.Discard, Stderr: &errb,
	}); err != nil {
		return time.Time{}, fmt.Errorf("writing %s's placement token into its container: %w %s", name, err, strings.TrimSpace(errb.String()))
	}
	return time.Unix(c.Expires, 0), nil
}

// dropPlacement removes a stopped process's token from its container.
func (a *App) dropPlacement(ctx context.Context, o, name string) {
	_ = a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "exec", "-u", ops.ProcUser, contract.Container(o), "rm", "-f", PlacementDir + "/" + name}, Stdout: io.Discard, Stderr: io.Discard})
}

// placementKey is one token berth keeps fresh: an org's process, in one container.
type placementKey struct{ org, container, process string }

// PlacementRefresher keeps the token of every supervised process started with placement fresh on
// this machine, for as long as it runs: it replaces each one placementRefresh before its end, and
// gives a process in a new container a token for that container. Which processes those are is
// berth-proc's own record of each one's variables (PlacementEnv among them), so a berth serve
// started again finds them too. `berth serve` runs it until it stops, and it keeps a heartbeat
// (placementBeat) that a start with placement asks for.
type PlacementRefresher struct {
	mu      sync.Mutex
	expires map[placementKey]time.Time
}

// Run refreshes every PlacementInterval until ctx is done, and then removes its heartbeat; what
// fails is said on log, and tried again.
func (r *PlacementRefresher) Run(ctx context.Context, newApp func() *App, log io.Writer) {
	a := newApp()
	defer a.dropBeat()
	for {
		r.Once(ctx, a, log)
		select {
		case <-ctx.Done():
			return
		case <-time.After(PlacementInterval):
		}
		a = newApp()
	}
}

// beat says this process keeps the tokens fresh, as of now.
func (a *App) beat() error {
	b, _ := json.Marshal(serveBeat{PID: os.Getpid(), At: time.Now().UTC()})
	if err := a.Host.FS.MkdirAll(path.Dir(a.placementBeat()), 0o700); err != nil {
		return err
	}
	return a.Host.FS.WriteFileAtomic(a.placementBeat(), append(b, '\n'), 0o600)
}

// dropBeat removes the heartbeat, if it is this process's.
func (a *App) dropBeat() {
	b, err := a.Host.FS.ReadFile(a.placementBeat())
	var beat serveBeat
	if err == nil && json.Unmarshal(b, &beat) == nil && beat.PID == os.Getpid() {
		_ = a.Host.FS.Remove(a.placementBeat())
	}
}

// Once is one round: its heartbeat, then every running org's processes started with placement,
// each given a token when it has none from this refresher or its token ends within
// placementRefresh.
func (r *PlacementRefresher) Once(ctx context.Context, a *App, log io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := a.beat(); err != nil {
		sayf(log, "berth serve: placement tokens: %v\n", err)
	}
	if r.expires == nil {
		r.expires = map[placementKey]time.Time{}
	}
	seen := map[placementKey]bool{}
	for _, o := range a.OrgDirs() {
		if !a.IsFile(ops.EnvPath(a, o)) || !ops.Running(ctx, a, o) {
			continue
		}
		ps, err := ops.GetProcesses(ctx, a, o)
		if err != nil || len(ps.Processes) == 0 {
			continue
		}
		id, err := a.capture(ctx, true, "docker", "inspect", "-f", "{{.Id}}", contract.Container(o))
		if err != nil {
			continue
		}
		for _, p := range ps.Processes {
			if p.State == "exited" || !slices.Contains(p.EnvNames, PlacementEnv) {
				continue
			}
			k := placementKey{o, strings.TrimSpace(id), p.Name}
			seen[k] = true
			if exp, ok := r.expires[k]; ok && time.Until(exp) > placementRefresh {
				continue
			}
			exp, err := a.issuePlacement(ctx, o, p.Name)
			if err != nil {
				sayf(log, "berth serve: %s's process %s: %v\n", o, p.Name, err)
				continue
			}
			r.expires[k] = exp
		}
	}
	for k := range r.expires {
		if !seen[k] {
			delete(r.expires, k) // stopped, or its container is gone
		}
	}
}
