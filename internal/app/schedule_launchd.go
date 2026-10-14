package app

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// On macOS the backup schedule is a launchd agent, dev.berth.backup (#63): cron there runs only
// while the Mac is awake at the minute, and is on its way out. Linux keeps ccenv's choice, a
// systemd user timer or else cron.

// scheduleLaunchd is whether `schedule` sets a launchd agent: on macOS, or wherever
// BERTH_SERVICE_MANAGER says launchd. BERTH_SERVICE_MANAGER=systemd keeps ccenv's choice, on any
// system (the parity suite sets it, so it runs on a Mac as it does on Linux).
func (a *App) scheduleLaunchd(ctx context.Context) (bool, error) {
	switch m := a.Getenv("BERTH_SERVICE_MANAGER"); m {
	case "launchd":
		return true, nil
	case "systemd":
		return false, nil
	case "":
		os, _ := a.capture(ctx, true, "uname", "-s")
		return strings.TrimSpace(os) == "Darwin", nil
	default:
		return false, fmt.Errorf("BERTH_SERVICE_MANAGER is launchd or systemd, not %q", m)
	}
}

// scheduleAgentOn writes and loads the agent that runs cmd daily at at (HH:MM). A cron line berth
// set before (macOS used cron until #63) is taken out, so the two never both run.
func (a *App) scheduleAgentOn(ctx context.Context, at, keep, out string, cmd []string, recips string) error {
	if table, err := a.crontabList(ctx); err == nil && strings.Contains(table, "# "+scheduleName) {
		if err := a.crontabInstall(ctx, withoutJob(table)); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "Removed the schedule's cron line: the launchd agent replaces it.")
	}
	agent, log := ops.ScheduleAgent(a), ops.ScheduleLog(a)
	for _, d := range []string{path.Dir(agent), path.Dir(log)} {
		if err := a.Host.FS.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	hh, mm, _ := strings.Cut(at, ":")
	h, _ := strconv.Atoi(hh)
	m, _ := strconv.Atoi(mm)
	when := fmt.Sprintf("  <key>StartCalendarInterval</key>\n  <dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>\n", h, m) +
		"  <key>ProcessType</key><string>Background</string>\n  <key>LowPriorityIO</key><true/>\n  <key>Nice</key><integer>10</integer>\n"
	var env [][2]string
	if recips != "" {
		env = append(env, [2]string{"BERTH_BACKUP_RECIPIENTS", recips})
	}
	plist := a.launchdPlistFor(ops.ScheduleLabel, "Written by berth backup schedule. Remove it with: berth backup schedule off", cmd, env, when, log)
	if err := a.Host.FS.WriteFileAtomic(agent, []byte(plist), 0o644); err != nil {
		return err
	}
	domain, target := a.launchdTargetFor(ctx, ops.ScheduleLabel)
	_ = a.quietRun(ctx, "launchctl", "bootout", target) // an older one, so the new file is the one loaded
	if err := a.passthrough(ctx, false, "launchctl", "bootstrap", domain, agent); err != nil {
		return fmt.Errorf("launchd didn't take %s (%v)", agent, err) //nolint:errorlint // not wrapped: launchctl's exit code would silence this message
	}
	in := ""
	if out != "" {
		in = " in " + out
	}
	sayf(a.Stdout, "Scheduled (launchd agent %s): daily at %s, keeping the newest %s backups per org%s.\n", ops.ScheduleLabel, at, keep, in)
	sayf(a.Stdout, "A run missed while the Mac sleeps starts when it wakes; one missed while it's off doesn't. Log: %s\n", log)
	return nil
}

// scheduleAgentRun starts the agent's job now; scheduleAgentOff unloads and removes it.
func (a *App) scheduleAgentRun(ctx context.Context) error {
	_, target := a.launchdTargetFor(ctx, ops.ScheduleLabel)
	if err := a.passthrough(ctx, false, "launchctl", "kickstart", target); err != nil {
		return fmt.Errorf("launchd didn't start %s (%v); %s schedule loads it again", target, err, Tool) //nolint:errorlint // as above
	}
	sayf(a.Stdout, "Started. See: %s schedule status\n", Tool)
	return nil
}

func (a *App) scheduleAgentOff(ctx context.Context) error {
	_, target := a.launchdTargetFor(ctx, ops.ScheduleLabel)
	_ = a.quietRun(ctx, "launchctl", "bootout", target)
	return a.Host.FS.Remove(ops.ScheduleAgent(a))
}
