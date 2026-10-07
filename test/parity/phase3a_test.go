package parity

import (
	"slices"
	"strings"
	"testing"
)

// Phase 3a: fw allow/add, deny/remove/rm, on/off, edit, reload, test.

func fwFile(content string) map[string]File {
	return withFile(twoOrgs, "state/orgs/globex/config/firewall.txt", File{Content: content, Mode: 0o644})
}

func init() {
	more := []checked{
		{Scenario{Name: "fw allow, running", Args: []string{"fw", "acme", "allow", "files.example.com"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme init-firewall\.sh apply$`, Stdout: "firewall: 14 entries\n"})}, 0, []string{"firewall: 14 entries"}},
		{Scenario{Name: "fw allow, apply fails", Args: []string{"fw", "acme", "add", "x.example"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `init-firewall\.sh apply$`, Stderr: "bad entry\n", Exit: 3})}, 3, []string{"bad entry"}},
		{Scenario{Name: "fw allow, file without final newline", Args: []string{"fw", "globex", "allow", "x.example"},
			Files: fwFile("mode on\npypi.org")}, 0, []string{"allowed: x.example"}},
		{Scenario{Name: "fw allow, odd entries", Args: []string{"fw", "globex", "allow", "http://h.example:8080/a/b", "pypi.org", "10.0.0.1", "@node"},
			Files: fwFile("mode on\npypi.org\n\n")}, 0, []string{"already allowed: pypi.org", "allowed: 10.0.0.1"}},
		{Scenario{Name: "fw allow, no file yet", Args: []string{"fw", "globex", "allow", "@node"},
			Files: withoutFile(twoOrgs, "state/orgs/globex/config/firewall.txt")}, 0, []string{"allowed: @node"}},
		{Scenario{Name: "fw allow, no entries", Args: []string{"fw", "globex", "allow"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> fw globex allow <domain|ip|cidr|@preset>..."}},
		{Scenario{Name: "fw deny, exact lines only", Args: []string{"fw", "globex", "remove", "pypi.org", "https://pypi.org"},
			Files: fwFile("mode on\npypi.org\r\npypi.org\n  pypi.org\npypi.org")}, 0, []string{"removed: pypi.org", "not in list: https://pypi.org"}},
		// Removing the last line left: grep -v selects nothing, and set -e ends the command.
		{Scenario{Name: "fw deny, last line", Args: []string{"fw", "globex", "rm", "pypi.org"}, Files: fwFile("pypi.org\npypi.org\n")}, 1, nil},
		{Scenario{Name: "fw deny, running", Args: []string{"fw", "acme", "deny", "pypi.org"}, Files: twoOrgs, Rules: running("acme")}, 0, []string{"removed"}},
		{Scenario{Name: "fw deny, no entries", Args: []string{"fw", "globex", "deny"}, Files: twoOrgs}, 1, []string{"usage: <tool> fw globex deny <entry>..."}},
		{Scenario{Name: "fw off", Args: []string{"fw", "globex", "off"}, Files: fwFile("# c\nmode on\n  mode   off  \nmodeon\nmode on # x\npypi.org")},
			0, []string{"(saved; applies on: <tool> up globex)"}},
		{Scenario{Name: "fw on, running", Args: []string{"fw", "acme", "on"}, Files: twoOrgs, Rules: running("acme")}, 0, nil},
		{Scenario{Name: "fw edit", Args: []string{"fw", "globex", "edit"}, Env: []string{"EDITOR=true"}, Files: twoOrgs}, 0, []string{"(saved"}},
		{Scenario{Name: "fw edit, editor fails", Args: []string{"fw", "globex", "edit"}, Env: []string{"EDITOR=false"}, Files: twoOrgs}, 1, nil},
		{Scenario{Name: "fw reload, running", Args: []string{"fw", "acme", "reload"}, Files: twoOrgs, Rules: running("acme")}, 0, nil},
		{Scenario{Name: "fw reload, stopped", Args: []string{"fw", "globex", "reload"}, Files: twoOrgs}, 0, []string{"(saved"}},
		{Scenario{Name: "fw test, defaults", Args: []string{"fw", "acme", "test"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `curl .* https://example\.com$`, Exit: 28})},
			0, []string{"ALLOWED  https://api.anthropic.com", "blocked  https://example.com"}},
		{Scenario{Name: "fw test, hosts and URLs", Args: []string{"fw", "acme", "test", "pypi.org", "http://h.example:8080/x"}, Files: twoOrgs, Rules: running("acme")},
			0, []string{"ALLOWED  http://h.example:8080/x"}},
		{Scenario{Name: "fw test, not running", Args: []string{"fw", "globex", "test"}, Files: twoOrgs}, 1, []string{"not running"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	for _, name := range []string{"fw allow, stopped", "fw deny"} {
		ported[name] = true
	}
}

// TestBerthFwAllowKeepsCIDR: berth keeps an IPv4 range's mask, where ccenv cut it at the "/" and
// allowed only the first address (PARITY.md). URLs are still reduced to the host, and deny takes
// the range as written.
func TestBerthFwAllowKeepsCIDR(t *testing.T) {
	r := run(t, Berth(berthBin), Scenario{Args: []string{"fw", "globex", "allow", "10.0.0.0/8", "142.250.0.0/15", "https://h.example/a/b", "10.0.0.0/8"},
		Files: fwFile("mode on\n")})
	if r.Exit != 0 {
		t.Fatalf("allow: exit %d\n%s", r.Exit, r.Stderr)
	}
	for _, want := range []string{"allowed: 10.0.0.0/8", "allowed: 142.250.0.0/15", "allowed: h.example", "already allowed: 10.0.0.0/8"} {
		if !strings.Contains(r.Stdout, want+"\n") {
			t.Errorf("stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	want := `state/orgs/globex/config/firewall.txt 0644 "mode on\n10.0.0.0/8\n142.250.0.0/15\nh.example\n"`
	if !slices.Contains(r.Tree, want) {
		t.Errorf("firewall.txt: want %s in\n%s", want, strings.Join(r.Tree, "\n"))
	}
	r = run(t, Berth(berthBin), Scenario{Args: []string{"fw", "globex", "deny", "10.0.0.0/8"}, Files: fwFile("mode on\n10.0.0.0/8\n")})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "removed: 10.0.0.0/8") {
		t.Errorf("deny: exit %d\n%s%s", r.Exit, r.Stdout, r.Stderr)
	}
}

// TestBerthFwAllowRefusesBadEntries: berth writes only entries the firewall can use, where ccenv
// stores anything (PARITY.md). One name dnsmasq can't parse used to cost the container its
// resolver (#104). A refused entry stops the command before the file or the container is touched.
func TestBerthFwAllowRefusesBadEntries(t *testing.T) {
	for arg, want := range map[string]string{
		"web-git-*.example.app":  "<tool>: 'web-git-*.example.app': a wildcard only works as a leading '*.' (the firewall can't match part of a label). List each host, or allow the whole domain, like '*.example.com'\n",
		"https://":               "<tool>: 'https://' has no host to allow\n",
		"999.1.1.1":              "<tool>: '999.1.1.1' isn't an IPv4 address or range\n",
		"exa$mple.com":           "<tool>: 'exa$mple.com' isn't a hostname, an IPv4 address or range, or an @preset\n",
		"https://-bad.example/x": "<tool>: 'https://-bad.example/x' isn't a hostname, an IPv4 address or range, or an @preset\n",
	} {
		r := run(t, Berth(berthBin), Scenario{Args: []string{"fw", "acme", "allow", "ok.example.com", arg}, Files: twoOrgs, Rules: running("acme")})
		if r.Exit != 1 || r.Stderr != want || r.Stdout != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", arg, r.Exit, r.Stdout, r.Stderr)
		}
		for _, l := range r.Tree {
			if strings.HasPrefix(l, "state/orgs/acme/config/firewall.txt ") && strings.Contains(l, "ok.example.com") {
				t.Errorf("%s: firewall.txt was written:\n%s", arg, l)
			}
		}
		for _, c := range r.Calls {
			if strings.Contains(c, "init-firewall") {
				t.Errorf("%s: the firewall was applied: %s", arg, c)
			}
		}
	}
	// A leading wildcard is fine, and so is what was there before.
	r := run(t, Berth(berthBin), Scenario{Args: []string{"fw", "globex", "allow", "*.example.com"}, Files: fwFile("mode on\nweb-git-*.example.app\n")})
	want := `state/orgs/globex/config/firewall.txt 0644 "mode on\nweb-git-*.example.app\n*.example.com\n"`
	if r.Exit != 0 || r.Stdout != "allowed: *.example.com\n(saved; applies on: <tool> up globex)\n" || !slices.Contains(r.Tree, want) {
		t.Errorf("leading wildcard: exit %d, stdout %q, stderr %q\n%s", r.Exit, r.Stdout, r.Stderr, strings.Join(r.Tree, "\n"))
	}
}

// TestBerthFirewallTemplate: a new firewall.txt says what the firewall does with a domain since
// #104, where ccenv's template still says wildcards aren't supported (PARITY.md).
func TestBerthFirewallTemplate(t *testing.T) {
	raw := Berth(berthBin)
	raw.Content = nil // the text as berth writes it
	r := run(t, raw, Scenario{Args: []string{"fw", "globex", "allow", "@node"}, Files: withoutFile(twoOrgs, "state/orgs/globex/config/firewall.txt")})
	var file string
	for _, l := range r.Tree {
		if strings.HasPrefix(l, "state/orgs/globex/config/firewall.txt ") {
			file = l
		}
	}
	if r.Exit != 0 || !strings.Contains(file, `# A domain allows that name and every name under it; *.example.com means the same.\n`) || strings.Contains(file, "not supported") {
		t.Errorf("exit %d, firewall.txt: %s", r.Exit, file)
	}
}
