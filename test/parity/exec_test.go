package parity

import (
	"strings"
	"testing"
)

// TestBerthClaudeCwdAndExec: `claude <org> --cwd DIR --env K=V -- args` and `exec` (berth-only,
// PARITY.md) start in DIR inside the workspace, with K=V set after the org's own variables; bad
// options are refused before anything runs. `claude` without them is ccenv's (the parity
// scenarios).
func TestBerthClaudeCwdAndExec(t *testing.T) {
	r := run(t, Berth(berthBin), Scenario{Args: []string{"claude", "acme", "--cwd", "app/.worktrees/feat-12", "--env", "OTEL_RESOURCE_ATTRIBUTES=crew.card=12",
		"--env", "CREW_REPO=acme/app", "--", "--agent", "cuemby:engineer", "/build 12"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 {
		t.Fatalf("claude: exit %d\n%s", r.Exit, r.Stderr)
	}
	wantCall(t, r, `docker "exec" "-it" "-u" "node" "-w" "/workspace/app/.worktrees/feat-12" "claude-acme" "env" "OTEL_RESOURCE_ATTRIBUTES=crew.card=12" "CREW_REPO=acme/app" "claude" "--agent" "cuemby:engineer" "/build 12"`)

	r = run(t, Berth(berthBin), Scenario{Args: []string{"exec", "acme", "--cwd", "/workspace/app", "--", "crew", "start", "12", "--no-launch", "--json"},
		Files: twoOrgs, Rules: running("acme", Rule{Bin: "docker", Match: `^exec -i -u node`, Stdout: "{}\n", Exit: 3})})
	if r.Exit != 3 || r.Stdout != "{}\n" {
		t.Errorf("exec: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	wantCall(t, r, `docker "exec" "-i" "-u" "node" "-w" "/workspace/app" "claude-acme" "crew" "start" "12" "--no-launch" "--json"`)

	// A migrated org's secrets load first, then the --env values (which win).
	migrated := merge(twoOrgs, map[string]File{"state/orgs/acme/config/secrets/env": {Dir: true}})
	r = run(t, Berth(berthBin), Scenario{Args: []string{"exec", "acme", "--env", "K=v", "--", "true"}, Files: migrated, Rules: running("acme")})
	wantCall(t, r, `"claude-acme" "sh" "-c"`)
	wantCall(t, r, `"berth-secrets" "env" "K=v" "true"`)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"claude", "acme", "--cwd", "../../etc", "--", "x"}, "--cwd ../../etc: must be inside /workspace"},
		{[]string{"exec", "acme", "--cwd", "/workspace/../etc", "--", "ls"}, "must be inside /workspace"},
		{[]string{"exec", "acme", "--env", "lower=1", "--", "ls"}, `--env "lower=1": want KEY=VALUE`},
		{[]string{"exec", "acme", "--env", "NOEQUALS", "--", "ls"}, "want KEY=VALUE"},
		{[]string{"exec", "acme", "--cwd", "app", "ls"}, "unknown option ls (the command goes after --)"},
		{[]string{"exec", "acme", "--cwd", "app"}, "missing -- before the command"},
		{[]string{"exec", "acme", "--"}, "missing command"},
		{[]string{"claude", "acme", "--env"}, "--env needs a value"},
		{[]string{"exec", "globex", "--", "ls"}, "claude-globex is not running"},
	} {
		r = run(t, Berth(berthBin), Scenario{Args: c.args, Files: twoOrgs, Rules: running("acme")})
		if r.Exit == 0 || !strings.Contains(r.Stderr, c.want) {
			t.Errorf("%v: exit %d, stderr %q, want %q", c.args, r.Exit, r.Stderr, c.want)
		}
		for _, l := range r.Calls {
			if strings.Contains(l, `"exec"`) {
				t.Errorf("%v ran %s", c.args, l)
			}
		}
	}
}
