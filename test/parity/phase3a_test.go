package parity

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
		{Scenario{Name: "fw allow, odd entries", Args: []string{"fw", "globex", "allow", "http://h.example:8080/a/b", "https://", "pypi.org", "10.0.0.0/8", "@node"},
			Files: fwFile("mode on\npypi.org\n\n")}, 0, []string{"already allowed: pypi.org", "allowed: 10.0.0.0"}},
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
