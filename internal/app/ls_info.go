package app

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/ar4mirez/berth/internal/ops"
)

// Ls is `ccenv ls`: every org dir with an org.env, whatever its MANAGER.
func (a *App) Ls(ctx context.Context) error {
	rows := a.orgRows(ctx)
	if a.Output == OutputJSON {
		out := ops.Orgs{Schema: "berth.orgs/v1", Orgs: []ops.OrgStatus{}}
		for _, r := range rows {
			out.Orgs = append(out.Orgs, r.OrgStatus)
		}
		return a.writeJSON(out)
	}
	fmt.Fprintf(a.Stdout, "%-14s %-6s %-6s %-6s %-8s %s\n", "ORG", "STATE", "SSH", "TTYD", "TOKEN", "REMOTE")
	for _, r := range rows {
		token := "MISSING"
		if r.Token {
			token = "set"
		}
		fmt.Fprintf(a.Stdout, "%-14s %-6s %-6s %-6s %-8s %s\n", r.Name, r.State, r.ssh, r.ttyd, token, r.Remote)
	}
	return nil
}

// orgRow is an org's status, plus the raw port values the text table prints as they are.
type orgRow struct {
	ops.OrgStatus
	ssh, ttyd string
}

// orgRows is ls's data: every dir with an org.env, in byte order, with the same checks (and the
// same docker calls, in the same order) as ccenv's cmd_ls.
func (a *App) orgRows(ctx context.Context) []orgRow {
	var rows []orgRow
	for _, o := range a.orgDirs() {
		if !a.isFile(a.Orgs.EnvPath(o)) {
			continue
		}
		r := orgRow{OrgStatus: ops.OrgStatus{Name: o, Manager: "ccenv", State: "down", Remote: "-"}}
		if m := a.env(o, "MANAGER"); m != "" {
			r.Manager = m
		}
		if a.running(ctx, o) {
			r.State = "up"
			switch {
			case a.env(o, "REMOTE_CONTROL") == "0":
				r.Remote = "off"
			case !a.rcLoggedIn(ctx, o):
				r.Remote = "login-needed"
			case a.rcRunning(ctx, o):
				r.Remote = "on"
			case a.rcBlocked(ctx, o):
				r.Remote = "blocked-by-org"
			default:
				r.Remote = "restarting"
			}
		}
		r.Token = a.secret(o, "CLAUDE_CODE_OAUTH_TOKEN")+a.secret(o, "ANTHROPIC_API_KEY") != ""
		r.ssh, r.ttyd = a.env(o, "SSH_PORT"), a.env(o, "TTYD_PORT")
		r.SSHPort, r.TTYDPort = portOf(r.ssh), portOf(r.ttyd)
		rows = append(rows, r)
	}
	return rows
}

// portOf is a port value as a number, or nil if it isn't one.
func portOf(v string) *int {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > 65535 {
		return nil
	}
	return &n
}

// Info is `ccenv info <org>`: every way to connect.
func (a *App) Info(ctx context.Context, o string) error {
	if err := a.needOrg(o); err != nil {
		return err
	}
	h := a.hostAddr(ctx, o)
	// ccenv: `sp=$(envval "$org" SSH_PORT)` at function level under set -e -o pipefail, so a missing
	// key ends the command with exit 1 and no message (PARITY.md, legacy quirks).
	sp, ok, _ := a.Orgs.Get(o, "SSH_PORT")
	if !ok {
		return &Exit{Code: 1}
	}
	tp, ok, _ := a.Orgs.Get(o, "TTYD_PORT")
	if !ok {
		return &Exit{Code: 1}
	}
	url := ""
	if a.running(ctx, o) {
		url = a.rcURL(ctx, o)
	}
	// The heredoc is expanded in full (a second docker ps, then cat) before anything is printed.
	state := "stopped"
	if a.running(ctx, o) {
		state = "running"
	}
	pub := a.catFile(path.Join(a.Orgs.Dir, o, "ssh", "id_ed25519.pub"))
	if url == "" {
		url = "not set up yet; run: " + Tool + " login " + o
	}
	fmt.Fprintf(a.Stdout, `== %[1]s ==  (container claude-%[1]s, %[2]s)

claude.ai / Claude app  %[3]s
Terminal on this host   %[4]s attach %[1]s
SSH (any device)        ssh -t -p %[5]s node@%[6]s tmux new -A -s main
Browser terminal        http://%[6]s:%[7]s   (password: %[4]s password %[1]s)
VS Code / Cursor        Remote-SSH to host "claude-%[1]s" (ssh config below)
Headless                %[4]s run %[1]s "your prompt"

~/.ssh/config:
Host claude-%[1]s
  HostName %[6]s
  Port %[5]s
  User node

Git public key:  %[8]s
`, o, state, url, Tool, sp, h, tp, pub)
	return nil
}
