package app

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
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

// fwWrites are the fw subcommands that change the allowlist (or open an editor on it).
var fwWrites = map[string]bool{"allow": true, "add": true, "deny": true, "remove": true, "rm": true,
	"on": true, "off": true, "edit": true, "reload": true}

// FwPresets is the preset list ccenv's completion offers for `fw <org> allow`.
var FwPresets = []string{"@mise", "@python", "@node", "@go", "@rust", "@ruby", "@docker", "@gitlab", "@bitbucket", "@aws", "@gcp", "@azure", "@debian"}

// Fw is `ccenv fw <org> [sub] [entries...]`. The writing subcommands need MANAGER=berth and are
// refused under --read-only; show, presets and test read.
func (a *App) Fw(ctx context.Context, o string, args []string) error {
	sub := "show"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	if fwWrites[sub] {
		if err := a.writable(o, "change "+o+"'s firewall"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
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
	case "allow", "add":
		if len(args) == 0 {
			return fmt.Errorf("usage: %s fw %s allow <domain|ip|cidr|@preset>...", Tool, o) //nolint:staticcheck // ccenv's exact usage text
		}
		for _, e := range args {
			// e="${e#*://}"; e="${e%%/*}"; e="${e%%:*}": https://x.com:443/path -> x.com
			if _, after, ok := strings.Cut(e, "://"); ok {
				e = after
			}
			e, _, _ = strings.Cut(e, "/")
			e, _, _ = strings.Cut(e, ":")
			if hasLine(content, e) {
				fmt.Fprintln(a.Stdout, "already allowed: "+e)
				continue
			}
			content = append(content, e+"\n"...) // echo "$e" >> "$f": no newline added before it
			if err := a.Host.FS.WriteFile(f, content, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(a.Stdout, "allowed: "+e)
		}
		return a.fwApply(ctx, o)
	case "deny", "remove", "rm":
		if len(args) == 0 {
			return fmt.Errorf("usage: %s fw %s deny <entry>...", Tool, o) //nolint:staticcheck // ccenv's exact usage text
		}
		for _, e := range args {
			if !hasLine(content, e) {
				fmt.Fprintln(a.Stdout, "not in list: "+e)
				continue
			}
			var kept []byte
			for _, l := range fileLines(content) {
				if l != e {
					kept = append(kept, l+"\n"...)
				}
			}
			// grep -vxF "$e" "$f" > "$tmp" selects nothing when e is all that's left: grep exits 1
			// and set -e ends the command, with the file unchanged.
			if len(kept) == 0 {
				return &Exit{Code: 1}
			}
			content = kept
			if err := a.Host.FS.WriteFile(f, content, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(a.Stdout, "removed: "+e)
		}
		return a.fwApply(ctx, o)
	case "on", "off":
		// grep -vE '^\s*mode\s+(on|off)\s*$' "$f" > "$tmp" || true; echo "mode $sub" >> "$tmp"; cat "$tmp" > "$f"
		var kept []byte
		for _, l := range fileLines(content) {
			if !modeLine.MatchString(l) {
				kept = append(kept, l+"\n"...)
			}
		}
		if err := a.Host.FS.WriteFile(f, append(kept, "mode "+sub+"\n"...), 0o644); err != nil {
			return err
		}
		return a.fwApply(ctx, o)
	case "edit":
		editor := a.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		if err := a.Host.Exec.Run(ctx, host.Cmd{Args: []string{editor, f}, TTY: true, Stdin: a.Stdin, Stdout: a.Stdout, Stderr: a.Stderr}); err != nil {
			return err
		}
		return a.fwApply(ctx, o)
	case "reload":
		return a.fwApply(ctx, o)
	case "test":
		if err := a.needUp(ctx, o); err != nil {
			return err
		}
		if len(args) == 0 {
			args = []string{"api.anthropic.com", "github.com", "example.com"}
		}
		for _, e := range args {
			if !strings.Contains(e, "://") {
				e = "https://" + e
			}
			if a.passthrough(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "curl", "-s", "-o", "/dev/null", "--max-time", "6", e) == nil {
				fmt.Fprintln(a.Stdout, "ALLOWED  "+e)
			} else {
				fmt.Fprintln(a.Stdout, "blocked  "+e)
			}
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

// fwApply is fw_apply: apply the list live if the container runs, else say when it will.
func (a *App) fwApply(ctx context.Context, o string) error {
	if a.running(ctx, o) {
		return a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "init-firewall.sh", "apply")
	}
	fmt.Fprintf(a.Stdout, "(saved; applies on: %s up %s)\n", Tool, o)
	return nil
}

// modeLine is grep -E '^\s*mode\s+(on|off)\s*$' (\s is [[:space:]]).
var modeLine = regexp.MustCompile(`^[ \t\n\v\f\r]*mode[ \t\n\v\f\r]+(on|off)[ \t\n\v\f\r]*$`)

// hasLine is `grep -qxF "$e" "$f"`: some line is exactly e ("" matches an empty line).
func hasLine(content []byte, e string) bool {
	for _, l := range fileLines(content) {
		if l == e {
			return true
		}
	}
	return false
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
