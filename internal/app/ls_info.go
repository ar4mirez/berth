package app

import (
	"context"
	"fmt"
	"net"

	"github.com/ar4mirez/berth/internal/ops"
)

// Ls is `ccenv ls`: every org dir with an org.env, whatever its MANAGER. With registered hosts
// (#45), their orgs follow this machine's, with a HOST column; a host that can't be reached is
// reported, never hidden. Without any, the output is ccenv's.
func (a *App) Ls(ctx context.Context) error {
	out := ops.ListOrgs(ctx, a)
	if a.Output == OutputJSON {
		return a.writeJSON(out)
	}
	format, head := "%-14s %-6s %-6s %-6s %-8s %s\n", []any{"ORG", "STATE", "SSH", "TTYD", "TOKEN", "REMOTE"}
	if out.MultiHost {
		format, head = "%-14s %-6s %-6s %-6s %-8s %-14s %s\n", append(head, "HOST")
	}
	sayf(a.Stdout, format, head...)
	for _, r := range out.Orgs {
		token := "MISSING"
		if r.Token {
			token = "set"
		}
		cols := []any{r.Name, r.State, r.SSHRaw, r.TTYDRaw, token, r.Remote}
		if out.MultiHost {
			cols = append(cols, r.Host)
		}
		sayf(a.Stdout, format, cols...)
	}
	for _, u := range out.Unreachable {
		sayf(a.Stderr, "%s: host %s is unreachable, its orgs aren't listed: %s\n", Tool, u.Host, u.Error)
	}
	return nil
}

// Info is `ccenv info <org>`: every way to connect.
func (a *App) Info(ctx context.Context, o string) error {
	in, err := ops.GetInfo(ctx, a, o)
	if err != nil {
		return err
	}
	if in.KeyError != "" {
		fmt.Fprintln(a.Stderr, in.KeyError)
	}
	if a.Output == OutputJSON {
		return a.writeJSON(in)
	}
	url := in.RemoteURL
	if url == "" {
		url = "not set up yet; run: " + Tool + " login " + o
	}
	sayf(a.Stdout, `== %[1]s ==  (container claude-%[1]s, %[2]s)

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
`, o, in.State, url, Tool, in.SSHRaw, in.AddressText, in.TTYDRaw, in.GitPublicKey)
	if t := in.Tunnel; t != nil {
		sayf(a.Stdout, "\nSSH tunnel              %s connect %s   (then SSH and the browser terminal above, on 127.0.0.1)\n", Tool, t.Connect)
		if t.SSHTarget != "" {
			sayf(a.Stdout, "                        or by hand: ssh -N -L %[1]s:127.0.0.1:%[1]s -L %[2]s:127.0.0.1:%[2]s %[3]s\n", in.SSHRaw, in.TTYDRaw, t.SSHTarget)
		}
	}
	return nil
}

// sshTarget is "[-p port ]user@host" for ssh's command line, from host:port.
func sshTarget(user, addr string) string {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return user + "@" + addr
	}
	if p != "22" {
		return "-p " + p + " " + user + "@" + h
	}
	return user + "@" + h
}
