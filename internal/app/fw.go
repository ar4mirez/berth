package app

import (
	"context"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
)

// firewallTemplate is ccenv's write_firewall_template.
func firewallTemplate(o string) string { return ops.FirewallTemplate(o) }

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
	// allow/deny/on/off edit firewall.txt under the state root's lock, released before the rules
	// are applied in the container. edit doesn't lock: it waits on a person in an editor.
	unlock := func() {}
	switch sub {
	case "allow", "add", "deny", "remove", "rm", "on", "off":
		u, err := a.lock(ctx)
		if err != nil {
			return err
		}
		unlock = u
	}
	defer unlock()
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
		out, err := ops.GetFirewallEntries(a, o)
		if err != nil {
			return err
		}
		if a.Output == OutputJSON {
			// The same data as the text, but no entries isn't an error here, just an empty list.
			out.Live, _ = ops.FirewallLive(ctx, a, o)
			return a.writeJSON(out)
		}
		fmt.Fprintln(a.Stdout, f)
		// grep -vE '^\s*(#|$)' "$f" | sed 's/^/  /': under pipefail and set -e, a file with no
		// entries (grep selects nothing, exit 1) ends the command here with exit 1.
		for _, l := range out.Entries {
			fmt.Fprintln(a.Stdout, "  "+l)
		}
		if len(out.Entries) == 0 {
			return &Exit{Code: 1}
		}
		if live, text := ops.FirewallLive(ctx, a, o); live != nil {
			fmt.Fprintln(a.Stdout, "live: "+text)
		}
		return nil
	case "allow", "add":
		if len(args) == 0 {
			return fmt.Errorf("usage: %s fw %s allow <domain|ip|cidr|@preset>...", Tool, o) //nolint:staticcheck // ccenv's exact usage text
		}
		for i, e := range args {
			// e="${e#*://}"; e="${e%%/*}"; e="${e%%:*}": https://x.com:443/path -> x.com
			if _, after, ok := strings.Cut(e, "://"); ok {
				e = after
			}
			// berth keeps an IPv4 range's mask (10.0.0.0/8): ccenv cut it, allowing only the first
			// address (PARITY.md).
			if !ipv4CIDR.MatchString(e) {
				e, _, _ = strings.Cut(e, "/")
				e, _, _ = strings.Cut(e, ":")
			}
			// Nothing is written unless every entry is one the firewall can use (#104; PARITY.md).
			if err := fwEntry(e, args[i]); err != nil {
				return err
			}
			args[i] = e
		}
		for _, e := range args {
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
		unlock()
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
		unlock()
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
		unlock()
		return a.fwApply(ctx, o)
	case "edit":
		editor := a.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		if err := a.Host.Exec.Run(ctx, host.Cmd{Args: []string{editor, f}, TTY: true, Stdin: a.Stdin, Stdout: a.Stdout, Stderr: a.Stderr}); err != nil {
			return err
		}
		unlock()
		return a.fwApply(ctx, o)
	case "reload":
		unlock()
		return a.fwApply(ctx, o)
	case "test":
		res, err := ops.TestFirewall(ctx, a, o, args)
		if err != nil {
			return err
		}
		if a.Output == OutputJSON {
			return a.writeJSON(res)
		}
		for _, r := range res.Results {
			if r.Allowed {
				fmt.Fprintln(a.Stdout, "ALLOWED  "+r.URL)
			} else {
				fmt.Fprintln(a.Stdout, "blocked  "+r.URL)
			}
		}
		return nil
	case "presets":
		// The list is printed as the image gives it, and a failing docker ends with its exit code.
		pre, err := ops.GetFirewallPresets(ctx, a, o)
		if a.Output == OutputJSON {
			if err != nil {
				return err
			}
			return a.writeJSON(pre)
		}
		fmt.Fprint(a.Stdout, pre.Raw)
		return err
	}
	return fmt.Errorf("usage: %s fw <org> [show|allow|deny|on|off|edit|reload|presets|test]", Tool)
}

// fwApply is fw_apply: apply the list live if the container runs, else say when it will.
func (a *App) fwApply(ctx context.Context, o string) error {
	if a.running(ctx, o) {
		return a.passthrough(ctx, false, "docker", "exec", "claude-"+o, contract.FirewallScript, "apply")
	}
	sayf(a.Stdout, "(saved; applies on: %s up %s)\n", Tool, o)
	return nil
}

// fwName is a name init-firewall.sh gives dnsmasq: a hostname, optionally with a leading "*.".
// fwPreset is an @preset; the image knows which exist.
var (
	fwName   = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?(\.[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?)*$`)
	fwPreset = regexp.MustCompile(`^@[a-z0-9_-]+$`)
	ipv4     = regexp.MustCompile(`^[0-9]+(\.[0-9]+){3}$`)
)

// fwEntry refuses an entry init-firewall.sh would skip: the same rule, applied before the file is
// written. e is the entry as it would be stored and arg is what was typed. ccenv stores anything.
func fwEntry(e, arg string) error {
	switch {
	case ipv4.MatchString(e), ipv4CIDR.MatchString(e): // before names: 10.0.0.1 reads as one too
		if _, _, err := net.ParseCIDR(e); err == nil || net.ParseIP(e) != nil {
			return nil
		}
		return fmt.Errorf("'%s' isn't an IPv4 address or range", arg)
	case fwPreset.MatchString(e), fwName.MatchString(e):
		return nil
	case e == "":
		return fmt.Errorf("'%s' has no host to allow", arg)
	case strings.Contains(e, "*"):
		return fmt.Errorf("'%s': a wildcard only works as a leading '*.' (the firewall can't match part of a label). "+
			"List each host, or allow the whole domain, like '*.example.com'", arg)
	}
	return fmt.Errorf("'%s' isn't a hostname, an IPv4 address or range, or an @preset", arg)
}

// modeLine is grep -E '^\s*mode\s+(on|off)\s*$' (\s is [[:space:]]).
// ipv4CIDR is an IPv4 range as init-firewall.sh accepts it (ipset hash:net).
var ipv4CIDR = regexp.MustCompile(`^[0-9]+(\.[0-9]+){3}/[0-9]+$`)

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
