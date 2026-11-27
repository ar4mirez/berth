package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
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
// never in an answer, an event, a log or the audit log.

// PlacementDir is where a process's token is in its container.
const PlacementDir = "/run/berth/placement"

// PlacementEnv names the file a process finds its token in.
const PlacementEnv = "BERTH_PLACEMENT_FILE"

// placementRefresh is how long before its end a token is replaced.
const placementRefresh = 5 * time.Minute

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

// issuePlacement writes a new token for o's process name into its container and returns when it
// expires. It names the container that runs now: its id and when it started.
func (a *App) issuePlacement(ctx context.Context, o, name string) (time.Time, error) {
	if a.HostName != "" {
		// The org's own machine signs its tokens: its berth serve, through the host API (#148).
		return time.Time{}, nil
	}
	if a.Host.EngineName() == host.EngineApple {
		// Apple's container reports no start time, which a token must name: a process there
		// runs without one, and a consumer that needs a proof refuses it.
		return time.Time{}, nil
	}
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

// PlacementRefresher keeps every supervised process's token fresh on this machine, for as long as
// it runs: it replaces each one placementRefresh before its end, and gives a process in a new
// container a token for that container. `berth serve` runs it until it stops. A token isn't
// renewed while no berth serve runs; it ends at most 15 minutes later.
type PlacementRefresher struct {
	mu      sync.Mutex
	expires map[placementKey]time.Time
}

// Run refreshes every interval until ctx is done; what fails is said on log, and tried again.
func (r *PlacementRefresher) Run(ctx context.Context, newApp func() *App, interval time.Duration, log io.Writer) {
	for {
		r.Once(ctx, newApp(), log)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Once is one round: every running org's processes, each given a token when it has none from
// this refresher or its token ends within placementRefresh.
func (r *PlacementRefresher) Once(ctx context.Context, a *App, log io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
			if p.State == "exited" {
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
