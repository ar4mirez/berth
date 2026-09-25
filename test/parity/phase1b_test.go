package parity

// Phase 1b: whoami, logs, fw show/presets.

// acmeUp: acme running, globex stopped, with whoami's two docker exec calls scripted.
func acmeUp(authStatus, ghLogin string) []Rule {
	return []Rule{
		{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n"},
		{Bin: "docker", Match: `^exec -u node claude-acme sh -c env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status`, Stdout: authStatus},
		{Bin: "docker", Match: `^exec -u node claude-acme sh -c gh api user`, Stdout: ghLogin},
		{Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"},
	}
}

func withoutFile(files map[string]File, p string) map[string]File {
	f := merge(files)
	delete(f, p)
	return f
}

func withFile(files map[string]File, p string, file File) map[string]File {
	return merge(files, map[string]File{p: file})
}

func init() {
	more := []checked{
		// whoami
		{Scenario{Name: "whoami, signed in", Args: []string{"whoami"}, Files: twoOrgs,
			Rules: acmeUp(`{"loggedIn":true,"email":"dev@example.com","orgName":"Acme Corp","orgId":"org-1"}`+"\n", "octo-acme\n")},
			0, []string{"dev@example.com  [Acme Corp]", "octo-acme", "(container down)"}},
		{Scenario{Name: "whoami, logged out", Args: []string{"whoami", "acme"}, Files: twoOrgs,
			Rules: acmeUp(`{"loggedIn":false}`, "")}, 0, []string{"not logged in (<tool> login acme)", "not signed in"}},
		{Scenario{Name: "whoami, null and odd fields", Args: []string{"whoami", "acme"}, Files: twoOrgs,
			Rules: acmeUp(`{"loggedIn":1,"orgName":{"n":"<x>"}}`, "")}, 0, []string{`null  [{"n":"<x>"}]`}},
		{Scenario{Name: "whoami, two statuses", Args: []string{"whoami", "acme"}, Files: twoOrgs,
			Rules: acmeUp(`{"loggedIn":true,"email":"a@example.com","orgName":"A"} {"loggedIn":false}`, "")}, 0, []string{"a@example.com"}},
		{Scenario{Name: "whoami, not JSON", Args: []string{"whoami", "acme"}, Files: twoOrgs,
			Rules: acmeUp("error: claude not found\n", "")}, 0, []string{"not logged in"}},
		// (Not scripted: a non-object followed by more values. jq 1.6 and 1.7 disagree on the exit
		// status there, and claude auth status prints one object.)
		{Scenario{Name: "whoami, not an object", Args: []string{"whoami", "acme"}, Files: twoOrgs,
			Rules: acmeUp("42\n", "")}, 0, []string{"not logged in"}},
		{Scenario{Name: "whoami, empty status", Args: []string{"whoami", "acme"}, Files: twoOrgs,
			Rules: acmeUp("", "")}, 0, []string{"not signed in"}},
		{Scenario{Name: "whoami, docker exec fails", Args: []string{"whoami", "acme"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n"},
			{Bin: "docker", Match: `^exec -u node claude-acme sh -c env`, Stdout: `{"loggedIn":true,"email":"e","orgName":"o"}`, Stderr: "Error: container stopping\n", Exit: 1},
		}}, 0, []string{"not logged in"}},
		{Scenario{Name: "whoami, named orgs in order", Args: []string{"whoami", "globex", "acme"}, Files: twoOrgs, Rules: nothingRunning},
			0, []string{"globex"}},
		{Scenario{Name: "whoami, unknown org mid-table", Args: []string{"whoami", "acme", "nope", "globex"}, Files: twoOrgs, Rules: nothingRunning},
			1, []string{"<tool>: unknown org 'nope'"}},
		// A last dir without org.env (or no orgs) leaves ccenv's loop with a failed [ -f ], but set -e
		// doesn't fire for it: the table is printed and whoami exits 0.
		{Scenario{Name: "whoami, last dir has no org.env", Args: []string{"whoami"}, Rules: nothingRunning,
			Files: withFile(twoOrgs, "state/orgs/zz-scratch", File{Dir: true})}, 0, []string{"globex"}},
		{Scenario{Name: "whoami, no orgs", Args: []string{"whoami"}}, 0, []string{"ORG"}},
		{Scenario{Name: "whoami, a middle dir has no org.env", Args: []string{"whoami"}, Rules: nothingRunning,
			Files: withFile(twoOrgs, "state/orgs/b-scratch", File{Dir: true})}, 0, []string{"acme", "globex"}},

		// logs
		{Scenario{Name: "logs", Args: []string{"logs", "acme"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^compose .* logs -f$`, Stdout: "claude-acme  | started\n"},
			{Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"},
		}}, 0, []string{"started"}},
		{Scenario{Name: "logs, exit code passes through", Args: []string{"logs", "globex"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^compose .* logs -f$`, Stderr: "no such service\n", Exit: 3},
		}}, 3, []string{"no such service"}},
		{Scenario{Name: "logs, tailscale down", Args: []string{"logs", "acme"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "tailscale", Match: `^ip -4$`, Exit: 1},
		}}, 0, []string{"<tool>: BIND_ADDR=tailscale but tailscale is not up on this host"}},
		{Scenario{Name: "logs, dirs already there", Args: []string{"logs", "globex"}, Files: withQuarantine}, 0, nil},
		{Scenario{Name: "logs, unknown org", Args: []string{"logs", "nope"}, Files: twoOrgs}, 1, []string{"unknown org"}},

		// fw show / presets
		{Scenario{Name: "fw show, stopped", Args: []string{"fw", "globex", "show"}, Files: twoOrgs, Rules: nothingRunning},
			0, []string{"  @python", "  pypi.org"}},
		{Scenario{Name: "fw show, no file yet", Args: []string{"fw", "globex"}, Rules: nothingRunning,
			Files: withoutFile(twoOrgs, "state/orgs/globex/config/firewall.txt")}, 0, []string{"  @ruby"}},
		// Legacy quirk: nothing but comments and blanks, so grep selects nothing and pipefail + set -e
		// end the command with exit 1 right after the path.
		{Scenario{Name: "fw show, only comments", Args: []string{"fw", "globex"}, Rules: nothingRunning,
			Files: withFile(twoOrgs, "state/orgs/globex/config/firewall.txt", File{Content: "# nothing\n\n   \n\t# still nothing\n", Mode: 0o644})},
			1, []string{"firewall.txt"}},
		{Scenario{Name: "fw show, odd lines", Args: []string{"fw", "globex"}, Rules: nothingRunning,
			Files: withFile(twoOrgs, "state/orgs/globex/config/firewall.txt",
				File{Content: "mode on\r\n\r\n  # indented comment\n  indented.example\nx.example # trailing\nlast.example", Mode: 0o644})},
			0, []string{"  last.example"}},
		{Scenario{Name: "fw show, live status fails", Args: []string{"fw", "acme"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n"},
			{Bin: "docker", Match: `^exec claude-acme cat /run/firewall\.status$`, Stdout: "partial", Exit: 1},
		}}, 0, []string{"live: partialunknown"}},
		{Scenario{Name: "fw presets, running", Args: []string{"fw", "acme", "presets"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n"},
			{Bin: "docker", Match: `^exec claude-acme init-firewall\.sh presets$`, Stdout: "@python  pypi.org …\n"},
		}}, 0, []string{"@python"}},
		{Scenario{Name: "fw presets, stopped", Args: []string{"fw", "globex", "presets"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^run --rm --entrypoint init-firewall\.sh claude-env presets$`, Stdout: "@go  proxy.golang.org …\n", Exit: 4},
		}}, 4, []string{"@go"}},
		{Scenario{Name: "fw unknown subcommand", Args: []string{"fw", "globex", "frob"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> fw <org> [show|allow|deny|on|off|edit|reload|presets|test]"}},
		{Scenario{Name: "fw missing org", Args: []string{"fw"}}, 1, []string{"missing <org>"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	for _, name := range []string{"whoami, all down", "fw show, running"} {
		ported[name] = true
	}
}
