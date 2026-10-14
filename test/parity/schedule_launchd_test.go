package parity

import (
	"strings"
	"testing"
)

// TestBerthScheduleLaunchd: on macOS the backup schedule is a launchd agent, dev.berth.backup (#63):
// `schedule` writes and loads it, moving a cron line it had set before; status, run and off act on
// it. berth-only: ccenv uses cron there.
func TestBerthScheduleLaunchd(t *testing.T) {
	sched := func(rules []Rule, files map[string]File, args ...string) Result {
		t.Helper()
		return run(t, Berth(berthBin), Scenario{Args: append([]string{"schedule"}, args...), Files: files, Rules: rules,
			Env: []string{"BERTH_SERVICE_MANAGER=launchd", "DOCKER_HOST=unix:///x/docker.sock"}})
	}
	calls := func(r Result) string { return strings.Join(r.Calls, "\n") }
	const agentPath = "home/Library/LaunchAgents/dev.berth.backup.plist"
	cronJob := "0 3 * * * /usr/local/bin/berth --home /x backup --all --keep 14 >> /x/cron.log 2>&1  # berth-backup\n"

	// on: the agent runs berth through the path it was run as, daily at --at, in the background.
	r := sched([]Rule{{Bin: "crontab", Match: `^-l$`, Exit: 1, Stderr: "no crontab for parity\n"}}, withKey, "--at", "04:15", "--keep", "7")
	agent := treeLine(r, agentPath)
	for _, want := range []string{`<string>dev.berth.backup</string>`,
		`<string><SELF></string>\n    <string>--home</string>\n    <string><RUN>/state</string>\n    <string>backup</string>\n    <string>--all</string>\n    <string>--keep</string>\n    <string>7</string>`,
		`<key>StartCalendarInterval</key>\n  <dict><key>Hour</key><integer>4</integer><key>Minute</key><integer>15</integer></dict>`,
		`<key>DOCKER_HOST</key><string>unix:///x/docker.sock</string>`, `<key>ProcessType</key><string>Background</string>`, `Library/Logs/berth/backup.log`} {
		if !strings.Contains(agent, want) {
			t.Errorf("the agent lacks %s:\n%s", want, agent)
		}
	}
	if strings.Contains(agent, "KeepAlive") || strings.Contains(agent, "RunAtLoad") {
		t.Errorf("the agent runs at load:\n%s", agent)
	}
	if c := calls(r); r.Exit != 0 || !strings.Contains(c, `launchctl "bootout" "gui/`) || !strings.Contains(c, `launchctl "bootstrap" "gui/`) ||
		strings.Index(c, `"bootout"`) > strings.Index(c, `"bootstrap"`) || strings.Contains(c, `crontab "-"`) {
		t.Errorf("on: exit %d\n%s\n%s", r.Exit, c, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "Scheduled (launchd agent dev.berth.backup): daily at 04:15, keeping the newest 7 backups per org.") {
		t.Errorf("on: %s", r.Stdout)
	}

	// A cron line berth had set (macOS used cron before) is taken out; the user's own lines stay.
	r = sched([]Rule{{Bin: "crontab", Match: `^-l$`, Stdout: "5 * * * * mine\n" + cronJob}}, withKey)
	if c := calls(r); r.Exit != 0 || !strings.Contains(c, `crontab "-"`) || !strings.Contains(r.Stdout, "Removed the schedule's cron line") {
		t.Errorf("on, over a cron line: exit %d %s\n%s", r.Exit, r.Stdout, c)
	}
	// Without a key there's nothing to run unattended, as with systemd and cron.
	if r = sched(nil, twoOrgs); r.Exit == 0 || !strings.Contains(r.Stderr, "keygen") || treeLine(r, agentPath) != "" {
		t.Errorf("on without a key: exit %d %s", r.Exit, r.Stderr)
	}

	// status: when, the agent's state at launchd, and the runs from its log.
	loaded := merge(withKey, map[string]File{
		agentPath:                            {Content: "<dict><key>Hour</key><integer>3</integer><key>Minute</key><integer>5</integer></dict>\n"},
		"home/Library/Logs/berth/backup.log": {Content: "== backing up acme\nnoise\nWrote /x/acme-1.tar.zst.age\n"},
	})
	print := Rule{Bin: "launchctl", Match: `^print gui/\d+/dev\.berth\.backup$`, Stdout: "gui/501/dev.berth.backup = {\n\tstate = not running\n\tlast exit code = 0\n}\n"}
	r = sched([]Rule{print}, loaded, "status")
	want := "daily at 03:05 (launchd agent dev.berth.backup)\nloaded, not running, last exit code 0\n\n== backing up acme\nWrote /x/acme-1.tar.zst.age\nlog: <RUN>/home/Library/Logs/berth/backup.log\n"
	if r.Exit != 0 || r.Stdout != want {
		t.Errorf("status: exit %d\n%q\nwant %q", r.Exit, r.Stdout, want)
	}
	r = run(t, Berth(berthBin), Scenario{Args: []string{"--output", "json", "schedule", "status"}, Files: loaded, Rules: []Rule{print}})
	if r.Exit != 0 || !strings.Contains(r.Stdout, `"kind": "launchd"`) {
		t.Errorf("status --output json: exit %d %s", r.Exit, r.Stdout)
	}
	if r = sched([]Rule{{Bin: "launchctl", Match: `^print`, Exit: 113}}, loaded, "status"); !strings.Contains(r.Stdout, "not loaded") {
		t.Errorf("status, not loaded: %s", r.Stdout)
	}

	// run starts it now; off unloads and removes it.
	if r = sched(nil, loaded, "run"); r.Exit != 0 || !strings.Contains(calls(r), `launchctl "kickstart" "gui/`) || !strings.Contains(r.Stdout, "Started.") {
		t.Errorf("run: exit %d %s\n%s", r.Exit, r.Stdout, calls(r))
	}
	r = sched(nil, loaded, "off")
	if r.Exit != 0 || treeLine(r, agentPath) != "" || !strings.Contains(calls(r), `launchctl "bootout" "gui/`) || !strings.Contains(r.Stdout, "Backup schedule removed.") {
		t.Errorf("off: exit %d %s\n%s", r.Exit, r.Stdout, calls(r))
	}
}
