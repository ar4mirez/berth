package parity

import (
	"strings"
	"testing"
)

// ported lists the scenarios berth implements: TestParity requires an empty legacy-vs-berth diff
// for each. A PARITY.md row is "done" once all its scenarios are here.
var ported = map[string]bool{
	"info unknown org": true, "info missing org": true, "ls": true, "info running org": true,
	"info, no SSH_PORT": true,
}

// Remote Control states for ls: off, login-needed, blocked-by-org, restarting, on.
var fiveOrgs = merge(twoOrgs,
	orgFiles("initech", strings.Replace(acmeEnv, "SSH_PORT=2201", "SSH_PORT=2203", 1)),
	orgFiles("t-restart", strings.Replace(acmeEnv, "SSH_PORT=2201", "SSH_PORT=2204", 1)),
	orgFiles("t-apikey", "ANTHROPIC_API_KEY=sk-ant-api-FAKE\nSSH_PORT=2205\nTTYD_PORT=7705\n"),
	map[string]File{
		"state/orgs/.restore-Ab12Cd/org.env": {Content: "SSH_PORT=9999\n"}, // hidden: skipped
		"state/orgs/t-noenv":                 {Dir: true},                  // no org.env: skipped
		"state/orgs/notes.txt":               {Content: "not an org\n"},    // a file: skipped
		"state/elsewhere/org.env":            {Content: "SSH_PORT=2206\nTTYD_PORT=7706\n"},
		"state/orgs/t-link":                  {Link: "../elsewhere"}, // symlinked org dir: listed
	})

var fiveRunning = []Rule{
	{Bin: "docker", Match: `^ps --format`, Stdout: "claude-globex\nclaude-initech\nclaude-t-restart\nclaude-acme-old\n"},
	// globex has REMOTE_CONTROL=0 in its fixture, but make it running and logged out anyway: "off" wins.
	{Bin: "docker", Match: `^exec claude-initech test -f`},
	{Bin: "docker", Match: `^exec claude-initech pgrep`, Exit: 1},
	{Bin: "docker", Match: `^exec claude-initech sh -c tail`, Stdout: "retrying\nError: blocked by organization policy\n"},
	{Bin: "docker", Match: `^exec claude-t-restart test -f`, Stderr: "note from docker\n"},
	{Bin: "docker", Match: `^exec claude-t-restart pgrep`, Exit: 1},
	{Bin: "docker", Match: `^exec claude-t-restart sh -c tail`, Stdout: "starting\n"},
}

func init() {
	more := []checked{
		{Scenario{Name: "ls, remote control states", Args: []string{"ls"}, Files: fiveOrgs, Rules: fiveRunning},
			0, []string{"blocked-by-org", "restarting", "t-link", "t-apikey"}},
		{Scenario{Name: "ls, logged out", Args: []string{"ls"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n"},
			{Bin: "docker", Match: `^exec claude-acme test -f`, Exit: 1, Stderr: "Error: not found\n"},
		}}, 0, []string{"login-needed"}},
		{Scenario{Name: "ls, no orgs dir", Args: []string{"ls"}}, 0, []string{"ORG"}},
		// Under pipefail a failing docker ps means "not running", even though it printed the name.
		{Scenario{Name: "ls, docker ps fails", Args: []string{"ls"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n", Stderr: "Cannot connect to the Docker daemon\n", Exit: 1},
		}}, 0, []string{"down"}},
		{Scenario{Name: "info, stopped, local only", Args: []string{"info", "globex"}, Files: twoOrgs, Rules: nothingRunning},
			0, []string{"(container claude-globex, stopped)", "127.0.0.1 (local only", "<tool> login globex"}},
		{Scenario{Name: "info, all interfaces, CCENV_HOST", Args: []string{"info", "globex"}, Env: []string{"CCENV_HOST=box.example.com"},
			Files: merge(twoOrgs, map[string]File{"state/orgs/globex/org.env": {Content: strings.Replace(globexEnv, "127.0.0.1", "0.0.0.0", 1)}})},
			0, []string{"node@box.example.com"}},
		{Scenario{Name: "info, all interfaces, hostname", Args: []string{"info", "globex"},
			Files: merge(twoOrgs, map[string]File{"state/orgs/globex/org.env": {Content: strings.Replace(globexEnv, "127.0.0.1", "0.0.0.0", 1)}})},
			0, []string{"node@parity-host"}},
		// resolve_bind dies inside $(host_addr), where set -e is off: the message, then an empty host, exit 0.
		{Scenario{Name: "info, tailscale down", Args: []string{"info", "acme"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "tailscale", Match: `^ip -4$`, Exit: 1, Stderr: "not running\n"},
		}}, 0, []string{"<tool>: BIND_ADDR=tailscale but tailscale is not up on this host", "node@ tmux"}},
		{Scenario{Name: "info, no public key", Args: []string{"info", "globex"}, Rules: nothingRunning,
			Files: func() map[string]File {
				f := merge(twoOrgs)
				delete(f, "state/orgs/globex/ssh/id_ed25519.pub")
				return f
			}()}, 0, []string{"cat: <RUN>/state/orgs/globex/ssh/id_ed25519.pub: No such file or directory"}},
		{Scenario{Name: "info, empty SSH_PORT", Args: []string{"info", "globex"}, Rules: nothingRunning,
			Files: merge(twoOrgs, map[string]File{"state/orgs/globex/org.env": {Content: "SSH_PORT=\nTTYD_PORT=7702\n"}})},
			0, []string{"ssh -t -p  node@127.0.0.1"}},
		{Scenario{Name: "info, invalid org name", Args: []string{"info", "../globex"}, Files: twoOrgs}, 1, []string{"unknown org '../globex'"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
}

// TestParity runs every ported scenario through legacy ccenv and berth and requires them to match:
// stdout, stderr, exit code, tool calls and the file tree.
func TestParity(t *testing.T) {
	legacy, berth := Legacy(repoRoot), Berth(berthBin)
	n := 0
	for _, sc := range scenarios {
		if !ported[sc.Name] {
			continue
		}
		n++
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel() // each run has its own dir and fake-tool log
			if d := Diff("ccenv", run(t, legacy, sc.Scenario), "berth", run(t, berth, sc.Scenario)); d != "" {
				t.Errorf("berth differs from legacy:\n%s", d)
			}
		})
	}
	for name := range ported {
		if !hasScenario(name) {
			t.Errorf("ported lists %q, which isn't a scenario", name)
		}
	}
	if n == 0 {
		t.Fatal("no ported scenarios")
	}
}

func hasScenario(name string) bool {
	for _, s := range scenarios {
		if s.Name == name {
			return true
		}
	}
	return false
}

// readCommands only read berth's state, so they must behave the same under --read-only.
var readCommands = map[string]bool{"ls": true, "info": true, "whoami": true, "logs": true, "fw": true, "repo": true}

// writingSubs are the subcommands of those commands that write (refused under --read-only).
var writingSubs = map[string]map[string]bool{
	"fw":   {"allow": true, "add": true, "deny": true, "remove": true, "rm": true, "on": true, "off": true, "edit": true, "reload": true},
	"repo": {"add": true, "new": true, "create": true, "publish": true, "rm": true, "remove": true, "adopt": true, "sync": true, "policy": true},
}

// readsOnly reports whether a scenario's command line only reads.
func readsOnly(args []string) bool {
	if len(args) == 0 || !readCommands[args[0]] {
		return false
	}
	sub := ""
	switch args[0] {
	case "fw":
		if len(args) > 2 {
			sub = args[2] // fw <org> <sub>
		}
	case "repo":
		if len(args) > 1 {
			sub = args[1] // repo <sub> <org>
		}
		if sub == "policy" && len(args) < 4 {
			return true // repo policy <org> shows the policy
		}
	}
	return !writingSubs[args[0]][sub]
}

// readOnlySkipsWrites are scenarios where legacy creates files as a side effect of a read (compose's
// bind-mount dirs, fw's template), which berth skips under --read-only.
var readOnlySkipsWrites = map[string]bool{
	"logs": true, "logs, exit code passes through": true, "logs, tailscale down": true, "fw show, no file yet": true,
}

// TestParityReadOnly: the phase 1 commands only read, so `berth --read-only` must still match
// legacy exactly. This is how berth is first used against the live orgs.
func TestParityReadOnly(t *testing.T) {
	legacy, berth := Legacy(repoRoot), Berth(berthBin)
	cmd := berth.Command
	berth.Command = func(run string, args []string) []string {
		argv := cmd(run, args)
		return append([]string{argv[0], "--read-only"}, argv[1:]...)
	}
	for _, sc := range scenarios {
		if !ported[sc.Name] || !readsOnly(sc.Args) {
			continue // writing commands are refused under --read-only: TestReadOnlyRefusesWritingCommands
		}
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel() // each run has its own dir and fake-tool log
			l, b := run(t, legacy, sc.Scenario), run(t, berth, sc.Scenario)
			if readOnlySkipsWrites[sc.Name] {
				// Legacy creates files here; berth --read-only doesn't (by design). Compare the rest.
				l.Tree, b.Tree = nil, nil
			}
			if d := Diff("ccenv", l, "berth --read-only", b); d != "" {
				t.Errorf("berth --read-only differs from legacy:\n%s", d)
			}
		})
	}
}
