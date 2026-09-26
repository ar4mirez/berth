package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/hosts"
)

// Active-host leases (#47). Once hosts are registered, the same org could exist on several of them
// (after a migrate or a restore elsewhere), and two running copies would share one Remote Control
// login and git identity. The lease names the one host that may run it: up and restart refuse on
// any other, and --take-lease moves it, stopping the copy on the old host first.
//
// Without registered hosts there are no leases: nothing is read or written, exactly as before.

// base is the App berth started with, on this machine (a itself unless a is an org@host App).
func (a *App) base() *App {
	if a.root != nil {
		return a.root
	}
	return a
}

// appOn is an App for the host named name ("local" or a registered host), and a func that releases
// it. Tests replace it.
func (a *App) appOn(ctx context.Context, name string) (*App, func(), error) {
	if a.HostAppFn != nil {
		return a.HostAppFn(ctx, name)
	}
	b := a.base()
	if name == hosts.Local {
		return b, func() {}, nil
	}
	h, _, done, err := b.At(ctx, "@"+name)
	return h, done, err
}

// checkLease runs before up or restart start o on a's host.
func (a *App) checkLease(ctx context.Context, o string) error {
	entries := a.base().otherHosts()
	if len(entries) == 0 {
		return nil
	}
	unlock, err := a.lockHosts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	p := a.hostPaths()
	leases, err := hosts.LoadLeases(a.Operator.FS, p)
	if err != nil {
		return err
	}
	here := a.hostLabel()
	holder, held := leases.Orgs[o]
	if held && holder == here {
		a.markLease(o, here)
		return nil
	}

	// Where else the org is: the lease holder, or (with no lease yet) every other host that has it.
	var others []string
	if held {
		others = []string{holder}
	} else {
		names := []string{hosts.Local}
		for _, e := range entries {
			names = append(names, e.Name)
		}
		for _, n := range names {
			if n == here {
				continue
			}
			b, done, err := a.appOn(ctx, n)
			if err != nil {
				return fmt.Errorf("can't check whether %s is on %s too: %w", o, n, err)
			}
			if b.isFile(b.Orgs.EnvPath(o)) {
				others = append(others, n)
			}
			done()
		}
	}
	if len(others) > 0 && !a.TakeLease {
		if held {
			return fmt.Errorf("%s runs on %s (it holds the lease), not here (%s). To move it here: %s up %s@%s --take-lease (that stops it on %s first)",
				o, holder, here, Tool, o, here, holder)
		}
		return fmt.Errorf("%s is also on %s. Say which host runs it: %s up %s@%s --take-lease stops it there first",
			o, strings.Join(others, ", "), Tool, o, here)
	}

	// Stop the other copies, then move the lease. A host berth no longer knows can't be stopped:
	// the lease just moves, and says so.
	for _, n := range others {
		if !isHost(n, entries) {
			fmt.Fprintf(a.Stdout, "%s's lease was on %s, which is no longer a registered host; moving it here.\n", o, n)
			continue
		}
		b, done, err := a.appOn(ctx, n)
		if err != nil {
			return fmt.Errorf("can't reach %s to stop %s there, so the lease stays: %w", n, o, err)
		}
		if b.running(ctx, o) {
			fmt.Fprintf(a.Stdout, "Stopping %s on %s first (the lease moves here; its work there stops).\n", o, n)
			if err := b.Down(ctx, o); err != nil {
				done()
				return fmt.Errorf("stopping %s on %s: %w; the lease stays there", o, n, err)
			}
		}
		_ = b.Host.FS.Remove(hosts.LeaseMarker(b.State.Home.Path, o))
		done()
	}
	leases.Orgs[o] = here
	if err := hosts.SaveLeases(a.Operator.FS, p, leases); err != nil {
		return err
	}
	a.markLease(o, here)
	if len(others) > 0 {
		fmt.Fprintf(a.Stdout, "%s's lease is now on %s.\n", o, here)
	}
	return nil
}

// isHost reports whether name is this machine or a registered host.
func isHost(name string, entries []hosts.Entry) bool {
	return name == hosts.Local || slices.ContainsFunc(entries, func(e hosts.Entry) bool { return e.Name == name })
}

// markLease writes the lease marker in a's state root (best effort: the operator's file decides).
func (a *App) markLease(o, here string) {
	f := hosts.LeaseMarker(a.State.Home.Path, o)
	b, err := a.Host.FS.ReadFile(f)
	if err == nil && strings.HasPrefix(string(b), "active: "+here+"\n") || err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err := a.Host.FS.MkdirAll(path.Dir(f), 0o700); err != nil {
		return
	}
	_ = a.Host.FS.WriteFileAtomic(f, []byte(fmt.Sprintf("active: %s\nsince: %s\n", here, time.Now().UTC().Format(time.RFC3339))), 0o600)
}

// dropLeases removes the leases held by host name (host rm), and returns the orgs they were for.
func (a *App) dropLeases(name string) ([]string, error) {
	p := a.hostPaths()
	leases, err := hosts.LoadLeases(a.Operator.FS, p)
	if err != nil {
		return nil, err
	}
	var orgs []string
	for o, h := range leases.Orgs {
		if h == name {
			orgs = append(orgs, o)
			delete(leases.Orgs, o)
		}
	}
	if len(orgs) == 0 {
		return nil, nil
	}
	return orgs, hosts.SaveLeases(a.Operator.FS, p, leases)
}
