package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
)

// scheduleName is berth's unit and crontab marker: berth-backup, never ccenv's ccenv-backup, so the
// two schedules can't overwrite each other. At cutover ccenv-backup is turned off in the same step
// (plan, decision 8).
const scheduleName = "berth-backup"

var (
	atHHMM = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	// journalLines is what `schedule status` shows of the service's journal: ccenv's pattern, plus
	// berth's own error prefix.
	journalLines = regexp.MustCompile(`==|Wrote|Pruned|failed|ccenv:|berth:`)
)

// Schedule is `ccenv schedule [--at HH:MM] [--keep N] [-o dir] | status | run | off`: a nightly
// `backup --all` through a systemd user timer, or cron where there is no systemd user manager.
func (a *App) Schedule(ctx context.Context, args []string) error {
	at, keep, out, action := "03:00", "14", "", "on"
	for i := 0; i < len(args); i++ {
		switch x := args[i]; x {
		case "--at", "--keep", "-o", "--output":
			// at="$2"; shift 2: under set -u a missing value ends the command (PARITY.md).
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", x)
			}
			i++
			switch x {
			case "--at":
				at = args[i]
			case "--keep":
				keep = args[i]
			default:
				out = args[i]
			}
		case "--off", "off":
			action = "off"
		case "status":
			action = "status"
		case "run", "now":
			action = "run"
		default:
			return fmt.Errorf("usage: %[1]s schedule [--at HH:MM] [--keep N] [-o dir] | status | run | off", Tool)
		}
	}
	if !atHHMM.MatchString(at) {
		return errors.New("--at must be HH:MM (24h)")
	}
	if !keepN.MatchString(keep) {
		return errors.New("--keep needs a number >= 1")
	}
	if action != "status" {
		if err := a.State.Writable("change the backup schedule"); err != nil {
			return err
		}
	}
	unit := a.Getenv("HOME") + "/.config/systemd/user"
	timer, service := unit+"/"+scheduleName+".timer", unit+"/"+scheduleName+".service"
	systemd := a.quietRun(ctx, "systemctl", "--user", "show-environment") == nil
	hasTimer := systemd && a.isFile(timer)

	switch action {
	case "status":
		return a.scheduleStatus(ctx, hasTimer)
	case "run":
		if !hasTimer {
			return fmt.Errorf("no systemd schedule to run (%s backup --all runs it by hand)", Tool)
		}
		if err := a.passthrough(ctx, false, "systemctl", "--user", "start", scheduleName+".service"); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Ran. See: %s schedule status\n", Tool)
		return nil
	case "off":
		if hasTimer {
			_ = a.quietRun(ctx, "systemctl", "--user", "disable", "--now", scheduleName+".timer")
			_ = a.Host.FS.Remove(timer)
			_ = a.Host.FS.Remove(service)
			if err := a.passthrough(ctx, false, "systemctl", "--user", "daemon-reload"); err != nil {
				return err
			}
		}
		// crontab -l 2>/dev/null | grep -v "# <name>" | crontab - || true: with no crontab at all,
		// this installs an empty one.
		if table, err := a.crontabList(ctx); err == nil || !host.IsNotFound(err) {
			_ = a.crontabInstall(ctx, withoutJob(table))
		}
		fmt.Fprintln(a.Stdout, "Backup schedule removed.")
		return nil
	}

	// Unattended runs can't answer a passphrase prompt, and a passphrase must not sit in a unit file.
	recips := a.backupEnv("BACKUP_RECIPIENTS")
	if !a.isFile(a.keyFile()) && recips == "" {
		return fmt.Errorf("scheduled backups need a key: run '%s keygen' first", Tool)
	}
	// The job names the state root explicitly, so it backs up the orgs this invocation sees.
	self := a.Invoked
	if self == "" {
		self = a.Self
	}
	cmd := []string{self, "--home", a.State.Home.Path, "backup", "--all", "--keep", keep}
	if out != "" {
		cmd = append(cmd, "-o", out)
	}
	if recips != "" {
		cmd = append([]string{"env", "BERTH_BACKUP_RECIPIENTS=" + recips}, cmd...)
	}
	line := strings.Join(cmd, " ") // ${cmd[*]}: joined by spaces, unquoted

	if systemd {
		if err := a.Host.FS.MkdirAll(unit, 0o777&^a.umask(ctx)); err != nil {
			return err
		}
		svc := "[Unit]\nDescription=berth: encrypted backup of all Claude org containers\nAfter=docker.service\n\n" +
			"[Service]\nType=oneshot\nExecStart=" + line + "\nNice=10\nIOSchedulingClass=idle\n"
		tmr := "[Unit]\nDescription=berth: nightly backup at " + at + "\n\n" +
			"[Timer]\nOnCalendar=*-*-* " + at + ":00\nPersistent=true\nRandomizedDelaySec=10m\n\n" +
			"[Install]\nWantedBy=timers.target\n"
		for _, u := range [][2]string{{service, svc}, {timer, tmr}} {
			if err := a.Host.FS.WriteFile(u[0], []byte(u[1]), 0o666&^a.umask(ctx)); err != nil {
				return err
			}
		}
		if err := a.passthrough(ctx, false, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := a.passthrough(ctx, true, "systemctl", "--user", "enable", "--now", scheduleName+".timer"); err != nil {
			return err
		}
		in := ""
		if out != "" {
			in = " in " + out
		}
		fmt.Fprintf(a.Stdout, "Scheduled (systemd user timer): daily at %s, keeping the newest %s backups per org%s.\n", at, keep, in)
		fmt.Fprintf(a.Stdout, "Missed runs (machine off) catch up at next boot. Logs: %s schedule status\n", Tool)
		user := a.Getenv("USER")
		if linger, _ := a.capture(ctx, true, "loginctl", "show-user", user, "-p", "Linger", "--value"); linger != "yes" {
			fmt.Fprintf(a.Stdout, "Note: runs only while you're logged in. To run even when logged out: sudo loginctl enable-linger %s\n", user)
		}
		return nil
	}

	table, err := a.crontabList(ctx)
	if host.IsNotFound(err) {
		return errors.New("neither systemd user services nor crontab are available on this host")
	}
	h, m, _ := strings.Cut(at, ":")
	logFile := a.backupsDir() + "/cron.log"
	job := strings.TrimPrefix(m, "0") + " " + strings.TrimPrefix(h, "0") + " * * * " + line + " >> " + logFile + " 2>&1  # " + scheduleName + "\n"
	// ccenv's ( crontab -l | grep -v …; echo "<job>" ) | crontab - never adds the job when there is
	// no crontab yet, or only its own line (grep fails under set -e): berth always does (PARITY.md).
	if err := a.crontabInstall(ctx, withoutJob(table)+job); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Scheduled (cron): daily at %s, keeping the newest %s backups per org. Log: %s\n", at, keep, logFile)
	return nil
}

// scheduleStatus is `schedule status`: the timer and the last runs from the journal, or the crontab
// line, or that there is no schedule.
func (a *App) scheduleStatus(ctx context.Context, hasTimer bool) error {
	if hasTimer {
		// systemctl … list-timers | head -2: under pipefail its failure ends the command.
		timers, err := a.captureRaw(ctx, false, "systemctl", "--user", "list-timers", scheduleName+".timer", "--no-pager")
		_, _ = a.Stdout.Write(headLines([]byte(timers), 2))
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout)
		journal, jerr := a.captureRaw(ctx, true, "journalctl", "--user", "-u", scheduleName+".service", "-n", "15", "--no-pager", "-o", "cat")
		shown := 0
		for _, l := range fileLines([]byte(journal)) {
			if journalLines.MatchString(l) {
				fmt.Fprintln(a.Stdout, l)
				shown++
			}
		}
		if jerr != nil || shown == 0 {
			fmt.Fprintln(a.Stdout, "(no runs yet)")
		}
		return nil
	}
	// crontab -l 2>/dev/null | grep -q "# <name>", then crontab -l | grep "# <name>" (errors shown;
	// under pipefail a failure ends the command).
	if table, err := a.crontabList(ctx); err == nil && strings.Contains(table, "# "+scheduleName) {
		table, err := a.captureRaw(ctx, false, "crontab", "-l")
		if err != nil {
			return err
		}
		for _, l := range fileLines([]byte(table)) {
			if strings.Contains(l, "# "+scheduleName) {
				fmt.Fprintln(a.Stdout, l)
			}
		}
		fmt.Fprintf(a.Stdout, "log: %s/cron.log\n", a.backupsDir())
		return nil
	}
	fmt.Fprintf(a.Stdout, "No backup schedule. Set one with: %s schedule\n", Tool)
	return nil
}

// crontabList is `crontab -l 2>/dev/null`.
func (a *App) crontabList(ctx context.Context) (string, error) {
	return a.captureRaw(ctx, true, "crontab", "-l")
}

// crontabInstall is `… | crontab -`.
func (a *App) crontabInstall(ctx context.Context, table string) error {
	return a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"crontab", "-"}, Stdin: strings.NewReader(table), Stdout: a.Stdout, Stderr: a.Stderr})
}

// withoutJob is `grep -v "# berth-backup"`: the table without berth's line.
func withoutJob(table string) string {
	var b bytes.Buffer
	for _, l := range fileLines([]byte(table)) {
		if !strings.Contains(l, "# "+scheduleName) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}
