package parity

import (
	"slices"
	"strings"
	"testing"
)

// Phase 4b: schedule (on, status, run, off). Fixture unit files are named ccenv-backup; berth's
// side gets them as berth-backup (Berth's Prepare), and <SCHEDULE> in fake output is each tool's name.

const units = "home/.config/systemd/user/"

var withTimer = merge(withKey, map[string]File{
	units + "ccenv-backup.timer":   {Content: "[Timer]\nOnCalendar=*-*-* 03:00:00\n", Mode: 0o644},
	units + "ccenv-backup.service": {Content: "[Service]\nExecStart=old\n", Mode: 0o644},
})

func init() {
	cronTable := "MAILTO=\"\"\n5 1 * * * /old/job >> /x 2>&1  # <SCHEDULE>\n0 * * * * /usr/bin/true"
	more := []checked{
		{Scenario{Name: "schedule -o, recipients from the environment", Args: []string{"schedule", "-o", "bk", "--at", "23:59"},
			Env: []string{"CCENV_BACKUP_RECIPIENTS=age1aaa,age1bbb"}, Files: twoOrgs}, 0, []string{"keeping the newest 14 backups per org in bk.", "enable-linger parity"}},
		{Scenario{Name: "schedule, lingering", Args: []string{"schedule", "--keep", "3"}, Files: withTimer,
			Rules: []Rule{{Bin: "loginctl", Match: `^show-user parity -p Linger --value$`, Stdout: "yes\n"}}}, 0, []string{"daily at 03:00, keeping the newest 3"}},
		{Scenario{Name: "schedule, enable fails", Args: []string{"schedule"}, Files: withKey,
			Rules: []Rule{{Bin: "systemctl", Match: `^--user enable --now `, Stdout: "hidden\n", Stderr: "Failed to enable unit\n", Exit: 1}}}, 1, []string{"Failed to enable unit"}},
		{Scenario{Name: "schedule via cron, replaces its line", Args: []string{"schedule", "--at", "00:05", "--output", "bk"}, Files: withKey,
			Rules: []Rule{noSystemd, {Bin: "crontab", Match: `^-l$`, Stdout: cronTable}}}, 0, []string{"Scheduled (cron): daily at 00:05", "Log: <RUN>/state/backups/cron.log"}},
		{Scenario{Name: "schedule, bad --at", Args: []string{"schedule", "--at", "24:00"}, Files: withKey}, 1, []string{"<tool>: --at must be HH:MM (24h)"}},
		{Scenario{Name: "schedule, bad --keep", Args: []string{"schedule", "--keep", "0"}, Files: withKey}, 1, []string{"<tool>: --keep needs a number >= 1"}},
		{Scenario{Name: "schedule, bad --at before status", Args: []string{"schedule", "status", "--at", "3"}, Files: withKey}, 1, []string{"--at must be"}},
		{Scenario{Name: "schedule, unknown argument", Args: []string{"schedule", "daily"}, Files: withKey},
			1, []string{"<tool>: usage: <tool> schedule [--at HH:MM] [--keep N] [-o dir] | status | run | off"}},

		// status
		{Scenario{Name: "schedule status, systemd", Args: []string{"schedule", "status"}, Files: withTimer, Rules: []Rule{
			{Bin: "systemctl", Match: `^--user list-timers <SCHEDULE>|list-timers`, Stdout: "NEXT LEFT\nThu 03:00 5h\n\n1 timers listed.\n"},
			{Bin: "journalctl", Match: `-n 15 --no-pager -o cat$`, Stdout: "== acme\nkept: 3 files\nWrote /b/acme.tar.zst.age (1KB, key-encrypted)\nPruned x\nccenv: backup of globex failed\n"}}},
			0, []string{"Thu 03:00 5h", "Wrote /b/acme", "<tool>: backup of globex failed"}},
		{Scenario{Name: "schedule status, no runs yet", Args: []string{"schedule", "status"}, Files: withTimer,
			Rules: []Rule{{Bin: "journalctl", Match: ``, Stderr: "No journal files\n", Exit: 1}}}, 0, []string{"(no runs yet)"}},
		{Scenario{Name: "schedule status, list-timers fails", Args: []string{"schedule", "status"}, Files: withTimer,
			Rules: []Rule{{Bin: "systemctl", Match: `list-timers`, Stdout: "a\nb\nc\n", Stderr: "bus error\n", Exit: 4}}}, 4, []string{"bus error"}},
		{Scenario{Name: "schedule status, cron", Args: []string{"schedule", "status"}, Files: withKey,
			Rules: []Rule{noSystemd, {Bin: "crontab", Match: `^-l$`, Stdout: cronTable}}}, 0, []string{"/old/job", "log: <RUN>/state/backups/cron.log"}},
		{Scenario{Name: "schedule status, none", Args: []string{"schedule", "status"}, Files: withKey,
			Rules: []Rule{noSystemd, {Bin: "crontab", Match: `^-l$`, Stderr: "no crontab for parity\n", Exit: 1}}},
			0, []string{"No backup schedule. Set one with: <tool> schedule"}},
		{Scenario{Name: "schedule status, systemd without a timer", Args: []string{"schedule", "status"}, Files: withKey,
			Rules: []Rule{{Bin: "crontab", Match: `^-l$`, Stdout: "0 * * * * /usr/bin/true\n"}}}, 0, []string{"No backup schedule"}},

		// run
		{Scenario{Name: "schedule run", Args: []string{"schedule", "run"}, Files: withTimer}, 0, []string{"Ran. See: <tool> schedule status"}},
		{Scenario{Name: "schedule now, start fails", Args: []string{"schedule", "now"}, Files: withTimer,
			Rules: []Rule{{Bin: "systemctl", Match: `^--user start `, Stderr: "Job failed\n", Exit: 3}}}, 3, []string{"Job failed"}},
		{Scenario{Name: "schedule run, no timer", Args: []string{"schedule", "run"}, Files: withKey},
			1, []string{"<tool>: no systemd schedule to run (<tool> backup --all runs it by hand)"}},
		{Scenario{Name: "schedule run, no systemd", Args: []string{"schedule", "run"}, Files: withTimer, Rules: []Rule{noSystemd}}, 1, []string{"no systemd schedule"}},

		// off
		{Scenario{Name: "schedule off, systemd and cron", Args: []string{"schedule", "off"}, Files: withTimer,
			Rules: []Rule{{Bin: "crontab", Match: `^-l$`, Stdout: cronTable}, {Bin: "systemctl", Match: `disable`, Stderr: "hidden\n", Exit: 1}}},
			0, []string{"Backup schedule removed."}},
		// With no crontab at all, off installs an empty one (crontab -l fails, grep -v passes nothing on).
		{Scenario{Name: "schedule --off, nothing scheduled", Args: []string{"schedule", "--off"}, Files: withKey,
			Rules: []Rule{noSystemd, {Bin: "crontab", Match: `^-l$`, Exit: 1}}}, 0, []string{"Backup schedule removed."}},
		{Scenario{Name: "schedule off, daemon-reload fails", Args: []string{"schedule", "off"}, Files: withTimer,
			Rules: []Rule{{Bin: "systemctl", Match: `daemon-reload`, Stderr: "no bus\n", Exit: 5}}}, 5, []string{"no bus"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	for _, name := range []string{"schedule via systemd", "schedule via cron", "schedule without a key"} {
		ported[name] = true
	}
}

// TestBerthScheduleNames: berth's schedule is its own, berth-backup, and its job names the state
// root, so it backs up the orgs of the berth that set it up (plan, decision 8). The harness maps
// these onto ccenv's names; this checks the real ones.
func TestBerthScheduleNames(t *testing.T) {
	r := run(t, BerthUnowned(berthBin), Scenario{Args: []string{"schedule", "--at", "02:30"}, Files: withKey})
	want := units + `berth-backup.service 0644 "[Unit]\nDescription=berth: encrypted backup of all Claude org containers\nAfter=docker.service\n\n` +
		`[Service]\nType=oneshot\nExecStart=<SELF> --home <RUN>/state backup --all --keep 14\nNice=10\nIOSchedulingClass=idle\n"`
	if r.Exit != 0 || !slices.Contains(r.Tree, want) || !slices.Contains(r.Calls, `systemctl "--user" "enable" "--now" "berth-backup.timer"`) {
		t.Errorf("exit %d, stderr %q\ntree:\n%s\ncalls:\n%s", r.Exit, r.Stderr, strings.Join(r.Tree, "\n"), strings.Join(r.Calls, "\n"))
	}
}

// TestBerthScheduleCronFirstJob: with no crontab yet (or only its own line), ccenv installs an empty
// table and exits 1 (PARITY.md, legacy quirk 3); berth installs its job.
func TestBerthScheduleCronFirstJob(t *testing.T) {
	for name, rule := range map[string]Rule{
		"no crontab":    {Bin: "crontab", Match: `^-l$`, Stderr: "no crontab for parity\n", Exit: 1},
		"only its line": {Bin: "crontab", Match: `^-l$`, Stdout: "0 3 * * * old  # berth-backup\n"},
	} {
		t.Run(name, func(t *testing.T) {
			r := run(t, BerthUnowned(berthBin), Scenario{Args: []string{"schedule"}, Files: withKey, Rules: []Rule{noSystemd, rule}})
			job := `crontab "-"  <stdin "0 3 * * * <SELF> --home <RUN>/state backup --all --keep 14 >> <RUN>/state/backups/cron.log 2>&1  # berth-backup\n">`
			if r.Exit != 0 || !slices.Contains(r.Calls, job) || !strings.Contains(r.Stdout, "Scheduled (cron): daily at 03:00") {
				t.Errorf("exit %d, stdout %q, stderr %q, calls:\n%s", r.Exit, r.Stdout, r.Stderr, strings.Join(r.Calls, "\n"))
			}
		})
	}
}
