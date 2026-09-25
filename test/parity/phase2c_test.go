package parity

import (
	"strings"
	"testing"
)

// Phase 2c: init, password, env, remote.

const ttydRandom = `^node:[A-Za-z0-9]{32}$`

var newOrgRandom = map[string]string{"state/orgs/t-new/config/secrets/ttyd_credential": ttydRandom}

// rcUp is acme running and signed in to Remote Control, with the log tail scripted.
func rcUp(log string, more ...Rule) []Rule {
	return running("acme", append([]Rule{
		{Bin: "docker", Match: `^exec claude-acme test -f /home/node/\.claude/\.credentials\.json$`},
		{Bin: "docker", Match: `^exec claude-acme pgrep -f claude remote-control$`},
		{Bin: "docker", Match: `^exec claude-acme sh -c grep -ao`, Stdout: "https://claude.ai/code?environment=env_PARITY01\n"},
		{Bin: "docker", Match: `^exec claude-acme sh -c sed `, Stdout: log},
	}, more...)...)
}

func init() {
	envWith := func(extra string) map[string]File {
		return withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: acmeEnv + extra})
	}
	more := []checked{
		// init
		{Scenario{Name: "init, keys and git identity from home", Args: []string{"init", "t-new", "--email", "t@example.com"}, Rules: nothingRunning,
			Random: newOrgRandom, Files: merge(twoOrgs, map[string]File{
				"home/.gitconfig":       {Content: "[user]\n\tname = Git Config Name\n\temail = ignored@example.com\n"},
				"home/.ssh":             {Dir: true},
				"home/.ssh/id_b.pub":    {Content: "ssh-ed25519 AAAAB operator@b\n", Mode: 0o644},
				"home/.ssh/id_a.pub":    {Content: "ssh-ed25519 AAAAA operator@a\n", Mode: 0o644},
				"home/.ssh/id_a":        {Content: "PRIVATE\n"},
				"home/.ssh/.hidden.pub": {Content: "ssh-ed25519 AAAAH hidden\n", Mode: 0o644},
				"home/.ssh/known_hosts": {Content: "x\n", Mode: 0o644},
			})}, 0, []string{"Created"}},
		{Scenario{Name: "init, an unreadable key empties authorized_keys", Args: []string{"init", "t-new"}, Rules: nothingRunning,
			Random: newOrgRandom, Files: merge(twoOrgs, map[string]File{
				"home/.ssh":          {Dir: true},
				"home/.ssh/id_a.pub": {Content: "ssh-ed25519 AAAAA operator@a\n", Mode: 0o644},
				"home/.ssh/id_z.pub": {Content: "unreadable\n", Mode: 0o000},
			})}, 0, []string{"Created"}},
		{Scenario{Name: "init, first org", Args: []string{"init", "t-new", "--name", "N", "--email", "e@example.com"}, Rules: nothingRunning,
			Random: newOrgRandom}, 0, []string{"claude-t-new@parity-host"}},
		{Scenario{Name: "init, over a partial scaffold", Args: []string{"init", "t-new"}, Rules: nothingRunning, Random: newOrgRandom,
			Files: map[string]File{"state/orgs/t-new/workspace/keep": {Content: "x\n", Mode: 0o644}, "state/orgs/t-new/config/firewall.txt": {Content: "old\n"}}},
			0, []string{"Created"}},
		{Scenario{Name: "init, already exists", Args: []string{"init", "acme"}, Files: twoOrgs}, 1, []string{"<tool>: acme already exists"}},
		{Scenario{Name: "init, unknown flag", Args: []string{"init", "t-new", "--nickname", "x"}, Files: twoOrgs}, 1, []string{"<tool>: unknown flag --nickname"}},
		{Scenario{Name: "init, no name", Args: []string{"init"}}, 1, []string{"lowercase"}},

		// password
		{Scenario{Name: "password show, no file", Args: []string{"password", "acme"},
			Files: withoutFile(twoOrgs, "state/orgs/acme/config/secrets/ttyd_credential")}, 1, []string{"<tool>: no password set (<tool> password acme rotate)"}},
		{Scenario{Name: "password show, empty file", Args: []string{"password", "acme", "show"},
			Files: withFile(twoOrgs, "state/orgs/acme/config/secrets/ttyd_credential", File{Content: ""})}, 1, []string{"no password set"}},
		{Scenario{Name: "password show, odd content", Args: []string{"password", "acme"},
			Files: withFile(twoOrgs, "state/orgs/acme/config/secrets/ttyd_credential", File{Content: "node:a:b\nno-colon\nx:y\n\n"})}, 0, []string{"pass: a:b"}},
		{Scenario{Name: "password rotate, running", Args: []string{"password", "acme", "rotate"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme pkill -x ttyd$`, Exit: 1})},
			0, []string{"Rotated"}},
		{Scenario{Name: "password rotate, stopped", Args: []string{"password", "globex", "rotate"}, Files: twoOrgs}, 0, []string{"Rotated"}},
		{Scenario{Name: "password, unknown subcommand", Args: []string{"password", "acme", "reset"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> password <org> [show|rotate]"}},

		// env
		{Scenario{Name: "env ls, none", Args: []string{"env", "acme"}, Files: twoOrgs}, 0, []string{"(no custom variables; add one: <tool> env acme set KEY)"}},
		{Scenario{Name: "env ls", Args: []string{"env", "acme", "list"}, Files: envWith("CCENV_ENV_KEYS=OPENROUTER_API_KEY  GONE_KEY\nOPENROUTER_API_KEY='x'\n")},
			0, []string{"GONE_KEY  (listed but missing)"}},
		{Scenario{Name: "env set, update", Args: []string{"env", "acme", "set", "OPENROUTER_API_KEY"}, Stdin: "new value",
			Files: envWith("CCENV_ENV_KEYS=OPENROUTER_API_KEY\nOPENROUTER_API_KEY='old'\n"), Rules: nothingRunning},
			0, []string{"applies on: <tool> restart acme"}},
		{Scenario{Name: "env set, running", Args: []string{"env", "acme", "set", "NEW_KEY"}, Stdin: "v\nextra line\n", Files: twoOrgs, Rules: running("acme")},
			0, []string{"claude-acme recreated"}},
		{Scenario{Name: "env set, running, compose fails", Args: []string{"env", "acme", "set", "NEW_KEY"}, Stdin: "v\n", Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^compose `, Stderr: "boom\n", Exit: 1})}, 1, []string{"set: NEW_KEY"}},
		{Scenario{Name: "env set, --no-restart only as 4th arg", Args: []string{"env", "acme", "set", "NEW_KEY", "x", "--no-restart"}, Stdin: "v\n",
			Files: twoOrgs, Rules: running("acme")}, 0, []string{"recreated"}},
		{Scenario{Name: "env set, empty value", Args: []string{"env", "acme", "set", "NEW_KEY"}, Files: twoOrgs}, 1, []string{"<tool>: empty value; nothing set"}},
		{Scenario{Name: "env set, quote in value", Args: []string{"env", "acme", "set", "NEW_KEY"}, Stdin: "it's\n", Files: twoOrgs}, 1, []string{"single quote"}},
		{Scenario{Name: "env set, bad key", Args: []string{"env", "acme", "set", "lower_key"}, Stdin: "v\n", Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> env acme set KEY   (KEY like OPENROUTER_API_KEY)"}},
		{Scenario{Name: "env unset", Args: []string{"env", "acme", "unset", "A_KEY", "--no-restart"}, Rules: running("acme"),
			Files: envWith("CCENV_ENV_KEYS=A_KEY B_KEY\nA_KEY='1'\nB_KEY='2'\nA_KEY='dup'")}, 0, []string{"removed: A_KEY", "applies on"}},
		{Scenario{Name: "env rm, listed only", Args: []string{"env", "acme", "rm", "B_KEY"}, Rules: nothingRunning,
			Files: envWith("CCENV_ENV_KEYS=B_KEY\n")}, 0, []string{"removed: B_KEY"}},
		{Scenario{Name: "env unset, not set", Args: []string{"env", "acme", "unset", "C_KEY"}, Files: twoOrgs}, 1, []string{"<tool>: C_KEY is not set for acme"}},
		{Scenario{Name: "env, unknown subcommand", Args: []string{"env", "acme", "show"}, Files: twoOrgs}, 1, []string{"usage: <tool> env <org>"}},
		{Scenario{Name: "env, unknown org", Args: []string{"env", "nope"}, Files: twoOrgs}, 1, []string{"unknown org"}},

		// remote
		{Scenario{Name: "remote status, running", Args: []string{"remote", "acme"}, Files: twoOrgs,
			Rules: rcUp("starting\n    Capacity: 0/8\nnoise\n  Capacity: 2/8 sessions\n")}, 0, []string{"  Capacity: 2/8 sessions", "env_PARITY01"}},
		{Scenario{Name: "remote status, no capacity line", Args: []string{"remote", "acme", "status"}, Files: twoOrgs,
			Rules: rcUp("starting\n")}, 1, []string{"Remote Control: running"}},
		{Scenario{Name: "remote status, not logged in", Args: []string{"remote", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme test -f`, Exit: 1})}, 0, []string{"not logged in. Run: <tool> login acme"}},
		{Scenario{Name: "remote status, blocked", Args: []string{"remote", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme pgrep`, Exit: 1},
			Rule{Bin: "docker", Match: `^exec claude-acme sh -c tail`, Stdout: "blocked by organization policy\n"})}, 0, []string{"BLOCKED"}},
		{Scenario{Name: "remote status, restarting", Args: []string{"remote", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme pgrep`, Exit: 1})}, 0, []string{"restarting (see: <tool> remote acme logs)"}},
		{Scenario{Name: "remote logs", Args: []string{"remote", "acme", "logs"}, Files: twoOrgs, Rules: rcUp("line 1\nline 2\n")}, 0, []string{"line 2"}},
		{Scenario{Name: "remote restart", Args: []string{"remote", "acme", "restart"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme pkill`, Exit: 1})}, 0, []string{"Restarting; back in ~5s."}},
		{Scenario{Name: "remote, not running", Args: []string{"remote", "globex"}, Files: twoOrgs}, 1, []string{"not running"}},
		{Scenario{Name: "remote, unknown subcommand", Args: []string{"remote", "acme", "stop"}, Files: twoOrgs, Rules: running("acme")},
			1, []string{"usage: <tool> remote <org> [status|logs|restart]"}},
	}
	for i := range more {
		if more[i].Name == "password rotate, running" || more[i].Name == "password rotate, stopped" {
			o := "acme"
			if more[i].Name == "password rotate, stopped" {
				o = "globex"
			}
			more[i].Mask = []string{`pass: [A-Za-z0-9]{32}`}
			more[i].Random = map[string]string{"state/orgs/" + o + "/config/secrets/ttyd_credential": ttydRandom}
		}
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	for _, name := range []string{"init", "init bad name", "password show", "env set from stdin", "env set reserved"} {
		ported[name] = true
	}
}

// TestBerthInitOwnsTheOrg: an org berth creates is berth's (MANAGER=berth, first in org.env; the
// comparison above drops that line), so berth's writing commands accept it and ccenv refuses it.
func TestBerthInitOwnsTheOrg(t *testing.T) {
	raw := BerthUnowned(berthBin)
	r := run(t, raw, Scenario{Name: "init", Args: []string{"init", "t-new"}, Rules: nothingRunning, Random: newOrgRandom})
	if r.Exit != 0 {
		t.Fatalf("init: exit %d\n%s", r.Exit, r.Stderr)
	}
	found := false
	for _, l := range r.Tree {
		if strings.HasPrefix(l, "state/orgs/t-new/org.env 0600 ") {
			found = strings.HasPrefix(strings.TrimPrefix(l, "state/orgs/t-new/org.env 0600 "), `"MANAGER=berth\n# ---- Claude account`)
		}
	}
	if !found {
		t.Errorf("org.env doesn't start with MANAGER=berth:\n%s", strings.Join(r.Tree, "\n"))
	}
}

// TestBerthReservesItsComposeVariables: env set refuses the variables berth passes to compose for
// its image, on top of ccenv's reserved list (ccenv allows them; PARITY.md).
func TestBerthReservesItsComposeVariables(t *testing.T) {
	for _, key := range []string{"CLAUDE_ENV_IMAGE", "CLAUDE_ENV_IMAGE_DIR", "IMAGE_TAG"} {
		r := run(t, Berth(berthBin), Scenario{Args: []string{"env", "acme", "set", key}, Stdin: "x\n", Files: twoOrgs})
		if r.Exit != 1 || !strings.Contains(r.Stderr, key+" is managed by <tool>") {
			t.Errorf("%s: exit %d, stderr %q", key, r.Exit, r.Stderr)
		}
	}
}
