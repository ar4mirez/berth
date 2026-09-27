package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"runtime"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/upgrade"
	"github.com/ar4mirez/berth/internal/version"
)

// Backup schedules on a registered host (#49): `berth schedule … --host <name>`. The host gets a
// berth binary (the signed release for its architecture, verified as `berth upgrade` does) and runs
// its own `berth schedule` with only the operator's public recipients: the private backup.key never
// leaves this machine (docs/plan.md, decision 4). The scheduled job calls ~/.local/bin/berth there,
// so upgrading berth on the host keeps it working.

// splitHostFlag takes `--host <name>` out of schedule's arguments.
func splitHostFlag(args []string) (name string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] != "--host" {
			rest = append(rest, args[i])
			continue
		}
		if i+1 >= len(args) || args[i+1] == "" {
			return "", nil, errors.New("--host needs a value")
		}
		i++
		name = args[i]
	}
	return name, rest, nil
}

// scheduleOnHost is Schedule with --host.
func (a *App) scheduleOnHost(ctx context.Context, name string, args []string) error {
	action := "on"
	for _, x := range args {
		switch x {
		case "status", "run", "now", "off", "--off":
			action = x
		}
	}
	if action != "status" {
		if err := a.State.Writable("change a host's backup schedule"); err != nil {
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
	rhome, err := remoteHome(ctx, h)
	if err != nil {
		return err
	}
	link := rhome + "/.local/bin/" + Tool
	remote := []string{link, "--home", e.Home, "schedule"}

	if action != "on" {
		if _, err := h.FS.Stat(link); err != nil {
			if action == "status" {
				fmt.Fprintf(a.Stdout, "No backup schedule on %s (berth isn't installed there).\n", name)
				return nil
			}
			return fmt.Errorf("berth isn't installed on %s, so it has no schedule", name)
		}
		return h.Exec.Run(ctx, host.Cmd{Args: append(remote, args...), Stdout: a.Stdout, Stderr: a.Stderr})
	}

	// The operator's public recipients: $BERTH_BACKUP_RECIPIENTS, else backup.key's public half.
	recips := a.backupEnv("BACKUP_RECIPIENTS")
	if recips == "" {
		pub, ok := a.publicKey(a.keyFile())
		if !ok {
			return fmt.Errorf("a host's scheduled backups encrypt to your key, and you have none: run '%s keygen' here first", Tool)
		}
		recips = strings.ReplaceAll(pub, "\n", ",")
	}

	if err := a.pushBerth(ctx, name, h, rhome); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Scheduling on %s; its backups encrypt to %s (the private key stays here).\n", name, recips)
	return h.Exec.Run(ctx, host.Cmd{
		Args: append(remote, args...), Env: []string{"BERTH_BACKUP_RECIPIENTS=" + recips},
		Stdout: a.Stdout, Stderr: a.Stderr,
	})
}

// pushBerth installs this berth's version on h: ~/.local/opt/berth/<version>/berth, linked from
// ~/.local/bin/berth by that binary's own install. A release pushes the signed release binary for
// the host's architecture, verified; a development build (tests, CI) pushes itself, when the host's
// platform is this one, and says so.
func (a *App) pushBerth(ctx context.Context, name string, h *host.Host, rhome string) error {
	osName, err := remoteOut(ctx, h, "uname", "-s")
	if err != nil {
		return err
	}
	if osName != "Linux" {
		return fmt.Errorf("%s runs %s; berth schedules backups on Linux hosts", name, osName)
	}
	facts, err := h.Facts.Facts(ctx)
	if err != nil {
		return err
	}
	ver := strings.TrimPrefix(version.Version, "v")
	var bin []byte
	release := !strings.Contains(ver, "snapshot") && ver != "dev" && a.Upgrader != nil && a.Upgrader.Verifier != nil
	if release {
		rel, err := a.Upgrader.Client.Get(ctx, "v"+ver)
		if err != nil {
			return fmt.Errorf("the release for this berth (v%s): %w", ver, err)
		}
		fmt.Fprintf(a.Stdout, "Fetching berth v%s for linux/%s, verifying its signature and checksum…\n", ver, facts.Arch)
		if bin, err = upgrade.Fetch(ctx, a.Upgrader.Client, a.Upgrader.Verifier, rel, "linux", facts.Arch); err != nil {
			return err
		}
	} else {
		if runtime.GOOS != "linux" || facts.Arch != runtime.GOARCH {
			return fmt.Errorf("this is a development build of berth (%s), which can only be pushed to a host of its own platform (linux/%s, not linux/%s); use a release", ver, runtime.GOARCH, facts.Arch)
		}
		fmt.Fprintf(a.Stderr, "warning: this is a development build of berth (%s): pushing it to %s as it is, unverified\n", ver, name)
		if bin, err = a.Operator.FS.ReadFile(a.Self); err != nil {
			return err
		}
	}
	dir := path.Join(rhome, ".local", "opt", Tool, ver)
	dst := path.Join(dir, Tool)
	if err := h.FS.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := h.FS.WriteFileAtomic(dst, bin, 0o755); err != nil {
		return err
	}
	var o, e bytes.Buffer
	err = h.Exec.Run(ctx, host.Cmd{Args: []string{dst, "--version"}, Stdout: &o, Stderr: &e})
	out := strings.TrimSpace(o.String())
	if err != nil {
		// 127 from the shell for a file that is there: it can't be executed (another platform, or a
		// binary linked against a C library the host lacks).
		return fmt.Errorf("berth on %s (%s) doesn't run there: %w %s", name, dst, err, strings.TrimSpace(e.String()))
	}
	if !strings.Contains(out, " "+ver+" ") {
		return fmt.Errorf("berth on %s doesn't run as %s (%q)", name, ver, out)
	}
	fmt.Fprintf(a.Stdout, "Installed berth %s on %s (%s).\n", ver, name, dst)
	return h.Exec.Run(ctx, host.Cmd{Args: []string{dst, "install"}, Stdout: a.Stdout, Stderr: a.Stderr})
}
