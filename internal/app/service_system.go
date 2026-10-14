package app

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// `berth system service … --system` (#63): `berth serve` as a unit of the whole machine
// (/etc/systemd/system), started at boot without a login or linger. It runs as the account that
// ran sudo, with that account's home, so it serves the same state root and config that account's
// own commands use. Linux only: a launchd agent already runs as its user.

const systemUnitDir = "/etc/systemd/system"

func systemUnitFile() string { return path.Join(systemUnitDir, serviceUnit) }

// systemUnit is the system unit for user, whose home is home.
func (a *App) systemUnit(user, home string, extra []string) string {
	self := a.Invoked
	if self == "" {
		self = a.Self
	}
	var env strings.Builder
	fmt.Fprintf(&env, "Environment=HOME=%s\n", home)
	for _, k := range serviceEnv[1:] {
		if v := a.Getenv(k); v != "" {
			fmt.Fprintf(&env, "Environment=%s=%s\n", k, v)
		}
	}
	// No --home: berth finds the account's state root as its own commands do, from HOME.
	return "# Written by " + Tool + " system service install --system. Remove it with: sudo " + Tool + " system service uninstall --system\n" +
		"[Unit]\nDescription=berth serve: berth's API on a Unix socket (docs/api.md), as " + user + "\n" +
		"Wants=network-online.target\nAfter=network-online.target docker.service\n\n" +
		"[Service]\nUser=" + user + "\nExecStart=" + strings.Join(append([]string{self, "serve"}, extra...), " ") + "\n" + env.String() +
		"Restart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=multi-user.target\n"
}

// systemService is Service with --system.
func (a *App) systemService(ctx context.Context, verb string, rest []string, usage error) error {
	if m := a.Getenv("BERTH_SERVICE_MANAGER"); m == "launchd" {
		return a.notSystemd()
	} else if m == "" {
		if os, _ := a.capture(ctx, true, "uname", "-s"); strings.TrimSpace(os) == "Darwin" {
			return a.notSystemd()
		}
	}
	unit := systemUnitFile()
	root := func(what string) error {
		if uid, _ := a.capture(ctx, true, "id", "-u"); strings.TrimSpace(uid) != "0" {
			return &ops.Error{Kind: ops.KindRefused, Code: 1, Msg: "--system " + what + " a unit for the whole machine: it needs root",
				Hint: "run it with sudo, from the account the service runs as"}
		}
		return nil
	}
	switch verb {
	case "install":
		var extra []string
		switch {
		case len(rest) == 2 && rest[0] == "--listen":
			extra = rest
		case len(rest) != 0:
			return usage
		}
		if err := a.State.Writable("install a service"); err != nil {
			return err
		}
		if err := root("installs"); err != nil {
			return err
		}
		user := a.Getenv("SUDO_USER")
		if user == "" || user == "root" {
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "--system runs berth serve as the account that ran sudo, and there is none",
				Hint: "run it with sudo, from the account the service runs as"}
		}
		pw, err := a.capture(ctx, true, "getent", "passwd", user)
		fields := strings.Split(strings.TrimSpace(pw), ":")
		if err != nil || len(fields) < 7 || !path.IsAbs(fields[5]) {
			return fmt.Errorf("can't find %s's home directory (getent passwd %s)", user, user)
		}
		home := fields[5]
		if err := a.Host.FS.WriteFileAtomic(unit, []byte(a.systemUnit(user, home, extra)), 0o644); err != nil {
			return err
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", serviceUnit}, {"restart", serviceUnit}} {
			if err := a.passthrough(ctx, false, append([]string{"systemctl"}, args...)...); err != nil {
				return err
			}
		}
		sayf(a.Stdout, "Installed %s (systemd, the whole machine): %s serve runs now as %s, and at boot.\n", serviceUnit, Tool, user)
		sayf(a.Stdout, "  unit    %s\n  socket  %s/.config/berth/berth.sock (%s's commands find it there)\n  log     %s system service logs --system\n",
			unit, home, user, Tool)
		if a.isFile(path.Join(home, ".config", "systemd", "user", serviceUnit)) {
			sayf(a.Stdout, "%s also has a user unit for it: remove that one, as %s, with: %s system service uninstall\n", user, user, Tool)
		}
		return nil
	case "uninstall":
		if len(rest) != 0 {
			return usage
		}
		if err := a.State.Writable("uninstall a service"); err != nil {
			return err
		}
		if !a.isFile(unit) {
			sayf(a.Stdout, "No system service is installed (%s isn't there).\n", unit)
			return nil
		}
		if err := root("removes"); err != nil {
			return err
		}
		_ = a.quietRun(ctx, "systemctl", "disable", "--now", serviceUnit)
		if err := a.Host.FS.Remove(unit); err != nil {
			return err
		}
		_ = a.quietRun(ctx, "systemctl", "daemon-reload")
		sayf(a.Stdout, "Removed the service (%s). %s serve is stopped; nothing else changed.\n", unit, Tool)
		return nil
	case "status":
		if len(rest) != 0 {
			return usage
		}
		if !a.isFile(unit) {
			sayf(a.Stdout, "not installed (sudo %s system service install --system)\n", Tool)
			return &Exit{Code: 3}
		}
		active, _ := a.capture(ctx, true, "systemctl", "is-active", serviceUnit)
		enabled, _ := a.capture(ctx, true, "systemctl", "is-enabled", serviceUnit)
		active, enabled = strings.TrimSpace(active), strings.TrimSpace(enabled)
		sayf(a.Stdout, "%s (systemd, the whole machine): %s, %s\n  unit   %s\n", serviceUnit, active, enabled, unit)
		if active != "active" {
			return &Exit{Code: 3}
		}
		return nil
	case "logs":
		follow := slices.Equal(rest, []string{"-f"}) || slices.Equal(rest, []string{"--follow"})
		if len(rest) != 0 && !follow {
			return usage
		}
		argv := []string{"journalctl", "-u", serviceUnit, "-n", "100", "--no-pager"}
		if follow {
			argv = append(argv, "-f")
		}
		return a.runLogs(ctx, argv)
	}
	return usage
}

func (a *App) notSystemd() error {
	return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "--system is for systemd on Linux: on macOS the launchd agent already runs as you, at each login"}
}

// PackagedUnit is the user unit the Linux packages install at /usr/lib/systemd/user, not enabled
// (#63): berth serve from /usr/bin, with what a user unit gets from systemd. `systemctl --user
// enable --now berth-serve` turns it on; `berth system service install` writes the user's own,
// which takes its place.
func PackagedUnit() string {
	return "# Installed by berth's package, not enabled. Turn it on with: systemctl --user enable --now berth-serve\n" +
		"# (berth system service install writes your own in ~/.config/systemd/user, which takes this one's place.)\n" +
		"[Unit]\nDescription=berth serve: berth's API on a Unix socket (docs/api.md)\n\n" +
		"[Service]\nExecStart=/usr/bin/" + Tool + " serve\nRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
}
