package parity

import (
	"slices"
	"strings"
	"testing"
)

// Secrets as files (#37): `berth secrets migrate` and what berth does for a migrated org. Orgs that
// aren't migrated are covered by the parity suite (unchanged).

const secretsEnvDir = "state/orgs/acme/config/secrets/env/"

// acmeWithSecrets is acme (berth's) with a token, a custom variable (quoted, as env set writes it)
// and an empty GH_TOKEN line.
var acmeWithSecrets = withFile(twoOrgs, "state/orgs/acme/org.env",
	File{Content: "MANAGER=berth\n" + acmeEnv + "CCENV_ENV_KEYS=OPENROUTER_API_KEY\nOPENROUTER_API_KEY='sk-or-FAKE'\n"})

// migratedAcme is acme after `secrets migrate`.
var migratedAcme = merge(withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: "MANAGER=berth\nGIT_USER_NAME=Test User\nSSH_PORT=2201\nTTYD_PORT=7701\nCCENV_ENV_KEYS=OPENROUTER_API_KEY\n"}),
	map[string]File{
		secretsEnvDir: {Dir: true},
		secretsEnvDir + "CLAUDE_CODE_OAUTH_TOKEN": {Content: "sk-ant-oat01-FILE"},
		secretsEnvDir + "OPENROUTER_API_KEY":      {Content: "sk-or-FILE"},
	})

func treeLine(r Result, prefix string) string {
	for _, l := range r.Tree {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestBerthSecretsMigrate(t *testing.T) {
	raw := BerthUnowned(berthBin)
	r := run(t, raw, Scenario{Args: []string{"secrets", "migrate", "acme", "--no-backup"}, Files: acmeWithSecrets, Rules: running("acme")})
	if r.Exit != 0 || len(r.Calls) != 0 {
		t.Fatalf("exit %d, calls %v (no docker call: nothing restarts), stderr %q", r.Exit, r.Calls, r.Stderr)
	}
	for _, want := range []string{
		secretsEnvDir[:len(secretsEnvDir)-1] + "/ 0700",
		secretsEnvDir + `CLAUDE_CODE_OAUTH_TOKEN 0600 "sk-ant-oat01-FAKE-PARITY-TOKEN"`,
		secretsEnvDir + `OPENROUTER_API_KEY 0600 "sk-or-FAKE"`, // unquoted, as compose saw it
	} {
		if !slices.Contains(r.Tree, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(r.Tree, "\n"))
		}
	}
	env := treeLine(r, "state/orgs/acme/org.env ")
	for _, gone := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENROUTER_API_KEY=", "GH_TOKEN"} {
		if strings.Contains(env, gone) {
			t.Errorf("org.env still has %s: %s", gone, env)
		}
	}
	if !strings.Contains(env, `CCENV_ENV_KEYS=OPENROUTER_API_KEY\n`) || !strings.HasPrefix(env, "state/orgs/acme/org.env 0600 ") {
		t.Errorf("org.env must keep the names list and its mode: %s", env)
	}
	if treeLine(r, secretsEnvDir+"GH_TOKEN") != "" {
		t.Error("an empty GH_TOKEN= line must not become a file")
	}
	if !strings.Contains(r.Stdout, "Nothing was restarted") {
		t.Errorf("stdout: %q", r.Stdout)
	}

	// Again: nothing to move.
	r = run(t, raw, Scenario{Args: []string{"secrets", "migrate", "acme", "--no-backup"}, Files: migratedAcme})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "nothing to move") {
		t.Errorf("second migrate: exit %d, stdout %q", r.Exit, r.Stdout)
	}

	// With the backup (the default): it runs first, and when it fails nothing is moved.
	r = run(t, raw, Scenario{Args: []string{"secrets", "migrate", "acme"}, Files: merge(withKey, acmeWithSecrets), Rules: archiveRules()})
	if r.Exit != 0 || !strings.Contains(r.Stderr, "Wrote <RUN>/state/backups/acme-") || treeLine(r, secretsEnvDir+"CLAUDE_CODE_OAUTH_TOKEN") == "" {
		t.Errorf("migrate with backup: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"secrets", "migrate", "acme"}, Files: merge(withKey, acmeWithSecrets),
		Rules: archiveRules(Rule{Bin: "docker", Match: `/archive\.sh create$`, Exit: 1})})
	if r.Exit == 0 || !strings.Contains(r.Stderr, "the backup failed, so nothing was moved") || treeLine(r, secretsEnvDir) != "" {
		t.Errorf("failed backup: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// A ccenv org, and --read-only, are refused before anything happens.
	if r = run(t, raw, Scenario{Args: []string{"secrets", "migrate", "globex", "--no-backup"}, Files: twoOrgs}); r.Exit != 1 || !strings.Contains(r.Stderr, "managed by ccenv") {
		t.Errorf("ccenv org: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}

func TestBerthSecretsMigratedOrg(t *testing.T) {
	raw := BerthUnowned(berthBin)

	r := run(t, raw, Scenario{Args: []string{"--output", "json", "ls"}, Files: migratedAcme})
	if !strings.Contains(r.Stdout, `"name": "acme",`) || !strings.Contains(r.Stdout, `"token": true`) {
		t.Errorf("ls must see the token in its file: %s", r.Stdout)
	}
	r = run(t, raw, Scenario{Args: []string{"env", "acme", "ls"}, Files: migratedAcme})
	if r.Stdout != "OPENROUTER_API_KEY\n" {
		t.Errorf("env ls: %q", r.Stdout)
	}

	// env set/unset write and remove files; org.env only keeps the names.
	r = run(t, raw, Scenario{Args: []string{"env", "acme", "set", "NEW_KEY", "--no-restart"}, Stdin: "v1\n", Files: migratedAcme})
	if r.Exit != 0 || !slices.Contains(r.Tree, secretsEnvDir+`NEW_KEY 0600 "v1"`) || strings.Contains(treeLine(r, "state/orgs/acme/org.env "), "NEW_KEY=") ||
		!strings.Contains(treeLine(r, "state/orgs/acme/org.env "), `CCENV_ENV_KEYS=OPENROUTER_API_KEY NEW_KEY\n`) {
		t.Errorf("env set: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Tree, "\n"))
	}
	r = run(t, raw, Scenario{Args: []string{"env", "acme", "unset", "OPENROUTER_API_KEY", "--no-restart"}, Files: migratedAcme})
	if r.Exit != 0 || treeLine(r, secretsEnvDir+"OPENROUTER_API_KEY") != "" || strings.Contains(treeLine(r, "state/orgs/acme/org.env "), "OPENROUTER_API_KEY") {
		t.Errorf("env unset: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// The token: saved to its file, and removed by logout --all.
	r = run(t, raw, Scenario{Args: []string{"token", "acme", "--paste", "--no-restart"}, Stdin: "sk-ant-oat01-NEW\n", Files: migratedAcme})
	if r.Exit != 0 || !slices.Contains(r.Tree, secretsEnvDir+`CLAUDE_CODE_OAUTH_TOKEN 0600 "sk-ant-oat01-NEW"`) ||
		strings.Contains(treeLine(r, "state/orgs/acme/org.env "), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("token: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"logout", "acme", "--all"}, Files: migratedAcme})
	if r.Exit != 0 || treeLine(r, secretsEnvDir+"CLAUDE_CODE_OAUTH_TOKEN") != "" {
		t.Errorf("logout --all: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// claude and run get the files through the inline loader (any image), after docker exec's flags.
	r = run(t, raw, Scenario{Args: []string{"run", "acme", "hi"}, Files: migratedAcme, Rules: running("acme")})
	found := false
	for _, c := range r.Calls {
		if strings.HasPrefix(c, `docker "exec" "-i" "-u" "node" "-w" "/workspace" "claude-acme" "sh" "-c" "for f in /config/secrets/env/*;`) &&
			strings.HasSuffix(c, `"berth-secrets" "claude" "-p" "hi"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("run: no loader in the exec call:\n%s", strings.Join(r.Calls, "\n"))
	}
}
