package app

import (
	"context"
	"fmt"
	"path"
)

// Ls is `ccenv ls`: every org dir with an org.env, whatever its MANAGER.
func (a *App) Ls(ctx context.Context) error {
	fmt.Fprintf(a.Stdout, "%-14s %-6s %-6s %-6s %-8s %s\n", "ORG", "STATE", "SSH", "TTYD", "TOKEN", "REMOTE")
	for _, o := range a.orgDirs() {
		if !a.isFile(a.Orgs.EnvPath(o)) {
			continue
		}
		r, s := "-", "down"
		if a.running(ctx, o) {
			s = "up"
			switch {
			case a.env(o, "REMOTE_CONTROL") == "0":
				r = "off"
			case !a.rcLoggedIn(ctx, o):
				r = "login-needed"
			case a.rcRunning(ctx, o):
				r = "on"
			case a.rcBlocked(ctx, o):
				r = "blocked-by-org"
			default:
				r = "restarting"
			}
		}
		token := "MISSING"
		if a.env(o, "CLAUDE_CODE_OAUTH_TOKEN")+a.env(o, "ANTHROPIC_API_KEY") != "" {
			token = "set"
		}
		fmt.Fprintf(a.Stdout, "%-14s %-6s %-6s %-6s %-8s %s\n", o, s, a.env(o, "SSH_PORT"), a.env(o, "TTYD_PORT"), token, r)
	}
	return nil
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
