package ops

import (
	"context"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// ListOrgs is `ls`: every org dir with an org.env, whatever its MANAGER, then the orgs of each
// registered host (#45). A host that can't be reached is reported, never hidden.
func ListOrgs(ctx context.Context, s System) Orgs {
	out := Orgs{Schema: "berth.orgs/v1", Orgs: orgStatuses(ctx, s), Unreachable: []HostError{}, Destroyed: s.Destroyed()}
	for _, p := range s.Peers(ctx) {
		out.MultiHost = true
		ps, done, err := p.Open(ctx)
		if err != nil {
			out.Unreachable = append(out.Unreachable, HostError{Host: p.Name, Error: err.Error()})
			continue
		}
		out.Orgs = append(out.Orgs, orgStatuses(ctx, ps)...)
		out.Destroyed = append(out.Destroyed, ps.Destroyed()...)
		done()
	}
	if out.Orgs == nil {
		out.Orgs = []OrgStatus{}
	}
	if out.Destroyed == nil {
		out.Destroyed = []DestroyedOrg{}
	}
	return out
}

// orgStatuses is ls's data for one System: every dir with an org.env, in byte order, with the
// same checks (and the same docker calls, in the same order) as ccenv's cmd_ls.
func orgStatuses(ctx context.Context, s System) []OrgStatus {
	var rows []OrgStatus
	for _, o := range s.OrgDirs() {
		if !s.IsFile(EnvPath(s, o)) {
			continue
		}
		r := OrgStatus{Name: o, Manager: "ccenv", State: "down", Remote: "-", Host: s.HostLabel()}
		if m := env(s, o, "MANAGER"); m != "" {
			r.Manager = m
		}
		if Running(ctx, s, o) {
			r.State = "up"
			r.Remote = RemoteState(ctx, s, o)
		}
		r.Token = s.Secret(o, "CLAUDE_CODE_OAUTH_TOKEN")+s.Secret(o, "ANTHROPIC_API_KEY") != ""
		r.SSHRaw, r.TTYDRaw = env(s, o, "SSH_PORT"), env(s, o, "TTYD_PORT")
		r.SSHPort, r.TTYDPort = PortOf(r.SSHRaw), PortOf(r.TTYDRaw)
		rows = append(rows, r)
	}
	return rows
}

// RemoteState is Remote Control's state in a running org, as `ls` shows it: "off", "login-needed",
// "on", "blocked-by-org" or "restarting". Each check is a docker call, made only if the one before
// it didn't decide.
func RemoteState(ctx context.Context, s System, org string) string {
	switch {
	case env(s, org, "REMOTE_CONTROL") == "0":
		return "off"
	case !RemoteLoggedIn(ctx, s, org):
		return "login-needed"
	case RemoteRunning(ctx, s, org):
		return "on"
	case RemoteBlocked(ctx, s, org):
		return "blocked-by-org"
	}
	return "restarting"
}

// PortOf is a port value as a number, or nil if it isn't one.
func PortOf(v string) *int {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > 65535 {
		return nil
	}
	return &n
}

// RemoteLoggedIn is rc_logged_in: `docker exec claude-$1 test -f …/.credentials.json`.
func RemoteLoggedIn(ctx context.Context, s System, org string) bool {
	return s.Succeeds(ctx, false, "docker", "exec", contract.Container(org), "test", "-f", contract.Credentials)
}

// RemoteRunning is `docker exec claude-$1 pgrep -f "claude remote-control" >/dev/null`.
func RemoteRunning(ctx context.Context, s System, org string) bool {
	return s.Succeeds(ctx, true, "docker", "exec", contract.Container(org), "pgrep", "-f", "claude remote-control")
}

// RemoteBlocked is `docker exec … sh -c 'tail -n 2 …remote-control.log 2>/dev/null' | grep -q
// "blocked by organization policy"`: true only if docker succeeds (pipefail) and a line matches.
func RemoteBlocked(ctx context.Context, s System, org string) bool {
	out, err := s.Capture(ctx, false, "docker", "exec", contract.Container(org), "sh", "-c",
		"tail -n 2 "+contract.RemoteControlLog+" 2>/dev/null")
	return err == nil && strings.Contains(out, contract.RemoteControlBlocked)
}

// RemoteURL is `docker exec … sh -c 'grep -ao "https://claude.ai/code?environment=[A-Za-z0-9_]*" … |
// tail -1' || true`: whatever it printed, even if it failed.
func RemoteURL(ctx context.Context, s System, org string) string {
	out, _ := s.Capture(ctx, false, "docker", "exec", contract.Container(org), "sh", "-c",
		`grep -ao "https://claude.ai/code?environment=[A-Za-z0-9_]*" `+contract.RemoteControlLog+` 2>/dev/null | tail -1`)
	return out
}
