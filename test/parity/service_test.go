package parity

import (
	"strings"
	"testing"
)

// TestBerthService: `service install|uninstall|status|logs` (#63) writes a unit that runs berth
// serve through the path berth was run as, hands it to the service manager, and takes it away
// again: a systemd user unit, or a launchd agent.
func TestBerthService(t *testing.T) {
	svc := func(manager string, rules []Rule, files map[string]File, args ...string) Result {
		t.Helper()
		if files == nil {
			files = twoOrgs
		}
		return run(t, Berth(berthBin), Scenario{Args: append([]string{"service"}, args...), Files: files, Rules: rules,
			Env: []string{"BERTH_SERVICE_MANAGER=" + manager, "DOCKER_HOST=unix:///x/docker.sock"}})
	}
	calls := func(r Result) string { return strings.Join(r.Calls, "\n") }

	// systemd
	const unitPath = "home/.config/systemd/user/berth-serve.service"
	r := svc("systemd", nil, nil, "install")
	unit := treeLine(r, unitPath)
	for _, want := range []string{`ExecStart=<SELF> serve\n`, `Environment=DOCKER_HOST=unix:///x/docker.sock\n`, `Restart=on-failure`, `WantedBy=default.target`} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit lacks %s:\n%s", want, unit)
		}
	}
	for _, want := range []string{`"--user" "daemon-reload"`, `"--user" "enable" "berth-serve.service"`, `"--user" "restart" "berth-serve.service"`} {
		if !strings.Contains(calls(r), want) {
			t.Errorf("systemd wasn't asked to %s:\n%s", want, calls(r))
		}
	}
	if r.Exit != 0 || !strings.Contains(r.Stdout, "Installed berth-serve.service") || !strings.Contains(r.Stdout, "loginctl enable-linger") {
		t.Errorf("install: exit %d\n%s%s", r.Exit, r.Stdout, r.Stderr)
	}
	// With linger, nothing to explain; --listen reaches the unit.
	r = svc("systemd", []Rule{{Bin: "loginctl", Match: `show-user`, Stdout: "yes\n"}}, nil, "install", "--listen", "127.0.0.1:8443")
	if strings.Contains(r.Stdout, "enable-linger") || !strings.Contains(treeLine(r, unitPath), `ExecStart=<SELF> serve --listen 127.0.0.1:8443\n`) {
		t.Errorf("install --listen, lingering:\n%s\n%s", r.Stdout, treeLine(r, unitPath))
	}
	installed := merge(twoOrgs, map[string]File{unitPath: {Content: "[Service]\n"}})
	r = svc("systemd", []Rule{{Bin: "systemctl", Match: `is-active`, Stdout: "active\n"}, {Bin: "systemctl", Match: `is-enabled`, Stdout: "enabled\n"},
		{Bin: "loginctl", Match: `show-user`, Stdout: "yes\n"}}, installed, "status")
	if r.Exit != 0 || !strings.Contains(r.Stdout, "berth-serve.service (systemd, your user): active, enabled") {
		t.Errorf("status, running: exit %d %s", r.Exit, r.Stdout)
	}
	r = svc("systemd", []Rule{{Bin: "systemctl", Match: `is-active`, Stdout: "failed\n", Exit: 3}}, installed, "status")
	if r.Exit != 3 || !strings.Contains(r.Stdout, ": failed,") {
		t.Errorf("status, failed: exit %d %s", r.Exit, r.Stdout)
	}
	if r = svc("systemd", nil, nil, "status"); r.Exit != 3 || !strings.Contains(r.Stdout, "not installed") {
		t.Errorf("status, not installed: exit %d %s", r.Exit, r.Stdout)
	}
	r = svc("systemd", nil, installed, "logs", "-f")
	if !strings.Contains(calls(r), `journalctl "--user" "-u" "berth-serve.service" "-n" "100" "--no-pager" "-f"`) {
		t.Errorf("logs -f:\n%s", calls(r))
	}
	r = svc("systemd", nil, installed, "uninstall")
	if r.Exit != 0 || treeLine(r, unitPath) != "" || !strings.Contains(calls(r), `"disable" "--now" "berth-serve.service"`) || !strings.Contains(r.Stdout, "Removed the service") {
		t.Errorf("uninstall: exit %d %s\n%s", r.Exit, r.Stdout, calls(r))
	}
	if r = svc("systemd", nil, nil, "uninstall"); r.Exit != 0 || !strings.Contains(r.Stdout, "No service is installed") || strings.Contains(calls(r), "disable") {
		t.Errorf("uninstall, not installed: %d %s", r.Exit, r.Stdout)
	}

	// launchd
	const plistPath = "home/Library/LaunchAgents/dev.berth.serve.plist"
	r = svc("launchd", nil, nil, "install")
	plist := treeLine(r, plistPath)
	for _, want := range []string{`<string>dev.berth.serve</string>`, `<string><SELF></string>\n    <string>--home</string>\n    <string><RUN>/state</string>\n    <string>serve</string>`,
		`<key>DOCKER_HOST</key><string>unix:///x/docker.sock</string>`, `<key>PATH</key>`, `<key>RunAtLoad</key><true/>`, `<key>KeepAlive</key><true/>`, `Library/Logs/berth/serve.log`} {
		if !strings.Contains(plist, want) {
			t.Errorf("the agent lacks %s:\n%s", want, plist)
		}
	}
	if c := calls(r); r.Exit != 0 || !strings.Contains(c, `launchctl "bootout" "gui/`) || !strings.Contains(c, `launchctl "bootstrap" "gui/`) ||
		strings.Index(c, `"bootout"`) > strings.Index(c, `"bootstrap"`) || !strings.Contains(c, `dev.berth.serve.plist"`) {
		t.Errorf("launchd install: exit %d\n%s\n%s", r.Exit, c, r.Stderr)
	}
	if r = svc("launchd", []Rule{{Bin: "launchctl", Match: `^bootstrap`, Exit: 5, Stderr: "Bootstrap failed: 5: Input/output error\n"}}, nil, "install"); r.Exit != 1 || !strings.Contains(r.Stderr, "launchd didn't take") {
		t.Errorf("launchd refusing the agent: exit %d %s", r.Exit, r.Stderr)
	}
	agent := merge(twoOrgs, map[string]File{plistPath: {Content: "<plist/>\n"}})
	r = svc("launchd", []Rule{{Bin: "launchctl", Match: `^print`, Stdout: "gui/501/dev.berth.serve = {\n\tactive count = 1\n\tstate = running\n\tpid = 4242\n}\n"}}, agent, "status")
	if r.Exit != 0 || !strings.Contains(r.Stdout, "dev.berth.serve (launchd): running") {
		t.Errorf("launchd status: exit %d %s", r.Exit, r.Stdout)
	}
	if r = svc("launchd", []Rule{{Bin: "launchctl", Match: `^print`, Exit: 113}}, agent, "status"); r.Exit != 3 || !strings.Contains(r.Stdout, "not loaded") {
		t.Errorf("launchd status, not loaded: exit %d %s", r.Exit, r.Stdout)
	}
	r = svc("launchd", nil, agent, "uninstall")
	if r.Exit != 0 || treeLine(r, plistPath) != "" || !strings.Contains(calls(r), `launchctl "bootout"`) {
		t.Errorf("launchd uninstall: exit %d %s\n%s", r.Exit, r.Stdout, calls(r))
	}

	// Refusals: read-only, a bad verb or argument, an unknown manager.
	out := run(t, Berth(berthBin), Scenario{Args: []string{"--read-only", "service", "install"}, Files: twoOrgs, Env: []string{"BERTH_SERVICE_MANAGER=systemd"}})
	if out.Exit != 1 || !strings.Contains(out.Stderr, "read-only") || treeLine(out, unitPath) != "" || len(out.Calls) != 0 {
		t.Errorf("--read-only install: exit %d %s", out.Exit, out.Stderr)
	}
	for _, args := range [][]string{{}, {"start"}, {"install", "--listen"}, {"install", "extra"}, {"status", "x"}, {"logs", "--since", "1h"}} {
		if r := svc("systemd", nil, nil, args...); r.Exit != 1 || !strings.Contains(r.Stderr, "usage:") || treeLine(r, unitPath) != "" {
			t.Errorf("service %v: exit %d %s", args, r.Exit, r.Stderr)
		}
	}
	if r := svc("upstart", nil, nil, "status"); r.Exit != 1 || !strings.Contains(r.Stderr, "launchd or systemd") {
		t.Errorf("an unknown manager: exit %d %s", r.Exit, r.Stderr)
	}
}
