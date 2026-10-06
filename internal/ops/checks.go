package ops

import (
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/hosts"
)

// GetFirewallPresets is `fw <org> presets`: the image's list, from the org's container when it
// runs, else from a throwaway one. A failing docker comes back with what it printed.
func GetFirewallPresets(ctx context.Context, s System, org string) (Presets, error) {
	out := Presets{Schema: "berth.firewall-presets/v1", Presets: []Preset{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	var err error
	if Running(ctx, s, org) {
		out.Raw, err = s.CaptureRaw(ctx, false, "docker", "exec", contract.Container(org), contract.FirewallScript, "presets")
	} else {
		out.Raw, err = s.CaptureRaw(ctx, false, "docker", "run", "--rm", "--entrypoint", contract.FirewallScript, "claude-env", "presets")
	}
	for _, l := range lines([]byte(out.Raw)) {
		if f := strings.Fields(l); len(f) > 0 && strings.HasPrefix(f[0], "@") {
			out.Presets = append(out.Presets, Preset{Name: f[0], Hosts: append([]string{}, f[1:]...)})
		}
	}
	return out, err
}

// TestFirewall is `fw <org> test [host...]`: whether the running org can reach each URL now (a
// bare host is tried over https). With none given, it tries Anthropic, GitHub and example.com.
func TestFirewall(ctx context.Context, s System, org string, targets []string) (FirewallTest, error) {
	out := FirewallTest{Schema: "berth.firewall-test/v1", Org: org, Results: []FirewallProbe{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	if err := NeedUp(ctx, s, org); err != nil {
		return out, err
	}
	if len(targets) == 0 {
		targets = []string{"api.anthropic.com", "github.com", "example.com"}
	}
	for _, e := range targets {
		if !strings.Contains(e, "://") {
			e = "https://" + e
		}
		ok := s.Succeeds(ctx, false, "docker", "exec", "-u", "node", contract.Container(org), "curl", "-s", "-o", "/dev/null", "--max-time", "6", e)
		out.Results = append(out.Results, FirewallProbe{URL: e, Allowed: ok})
	}
	return out, nil
}

// ScheduleName is berth's unit and crontab marker: berth-backup, never ccenv's ccenv-backup, so the
// two schedules can't overwrite each other. At cutover ccenv-backup is turned off in the same step
// (plan, decision 8).
const ScheduleName = "berth-backup"

// journalLines is what `schedule status` shows of the service's journal: ccenv's pattern, plus
// berth's own error prefix.
var journalLines = regexp.MustCompile(`==|Wrote|Pruned|failed|ccenv:|berth:`)

// ScheduleTimer is the systemd user timer's unit file.
func ScheduleTimer(s System) string {
	return s.OperatorEnv("HOME") + "/.config/systemd/user/" + ScheduleName + ".timer"
}

// HasScheduleTimer is whether the schedule is a systemd user timer: systemd answers for this user,
// and the timer's unit is there.
func HasScheduleTimer(ctx context.Context, s System) bool {
	return s.Silent(ctx, "systemctl", "--user", "show-environment") && s.IsFile(ScheduleTimer(s))
}

// GetSchedule is `schedule status`: the timer and the last runs from the journal, or the crontab
// line. A failing `systemctl list-timers` comes back with what it printed.
func GetSchedule(ctx context.Context, s System) (Schedule, error) {
	out := Schedule{Schema: "berth.schedule/v1", Kind: "none", Timer: []string{}, Runs: []string{}, Jobs: []string{}}
	if HasScheduleTimer(ctx, s) {
		out.Kind = "systemd"
		// systemctl … list-timers | head -2: under pipefail its failure ends the command.
		timers, err := s.CaptureRaw(ctx, false, "systemctl", "--user", "list-timers", ScheduleName+".timer", "--no-pager")
		out.TimerText = string(headLines([]byte(timers), 2))
		out.Timer = append(out.Timer, lines([]byte(out.TimerText))...)
		if err != nil {
			return out, err
		}
		journal, jerr := s.CaptureRaw(ctx, true, "journalctl", "--user", "-u", ScheduleName+".service", "-n", "15", "--no-pager", "-o", "cat")
		for _, l := range lines([]byte(journal)) {
			if journalLines.MatchString(l) {
				out.Runs = append(out.Runs, l)
			}
		}
		out.NoRuns = jerr != nil || len(out.Runs) == 0
		return out, nil
	}
	// crontab -l 2>/dev/null | grep -q "# <name>", then crontab -l | grep "# <name>" (errors shown;
	// under pipefail a failure ends the command).
	if table, err := s.CaptureRaw(ctx, true, "crontab", "-l"); err == nil && strings.Contains(table, "# "+ScheduleName) {
		out.Kind = "cron"
		table, err := s.CaptureRaw(ctx, false, "crontab", "-l")
		if err != nil {
			return out, err
		}
		for _, l := range lines([]byte(table)) {
			if strings.Contains(l, "# "+ScheduleName) {
				out.Jobs = append(out.Jobs, l)
			}
		}
		out.Log = s.BackupsDir() + "/cron.log"
	}
	return out, nil
}

// headLines is `head -n`: the first n lines of b, as they are.
func headLines(b []byte, n int) []byte {
	end := 0
	for i := 0; i < n && end < len(b); i++ {
		j := bytes.IndexByte(b[end:], '\n')
		if j < 0 {
			return b
		}
		end += j + 1
	}
	return b[:end]
}

// GetHostGuard is `host guard <name> status`, on that host's System: whether the guard is
// installed, its container's state, and its own report of its chains. A failing report comes back
// with what it printed.
func GetHostGuard(ctx context.Context, s System, name string) (HostGuard, error) {
	out := HostGuard{Schema: "berth.host-guard/v1", Host: name}
	if !s.Silent(ctx, "docker", "inspect", hosts.GuardContainer) {
		return out, nil
	}
	out.Installed = true
	out.State, _ = s.Capture(ctx, true, "docker", "inspect", "-f", "{{.State.Status}}", hosts.GuardContainer)
	var err error
	out.Rules, err = s.CaptureRaw(ctx, false, "docker", "exec", hosts.GuardContainer, "bash", "-c", hosts.GuardScript, "guard", "status")
	return out, err
}
