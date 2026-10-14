package app

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
)

// `berth system service install|uninstall|status|logs` (#63): `berth serve` as a service of this
// user, so the API is there at login without anyone starting it. systemd (a user unit) on Linux,
// launchd (an agent) on macOS. The unit runs berth through the path it was run as (its stable
// link), so an upgrade doesn't leave it pointing at an old version.

// The service's names, at each manager.
const (
	serviceUnit  = "berth-serve.service"
	serviceLabel = "dev.berth.serve"
	// packagedUnit is the user unit the Linux packages install, not enabled (#63).
	packagedUnit = "/usr/lib/systemd/user/" + serviceUnit
)

// serviceEnv are the variables the service is given when they are set where it is installed: a
// service manager starts with almost nothing, and berth must find the engine and its own config.
var serviceEnv = []string{"PATH", "DOCKER_HOST", "XDG_CONFIG_HOME", "BERTH_ENGINE", "BERTH_SOCKET"}

func (a *App) serviceCommand(extra []string) []string {
	self := a.Invoked
	if self == "" {
		self = a.Self
	}
	return append([]string{self, "--home", a.State.Home.Path, "serve"}, extra...)
}

func (a *App) systemdUnitFile() string {
	return path.Join(a.Getenv("HOME"), ".config", "systemd", "user", serviceUnit)
}

func (a *App) launchdPlist() string {
	return path.Join(a.Getenv("HOME"), "Library", "LaunchAgents", serviceLabel+".plist")
}

func (a *App) launchdLog() string {
	return path.Join(a.Getenv("HOME"), "Library", "Logs", "berth", "serve.log")
}

// systemdUnit is the user unit.
func (a *App) systemdUnit(extra []string) string {
	var env strings.Builder
	for _, k := range serviceEnv[1:] { // systemd gives a user unit its own PATH
		if v := a.Getenv(k); v != "" {
			fmt.Fprintf(&env, "Environment=%s=%s\n", k, v)
		}
	}
	return "# Written by " + Tool + " system service install. Remove it with: " + Tool + " system service uninstall\n" +
		"[Unit]\nDescription=berth serve: berth's API on a Unix socket (docs/api.md)\n\n" +
		"[Service]\nExecStart=" + strings.Join(a.serviceCommand(extra), " ") + "\n" + env.String() +
		"Restart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// launchdAgent is the agent's property list.
func (a *App) launchdAgent(extra []string) string {
	return a.launchdPlistFor(serviceLabel, "Written by berth system service install. Remove it with: berth system service uninstall",
		a.serviceCommand(extra), nil, "  <key>RunAtLoad</key><true/>\n  <key>KeepAlive</key><true/>\n", a.launchdLog())
}

// launchdPlistFor is a launchd agent's property list: label runs args, with serviceEnv and env,
// when says when (its keys, as XML), and its output goes to log.
func (a *App) launchdPlistFor(label, comment string, args []string, env [][2]string, when, log string) string {
	var argv, vars strings.Builder
	for _, x := range args {
		argv.WriteString("    <string>" + xmlEscape(x) + "</string>\n")
	}
	for _, k := range serviceEnv {
		if v := a.Getenv(k); v != "" {
			env = append([][2]string{{k, v}}, env...)
		}
	}
	slices.SortStableFunc(env, func(x, y [2]string) int { return slices.Index(serviceEnv, x[0]) - slices.Index(serviceEnv, y[0]) })
	for _, kv := range env {
		vars.WriteString("    <key>" + kv[0] + "</key><string>" + xmlEscape(kv[1]) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- ` + comment + ` -->
<plist version="1.0">
<dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key>
  <array>
` + argv.String() + `  </array>
  <key>EnvironmentVariables</key>
  <dict>
` + vars.String() + `  </dict>
` + when + `  <key>StandardOutPath</key><string>` + xmlEscape(log) + `</string>
  <key>StandardErrorPath</key><string>` + xmlEscape(log) + `</string>
</dict>
</plist>
`
}

// serviceManager is the service manager here: "launchd", "systemd", or an error saying why
// neither.
func (a *App) serviceManager(ctx context.Context) (string, error) {
	// BERTH_SERVICE_MANAGER names it outright (the tests; a machine where the guess is wrong).
	switch m := a.Getenv("BERTH_SERVICE_MANAGER"); m {
	case "launchd", "systemd":
		return m, nil
	case "":
	default:
		return "", fmt.Errorf("BERTH_SERVICE_MANAGER is launchd or systemd, not %q", m)
	}
	if os, _ := a.capture(ctx, true, "uname", "-s"); strings.TrimSpace(os) == "Darwin" {
		return "launchd", nil
	}
	if a.quietRun(ctx, "systemctl", "--user", "show-environment") == nil {
		return "systemd", nil
	}
	return "", &ops.Error{Kind: ops.KindState, Code: 1, Msg: "there is no systemd user manager here (and this isn't macOS): berth can't install a service",
		Hint: "run " + Tool + " serve under whatever supervises processes on this machine"}
}

// launchdTarget is the agent in this user's GUI domain.
func (a *App) launchdTarget(ctx context.Context) (domain, target string) {
	return a.launchdTargetFor(ctx, serviceLabel)
}

// launchdTargetFor is the agent label in this user's GUI domain.
func (a *App) launchdTargetFor(ctx context.Context, label string) (domain, target string) {
	uid, _ := a.capture(ctx, true, "id", "-u")
	domain = "gui/" + strings.TrimSpace(uid)
	return domain, domain + "/" + label
}

// Service is `berth system service install [--listen ADDR] | uninstall | status | logs [-f]`.
func (a *App) Service(ctx context.Context, args []string) error {
	usage := &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("usage: %s service install [--listen ADDR] | uninstall | status | logs [-f], each with --system for the whole machine's", Tool)}
	if len(args) == 0 {
		return usage
	}
	verb, rest := args[0], args[1:]
	if a.HostName != "" {
		return fmt.Errorf("a service is installed on the machine %s runs on: run this there", Tool)
	}
	if slices.Contains(rest, "--system") {
		return a.systemService(ctx, verb, slices.DeleteFunc(slices.Clone(rest), func(s string) bool { return s == "--system" }), usage)
	}
	mgr, err := a.serviceManager(ctx)
	if err != nil {
		return err
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
		return a.serviceInstall(ctx, mgr, extra)
	case "uninstall":
		if len(rest) != 0 {
			return usage
		}
		if err := a.State.Writable("uninstall a service"); err != nil {
			return err
		}
		return a.serviceUninstall(ctx, mgr)
	case "status":
		if len(rest) != 0 {
			return usage
		}
		return a.serviceStatus(ctx, mgr)
	case "logs":
		follow := len(rest) == 1 && (rest[0] == "-f" || rest[0] == "--follow")
		if len(rest) != 0 && !follow {
			return usage
		}
		return a.serviceLogs(ctx, mgr, follow)
	}
	return usage
}

func (a *App) serviceInstall(ctx context.Context, mgr string, extra []string) error {
	if mgr == "launchd" {
		plist := a.launchdPlist()
		for _, d := range []string{path.Dir(plist), path.Dir(a.launchdLog())} {
			if err := a.Host.FS.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		if err := a.Host.FS.WriteFileAtomic(plist, []byte(a.launchdAgent(extra)), 0o644); err != nil {
			return err
		}
		domain, target := a.launchdTarget(ctx)
		_ = a.quietRun(ctx, "launchctl", "bootout", target) // an older one, so the new file is the one loaded
		if err := a.passthrough(ctx, false, "launchctl", "bootstrap", domain, plist); err != nil {
			return fmt.Errorf("launchd didn't take %s (%v)", plist, err) //nolint:errorlint // not wrapped: launchctl's exit code would silence this message
		}
		sayf(a.Stdout, "Installed %s (launchd): %s serve runs now, and at each login.\n", serviceLabel, Tool)
		sayf(a.Stdout, "  agent  %s\n  log    %s\n", plist, a.launchdLog())
		return nil
	}
	unit := a.systemdUnitFile()
	if err := a.Host.FS.MkdirAll(path.Dir(unit), 0o755); err != nil {
		return err
	}
	if err := a.Host.FS.WriteFileAtomic(unit, []byte(a.systemdUnit(extra)), 0o644); err != nil {
		return err
	}
	if err := a.passthrough(ctx, false, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	// enable for the next logins, restart for now: an installed one picks up the new unit.
	if err := a.passthrough(ctx, false, "systemctl", "--user", "enable", serviceUnit); err != nil {
		return err
	}
	if err := a.passthrough(ctx, false, "systemctl", "--user", "restart", serviceUnit); err != nil {
		return err
	}
	sayf(a.Stdout, "Installed %s (systemd, your user): %s serve runs now, and at each login.\n", serviceUnit, Tool)
	sayf(a.Stdout, "  unit   %s\n  log    %s system service logs\n", unit, Tool)
	a.sayLinger(ctx)
	return nil
}

// sayLinger explains what a user unit's lifetime is, when the user doesn't linger.
func (a *App) sayLinger(ctx context.Context) {
	user := a.Getenv("USER")
	if user == "" {
		u, _ := a.capture(ctx, true, "id", "-un")
		user = strings.TrimSpace(u)
	}
	if v, err := a.capture(ctx, true, "loginctl", "show-user", user, "--property=Linger", "--value"); err == nil && strings.TrimSpace(v) != "yes" {
		sayf(a.Stdout, "It runs while %s is logged in, and stops at the last logout. To keep it running, and start it at boot: loginctl enable-linger %s\n", user, user)
	}
}

func (a *App) serviceUninstall(ctx context.Context, mgr string) error {
	file := a.systemdUnitFile()
	if mgr == "launchd" {
		file = a.launchdPlist()
	}
	if mgr == "systemd" && !a.isFile(file) && a.isFile(packagedUnit) {
		// The package's own unit stays with the package: it is only stopped and disabled.
		_ = a.quietRun(ctx, "systemctl", "--user", "disable", "--now", serviceUnit)
		sayf(a.Stdout, "Stopped and disabled %s; the package keeps the file.\n", packagedUnit)
		return nil
	}
	if !a.isFile(file) {
		sayf(a.Stdout, "No service is installed (%s isn't there).\n", file)
		return nil
	}
	if mgr == "launchd" {
		_, target := a.launchdTarget(ctx)
		_ = a.quietRun(ctx, "launchctl", "bootout", target)
	} else {
		_ = a.quietRun(ctx, "systemctl", "--user", "disable", "--now", serviceUnit)
	}
	if err := a.Host.FS.Remove(file); err != nil {
		return err
	}
	if mgr == "systemd" {
		_ = a.quietRun(ctx, "systemctl", "--user", "daemon-reload")
	}
	sayf(a.Stdout, "Removed the service (%s). %s serve is stopped; nothing else changed.\n", file, Tool)
	return nil
}

func (a *App) serviceStatus(ctx context.Context, mgr string) error {
	if mgr == "launchd" {
		plist := a.launchdPlist()
		if !a.isFile(plist) {
			sayf(a.Stdout, "not installed (%s system service install)\n", Tool)
			return &Exit{Code: 3}
		}
		_, target := a.launchdTarget(ctx)
		out, err := a.capture(ctx, true, "launchctl", "print", target)
		state := "not loaded"
		if err == nil {
			state = "loaded"
			for _, l := range strings.Split(out, "\n") {
				// The first one is the agent's; further down are its endpoints' own.
				if k, v, ok := strings.Cut(strings.TrimSpace(l), " = "); ok && k == "state" {
					state = v
					break
				}
			}
		}
		sayf(a.Stdout, "%s (launchd): %s\n  agent  %s\n  log    %s\n", serviceLabel, state, plist, a.launchdLog())
		if state != "running" {
			return &Exit{Code: 3}
		}
		return nil
	}
	unit := a.systemdUnitFile()
	if !a.isFile(unit) && a.isFile(packagedUnit) {
		unit = packagedUnit // the package's, enabled by hand (systemctl --user enable --now berth-serve)
	}
	if !a.isFile(unit) {
		if a.isFile(systemUnitFile()) {
			sayf(a.Stdout, "not installed for your user; the whole machine's is: %s system service status --system\n", Tool)
		} else {
			sayf(a.Stdout, "not installed (%s system service install)\n", Tool)
		}
		return &Exit{Code: 3}
	}
	active, _ := a.capture(ctx, true, "systemctl", "--user", "is-active", serviceUnit)
	enabled, _ := a.capture(ctx, true, "systemctl", "--user", "is-enabled", serviceUnit)
	active, enabled = strings.TrimSpace(active), strings.TrimSpace(enabled)
	sayf(a.Stdout, "%s (systemd, your user): %s, %s\n  unit   %s\n", serviceUnit, active, enabled, unit)
	a.sayLinger(ctx)
	if active != "active" {
		return &Exit{Code: 3}
	}
	return nil
}

func (a *App) serviceLogs(ctx context.Context, mgr string, follow bool) error {
	var argv []string
	if mgr == "launchd" {
		argv = []string{"tail", "-n", "100"}
		if follow {
			argv = append(argv, "-f")
		}
		argv = append(argv, a.launchdLog())
	} else {
		argv = []string{"journalctl", "--user", "-u", serviceUnit, "-n", "100", "--no-pager"}
		if follow {
			argv = append(argv, "-f")
		}
	}
	return a.runLogs(ctx, argv)
}

// runLogs runs argv (journalctl, tail) onto stdout; a failure says what it printed.
func (a *App) runLogs(ctx context.Context, argv []string) error {
	var errb bytes.Buffer
	if err := a.Host.Exec.Run(ctx, host.Cmd{Args: argv, Stdout: a.Stdout, Stderr: &errb}); err != nil {
		return fmt.Errorf("%s: %w %s", argv[0], err, strings.TrimSpace(errb.String()))
	}
	return nil
}
