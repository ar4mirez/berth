package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/hosts"
)

// The host guard (#48): on a registered host, DOCKER-USER/INPUT rules under every org's own
// firewall keep org containers away from the host itself and from cloud metadata
// (internal/hosts/guard.sh). It runs as a container of berth's image, so it needs no root login
// and persists across reboots through Docker's restart policy.

// installGuard (re)starts the guard on b's host and applies its rules once, now.
func (a *App) installGuard(ctx context.Context, b *App) error {
	fmt.Fprintf(a.Stdout, "Installing the host guard (%s): org containers can't reach the host or 169.254.0.0/16…\n", hosts.GuardContainer)
	if !b.imageFromRelease(ctx) {
		if err := b.Build(ctx, nil); err != nil {
			return fmt.Errorf("building berth's image for the guard: %w", err)
		}
	}
	set, _, err := b.composeAssets()
	if err != nil {
		return err
	}
	_ = b.quietRun(ctx, "docker", "rm", "-f", hosts.GuardContainer)
	if _, err := b.capture(ctx, true, "docker", "run", "-d", "--name", hosts.GuardContainer, "--restart", "always",
		"--network", "host", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW",
		"-v", "/var/run/docker.sock:/var/run/docker.sock", "--label", "berth.guard=1",
		"--entrypoint", "/usr/bin/tini", set.Tag, "--", "bash", "-c", hosts.GuardScript, "guard", "run"); err != nil {
		return fmt.Errorf("starting %s: %w", hosts.GuardContainer, err)
	}
	return b.guard(ctx, "apply", a.Stderr)
}

// guard runs the guard script's cmd in the guard container on a's host.
func (a *App) guard(ctx context.Context, cmd string, stdout io.Writer) error {
	return a.Host.Exec.Run(ctx, host.Cmd{
		Args:   []string{"docker", "exec", hosts.GuardContainer, "bash", "-c", hosts.GuardScript, "guard", cmd},
		Stdout: stdout, Stderr: a.Stderr,
	})
}

// removeGuard takes the rules out and removes the guard container from b's host.
func (a *App) removeGuard(ctx context.Context, b *App) error {
	if b.quietRun(ctx, "docker", "inspect", hosts.GuardContainer) != nil {
		return nil // not installed
	}
	err := b.guard(ctx, "remove", io.Discard)
	if rerr := b.quietRun(ctx, "docker", "rm", "-f", hosts.GuardContainer); rerr != nil {
		err = errors.Join(err, rerr)
	}
	return err
}

// refreshGuard re-applies the guard right after an org started on a registered host, so its new
// network is covered at once rather than within 20 seconds. Best effort: without a guard (host add
// --no-guard) there is nothing to do.
func (a *App) refreshGuard(ctx context.Context) {
	if a.HostName == "" {
		return
	}
	_ = a.Host.Exec.Run(ctx, host.Cmd{
		Args:   []string{"docker", "exec", hosts.GuardContainer, "bash", "-c", hosts.GuardScript, "guard", "apply"},
		Stdout: io.Discard, Stderr: io.Discard,
	})
}

// HostGuard is `berth host guard <name> [on|off|status]`.
func (a *App) HostGuard(ctx context.Context, args []string) error {
	name, sub := nth(args, 0), nth(args, 1)
	if sub == "" {
		sub = "status"
	}
	if name == "" || len(args) > 2 || (sub != "on" && sub != "off" && sub != "status") {
		return fmt.Errorf("usage: %s host guard <name> [on|off|status]", Tool)
	}
	if sub != "status" {
		if err := a.State.Writable("change a host's guard"); err != nil {
			return err
		}
	}
	e, err := a.registered(name)
	if err != nil {
		return err
	}
	h, err := a.dialHost(ctx, e, e.Key)
	if err != nil {
		return fmt.Errorf("host %s: %w", name, err)
	}
	defer func() { _ = h.Close() }()
	b := a.On(name, h, e.Home)
	switch sub {
	case "on":
		if err := a.installGuard(ctx, b); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "The host guard is on for %s. Nothing was restarted.\n", name)
	case "off":
		if err := a.removeGuard(ctx, b); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "The host guard is off for %s: its rules are removed. Nothing was restarted.\n", name)
	default:
		if b.quietRun(ctx, "docker", "inspect", hosts.GuardContainer) != nil {
			fmt.Fprintf(a.Stdout, "The host guard isn't installed on %s (turn it on: %s host guard %s on).\n", name, Tool, name)
			return nil
		}
		state, _ := b.capture(ctx, true, "docker", "inspect", "-f", "{{.State.Status}}", hosts.GuardContainer)
		fmt.Fprintf(a.Stdout, "%s on %s: %s\n", hosts.GuardContainer, name, state)
		return b.guard(ctx, "status", a.Stdout)
	}
	return nil
}
