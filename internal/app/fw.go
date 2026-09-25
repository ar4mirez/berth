package app

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// firewallTemplate is ccenv's write_firewall_template.
func firewallTemplate(o string) string {
	return `# Egress allowlist for ` + o + `, applied live by: ` + Tool + ` fw ` + o + ` ...
# One entry per line:  domain (pypi.org) | IP or CIDR (10.0.0.0/8) | @preset (@python) | mode on|off
# Always allowed: Anthropic/Claude, GitHub, npm.  List presets: ` + Tool + ` fw ` + o + ` presets
# Entries are resolved to IPs, so wildcards (*.example.com) are not supported.
mode on
# Toolchain installs via mise, plus package registries for common languages:
@mise
@python
@go
@rust
@ruby
`
}

// fwUnported are the fw subcommands that write: they come with phase 3.
var fwUnported = map[string]bool{"allow": true, "add": true, "deny": true, "remove": true, "rm": true,
	"on": true, "off": true, "edit": true, "reload": true, "test": true}

// Fw is `ccenv fw <org> [sub]` for the subcommands that only read: show (the default) and presets.
func (a *App) Fw(ctx context.Context, o string, args []string) error {
	if err := a.needOrg(o); err != nil {
		return err
	}
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	if fwUnported[sub] {
		return fmt.Errorf("fw %s is not in berth yet (phase 3); use ccenv for now", sub)
	}
	// ccenv writes the template before looking at the subcommand, whatever it is. Under --read-only
	// berth uses the template without writing it.
	f := path.Join(a.Orgs.Dir, o, "config", "firewall.txt")
	content, err := a.Host.FS.ReadFile(f)
	if err != nil {
		content = []byte(firewallTemplate(o))
		if !a.State.ReadOnly {
			if err := a.writeNew(ctx, f, content); err != nil {
				return err
			}
		}
	}
	switch sub {
	case "show":
		fmt.Fprintln(a.Stdout, f)
		// grep -vE '^\s*(#|$)' "$f" | sed 's/^/  /': under pipefail and set -e, a file with no
		// entries (grep selects nothing, exit 1) ends the command here with exit 1.
		shown := 0
		for _, l := range fileLines(content) {
			if t := strings.TrimLeft(l, " \t\n\v\f\r"); t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			fmt.Fprintln(a.Stdout, "  "+l)
			shown++
		}
		if shown == 0 {
			return &Exit{Code: 1}
		}
		if a.running(ctx, o) {
			// echo "live: $(docker exec … cat /run/firewall.status 2>/dev/null || echo unknown)"
			live, err := a.captureRaw(ctx, true, "docker", "exec", "claude-"+o, "cat", "/run/firewall.status")
			if err != nil {
				live += "unknown\n"
			}
			fmt.Fprintln(a.Stdout, "live: "+strings.TrimRight(live, "\n"))
		}
		return nil
	case "presets":
		if a.running(ctx, o) {
			return a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "init-firewall.sh", "presets")
		}
		return a.passthrough(ctx, false, "docker", "run", "--rm", "--entrypoint", "init-firewall.sh", "claude-env", "presets")
	}
	return fmt.Errorf("usage: %s fw <org> [show|allow|deny|on|off|edit|reload|presets|test]", Tool)
}

// fileLines splits file content into lines the way grep reads them: a final newline ends the
// last line, and a last line without one still counts.
func fileLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	ls := strings.Split(string(b), "\n")
	if ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}
