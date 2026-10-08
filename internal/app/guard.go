package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/hosts"
	"github.com/ar4mirez/berth/internal/ops"
)

// The host guard (#48): on a registered host, DOCKER-USER/INPUT rules under every org's own
// firewall keep org containers away from the host itself and from cloud metadata
// (internal/hosts/guard.sh). It runs as a container of berth's image, so it needs no root login
// and persists across reboots through Docker's restart policy.
//
// On a rootful Podman (#97) it is the same container on Podman's socket, with its rules hooked
// into FORWARD. A rootless Podman (#153) keeps org networks in the user's own network namespace:
// there the guard is started without a nested user namespace, so that it may enter that namespace
// and keep its rules in it (guardArgs).

// guardUnavailable is why b's host can't have the guard ("" when it can).
func guardUnavailable(_ context.Context, b *App) string {
	switch e := b.Host.EngineName(); e {
	case host.EngineDocker, host.EnginePodman:
		return ""
	default:
		return "not available with " + e + " (docs/engines.md)"
	}
}

// guardArgs are the `docker run` arguments that differ by engine: where the guard runs, and the
// engine's API socket, through which it lists org networks.
func guardArgs(ctx context.Context, b *App) ([]string, error) {
	host0 := []string{"--network", "host", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW"}
	switch {
	case b.Host.EngineName() != host.EnginePodman:
		return append(host0, "-v", "/var/run/docker.sock:/var/run/docker.sock"), nil
	case !b.Host.Rootless(ctx):
		return append(host0, "-v", "/run/podman/podman.sock:/var/run/docker.sock"), nil
	}
	// Rootless: Podman says where its socket and its run root are; the network namespace is a file
	// under the run root. The directories are mounted, not the files: the socket comes and goes,
	// and guard.sh has what the namespace's file needs.
	out, err := b.capture(ctx, true, "docker", "info", "--format", "{{.Host.RemoteSocket.Path}}|{{.Store.RunRoot}}")
	sock, runRoot, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(out), "unix://"), "|")
	if err != nil || !ok || !path.IsAbs(sock) || !path.IsAbs(runRoot) {
		return nil, fmt.Errorf("can't tell where the rootless Podman's socket and run root are (%q): %w", out, err)
	}
	return append(host0,
		// The user's own namespace, not one nested in it (which is what berth gives its other
		// containers there): only from it may the guard enter the rootless network namespace.
		"--userns=host", "--cap-add", "SYS_ADMIN", "--security-opt", "label=disable",
		"-v", path.Dir(sock)+":/berth-sock", "-e", "BERTH_GUARD_SOCK=/berth-sock/"+path.Base(sock),
		"-v", runRoot+":/berth-run", "-e", "BERTH_GUARD_NETNS=/berth-run/networks/rootless-netns/rootless-netns"), nil
}

// installGuard (re)starts the guard on b's host and applies its rules once, now.
func (a *App) installGuard(ctx context.Context, b *App) error {
	if why := guardUnavailable(ctx, b); why != "" {
		return fmt.Errorf("the host guard is %s", why)
	}
	sayf(a.Stdout, "Installing the host guard (%s): org containers can't reach the host or 169.254.0.0/16…\n", hosts.GuardContainer)
	if !b.imageFromRelease(ctx) {
		if err := b.Build(ctx, nil); err != nil {
			return fmt.Errorf("building berth's image for the guard: %w", err)
		}
	}
	set, _, err := b.composeAssets()
	if err != nil {
		return err
	}
	where, err := guardArgs(ctx, b)
	if err != nil {
		return err
	}
	_ = b.quietRun(ctx, "docker", "rm", "-f", hosts.GuardContainer)
	run := append([]string{"run", "-d", "--name", hosts.GuardContainer, "--restart", "always"}, where...)
	run = append(run, "--label", "berth.guard=1",
		"--entrypoint", "/usr/bin/tini", set.Tag, "--", "bash", "-c", hosts.GuardScript, "guard", "run")
	if _, err := b.capture(ctx, true, append([]string{"docker"}, run...)...); err != nil {
		return fmt.Errorf("starting %s: %w", hosts.GuardContainer, err)
	}
	// The container may not take an exec the instant after its start (Podman: "container state
	// improper"), and a rootless one may be on its way round a restart: a few tries, then the error.
	for range 5 {
		if b.quietRun(ctx, "docker", "exec", hosts.GuardContainer, "bash", "-c", hosts.GuardScript, "guard", "apply") == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
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
	apply := func() error {
		return a.Host.Exec.Run(ctx, host.Cmd{
			Args:   []string{"docker", "exec", hosts.GuardContainer, "bash", "-c", hosts.GuardScript, "guard", "apply"},
			Stdout: io.Discard, Stderr: io.Discard,
		})
	}
	if apply() == nil || !a.Host.Rootless(ctx) {
		return
	}
	// A rootless Podman's network namespace came up with this org, after the guard started: the
	// guard sees it only from a fresh start (guard.sh).
	if a.quietRun(ctx, "docker", "restart", hosts.GuardContainer) == nil {
		_ = apply()
	}
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
		sayf(a.Stdout, "The host guard is on for %s. Nothing was restarted.\n", name)
	case "off":
		if err := a.removeGuard(ctx, b); err != nil {
			return err
		}
		sayf(a.Stdout, "The host guard is off for %s: its rules are removed. Nothing was restarted.\n", name)
	default:
		// The guard's own report can fail after the line above it is printed.
		g, err := ops.GetHostGuard(ctx, b, name)
		if a.Output == OutputJSON {
			if err != nil {
				return err
			}
			return a.writeJSON(g)
		}
		if !g.Installed {
			sayf(a.Stdout, "The host guard isn't installed on %s (turn it on: %s host guard %s on).\n", name, Tool, name)
			return nil
		}
		sayf(a.Stdout, "%s on %s: %s\n", hosts.GuardContainer, name, g.State)
		fmt.Fprint(a.Stdout, g.Rules)
		return err
	}
	return nil
}
