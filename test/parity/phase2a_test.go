package parity

import (
	"slices"
	"strings"
	"testing"
)

// Phase 2a: down, restart, attach, shell, claude, run.

func running(org string, more ...Rule) []Rule {
	return append([]Rule{
		{Bin: "docker", Match: `^ps --format`, Stdout: "claude-" + org + "\n"},
		{Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"},
	}, more...)
}

func init() {
	more := []checked{
		{Scenario{Name: "restart", Args: []string{"restart", "acme"}, Files: twoOrgs, Rules: running("acme")}, 0, nil},
		{Scenario{Name: "restart, compose fails", Args: []string{"restart", "globex"}, Files: withQuarantine, Rules: []Rule{
			{Bin: "docker", Match: `^compose `, Stderr: "Error: port is already allocated\n", Exit: 1},
		}}, 1, []string{"port is already allocated"}},
		{Scenario{Name: "down, tailscale down", Args: []string{"down", "acme"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "tailscale", Match: `^ip -4$`, Exit: 1},
		}}, 0, []string{"<tool>: BIND_ADDR=tailscale but tailscale is not up on this host"}},
		{Scenario{Name: "down, unknown org", Args: []string{"down", "nope"}, Files: twoOrgs}, 1, []string{"unknown org 'nope'"}},
		{Scenario{Name: "attach", Args: []string{"attach", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec -it -u node claude-acme tmux`, Exit: 0})}, 0, nil},
		{Scenario{Name: "attach, not running", Args: []string{"attach", "globex"}, Files: twoOrgs},
			1, []string{"<tool>: claude-globex is not running (<tool> up globex)"}},
		{Scenario{Name: "shell, exit code", Args: []string{"shell", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec -it -u node -w /workspace claude-acme bash -l$`, Exit: 130})}, 130, nil},
		{Scenario{Name: "claude, flags passed through", Args: []string{"claude", "acme", "--resume", "-p", "two words", "--", "-h"},
			Files: twoOrgs, Rules: running("acme")}, 0, nil},
		{Scenario{Name: "claude, missing org", Args: []string{"claude"}}, 1, []string{"missing <org>"}},
		{Scenario{Name: "claude, not running", Args: []string{"claude", "globex", "--help"}, Files: twoOrgs},
			1, []string{"not running"}},
		{Scenario{Name: "run, stdin and exit code", Args: []string{"run", "acme", "fix the build", "--model", "x", "--output-format", "json"},
			Stdin: "context from stdin\n", Files: twoOrgs, Rules: running("acme",
				Rule{Bin: "docker", Match: `^exec -i -u node -w /workspace claude-acme claude -p`, Stdin: true, Stdout: "{\"ok\":true}\n", Exit: 7})},
			7, []string{`{"ok":true}`}},
		{Scenario{Name: "run, missing prompt", Args: []string{"run", "acme"}, Files: twoOrgs, Rules: running("acme")},
			1, []string{"<tool>: missing prompt"}},
		{Scenario{Name: "run, not running", Args: []string{"run", "globex", "hi"}, Files: twoOrgs}, 1, []string{"not running"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	ported["down"] = true
}

// writing are the commands that act on an org and so need MANAGER=berth, with example args.
var writing = [][]string{
	{"down", "acme"}, {"restart", "acme"}, {"attach", "acme"}, {"shell", "acme"},
	{"claude", "acme", "--resume"}, {"run", "acme", "hi"}, {"up", "acme"},
	{"fw", "acme", "allow", "x.example"}, {"fw", "acme", "deny", "pypi.org"}, {"fw", "acme", "off"}, {"fw", "acme", "reload"},
	{"token", "acme", "--paste"}, {"auth", "acme"}, {"login", "acme"}, {"logout", "acme", "--all"}, {"gh-login", "acme"},
	// Writing subcommands of reading commands check for themselves.
	{"password", "acme", "rotate"}, {"env", "acme", "set", "X_KEY"}, {"env", "acme", "unset", "X_KEY"}, {"remote", "acme", "restart"},
	{"repo", "add", "acme", "acme/widget"}, {"repo", "rm", "acme", "app"}, {"repo", "remove", "acme", "app", "--delete"},
	{"repo", "new", "acme", "acme/widget"}, {"repo", "create", "acme", "x", "--local"}, {"repo", "publish", "acme", "notes", "acme/notes"},
	{"repo", "adopt", "acme", "--all"}, {"repo", "sync", "acme"}, {"rehydrate", "acme"}, {"handback", "acme"}, {"repo", "policy", "acme", "warn"}, {"clone", "acme", "acme/widget"},
}

// writesAnyOrg write, so --read-only refuses them, but act on either tool's orgs (a backup doesn't
// change the org).
var writesAnyOrg = [][]string{{"backup", "acme"}, {"backup", "--all"}, {"keygen"}, {"schedule"}, {"schedule", "run"}, {"schedule", "off"}, {"schedule", "status", "off"},
	{"restore", "-"}, {"migrate", "acme", "ops@new-host"}, {"takeover", "acme"}}

// TestBerthRefusesLegacyOrgs: berth's writing commands refuse an org it doesn't own (no MANAGER,
// or MANAGER=ccenv), before touching anything: no tool calls, no file changes.
func TestBerthRefusesLegacyOrgs(t *testing.T) {
	unowned := BerthUnowned(berthBin)
	ccenvOwned := merge(twoOrgs, map[string]File{"state/orgs/acme/org.env": {Content: "MANAGER=ccenv\n" + acmeEnv}})
	for _, args := range writing {
		for name, files := range map[string]map[string]File{"no MANAGER": twoOrgs, "MANAGER=ccenv": ccenvOwned} {
			t.Run(strings.Join(args, " ")+", "+name, func(t *testing.T) {
				t.Parallel()
				s := Scenario{Args: args, Files: files, Rules: running("acme")}
				r := run(t, unowned, s)
				want := "<tool>: org 'acme' is managed by ccenv (MANAGER=unset, so ccenv); use: ccenv ... acme\n"
				if name == "MANAGER=ccenv" {
					want = "<tool>: org 'acme' is managed by ccenv (MANAGER=ccenv); use: ccenv ... acme\n"
				}
				if r.Exit != 1 || r.Stderr != want || r.Stdout != "" || len(r.Calls) != 0 {
					t.Errorf("exit %d, stderr %q, stdout %q, calls %v; want exit 1 and %q only", r.Exit, r.Stderr, r.Stdout, r.Calls, want)
				}
				if before := run(t, unowned, Scenario{Args: []string{"ls"}, Files: files}); strings.Join(before.Tree, "\n") != strings.Join(r.Tree, "\n") {
					t.Error("the refused command changed files")
				}
			})
		}
	}
}

// TestReadOnlyRefusesWritingCommands: under --read-only every writing command is refused, including
// the passthrough ones whose own flags cobra doesn't parse (--read-only goes before the command).
func TestReadOnlyRefusesWritingCommands(t *testing.T) {
	ro := Berth(berthBin)
	cmd := ro.Command
	ro.Command = func(run string, args []string) []string {
		argv := cmd(run, args)
		return append([]string{argv[0], "--read-only"}, argv[1:]...)
	}
	for _, args := range append(slices.Clone(writing), writesAnyOrg...) {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			r := run(t, ro, Scenario{Args: args, Files: twoOrgs, Rules: running("acme")})
			if r.Exit != 1 || !strings.Contains(r.Stderr, "read-only mode (--read-only): refusing to ") || len(r.Calls) != 0 {
				t.Errorf("exit %d, stderr %q, calls %v", r.Exit, r.Stderr, r.Calls)
			}
		})
	}
}
