package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ar4mirez/berth/internal/assets"
)

// The state root's lock (#46) serializes read-modify-write of org files (org.env, firewall.txt,
// repos.txt) and port allocation, between berth commands, local or over SSH. It's held only around
// those sections, never across a container restart or a clone, and it's re-entrant within a command.

// lockWaitNotice is how long to wait quietly before saying another command holds the lock.
var lockWaitNotice = 500 * time.Millisecond

// lockTimeout is how long to wait for the lock at most.
var lockTimeout = 2 * time.Minute

// lock takes the state root's lock and returns its release (safe to call more than once). Under
// --read-only nothing is written, so there's nothing to lock.
func (a *App) lock(ctx context.Context) (func(), error) {
	if a.State.ReadOnly {
		return func() {}, nil
	}
	if a.lockDepth > 0 {
		a.lockDepth++
		return a.release(), nil
	}
	p, err := assets.LockPath(a.Host.FS, a.State.Home.Path)
	if err != nil {
		return nil, err
	}
	quick, cancel := context.WithTimeout(ctx, lockWaitNotice)
	u, err := a.Host.FS.Lock(quick, p)
	cancel()
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintf(a.Stderr, "%s: waiting for another %s command on this state root (%s)…\n", Tool, Tool, p)
		long, cancel := context.WithTimeout(ctx, lockTimeout)
		u, err = a.Host.FS.Lock(long, p)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("another %s command still holds %s after %s; try again", Tool, p, lockTimeout)
		}
	}
	if err != nil {
		return nil, err
	}
	a.lockHeld, a.lockDepth = u, 1
	return a.release(), nil
}

// release returns a function that drops one level of the lock, once.
func (a *App) release() func() {
	done := false
	return func() {
		if done {
			return
		}
		done = true
		if a.lockDepth--; a.lockDepth == 0 && a.lockHeld != nil {
			_ = a.lockHeld.Unlock()
			a.lockHeld = nil
		}
	}
}
