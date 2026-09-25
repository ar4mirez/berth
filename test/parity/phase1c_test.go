package parity

// Phase 1c: repo ls|list and repo audit.

const acmeRepos = `# Repos allowed in the 'acme' container.
app git@github.com:acme/app.git
api	git@github.com:acme/api.git	develop
notes local
odd ssh://github.com:2222/acme/odd
tool https://gitlab.com/globex/tool -
last git@github.com:initech/last.git`

// repoOrg is acme with a busier workspace: registered and unregistered folders, hidden entries, a
// broken symlink, and a quarantine with a visible and a hidden entry.
var repoOrg = merge(twoOrgs, map[string]File{
	"state/orgs/acme/config/repos.txt":             {Content: acmeRepos, Mode: 0o644},
	"state/orgs/acme/workspace/notes":              {Dir: true, Mode: 0o755},
	"state/orgs/acme/workspace/tool":               {Dir: true, Mode: 0o755},
	"state/orgs/acme/workspace/tool/.git":          {Dir: true, Mode: 0o755},
	"state/orgs/acme/workspace/.claude":            {Dir: true, Mode: 0o755},
	"state/orgs/acme/workspace/.scratch":           {Dir: true, Mode: 0o755},
	"state/orgs/acme/workspace/..weird":            {Dir: true, Mode: 0o755},
	"state/orgs/acme/workspace/Zeta":               {Content: "x", Mode: 0o644},
	"state/orgs/acme/workspace/dangling":           {Link: "does-not-exist"},
	"state/orgs/acme/quarantine":                   {Dir: true},
	"state/orgs/acme/quarantine/old.20260101-0000": {Dir: true, Mode: 0o755},
	"state/orgs/acme/quarantine/.hidden":           {Dir: true, Mode: 0o755},
})

var acmeRepoRunning = []Rule{
	{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\n"},
	{Bin: "docker", Match: `^exec claude-acme test -d /workspace/app/\.git$`},
	{Bin: "docker", Match: `^exec -u node -w /workspace/app claude-acme sh -c`, Stdout: "main 2"},
	{Bin: "docker", Match: `^exec claude-acme test -d /workspace/tool/\.git$`},
	{Bin: "docker", Match: `^exec -u node -w /workspace/tool claude-acme sh -c`, Stdout: "\n", Exit: 1}, // → "? ?" on the next line
	{Bin: "docker", Match: `^exec claude-acme test -d `, Exit: 1, Stderr: "(not cloned)\n"},
}

func init() {
	more := []checked{
		{Scenario{Name: "repo ls, running", Args: []string{"repo", "ls", "acme"}, Files: repoOrg, Rules: acmeRepoRunning},
			0, []string{"cloned, 2 changed", "(local only, not published)", "MISSING (<tool> repo sync acme)", "Not allowed in /workspace (policy: enforce): Zeta stray .scratch", "old.20260101-0000"}},
		{Scenario{Name: "repo list, stopped", Args: []string{"repo", "list", "acme"}, Files: repoOrg, Rules: nothingRunning},
			0, []string{"cloned (container down)", "develop"}},
		{Scenario{Name: "repo ls, no repos.txt yet", Args: []string{"repo", "ls", "globex"}, Rules: nothingRunning,
			Files: withoutFile(withQuarantine, "state/orgs/globex/config/repos.txt")}, 0, []string{"(no repos registered; add one: <tool> repo add globex <owner/repo>)"}},
		{Scenario{Name: "repo audit, clean", Args: []string{"repo", "audit", "globex"}, Rules: nothingRunning,
			Files: withoutFile(withFile(withQuarantine, "state/orgs/globex/quarantine/old.20260101-000000", File{Dir: true}), "state/orgs/globex/workspace/stray")},
			0, []string{"Workspace clean: only registered repos."}},
		{Scenario{Name: "repo audit --quiet, clean", Args: []string{"repo", "audit", "globex", "--quiet"}, Rules: nothingRunning,
			Files: withoutFile(withQuarantine, "state/orgs/globex/workspace/stray")}, 0, []string{"Quarantined"}},
		// REPO_POLICY as shown by `envval | sed 's/^$/enforce/'`: empty → enforce, missing → nothing.
		{Scenario{Name: "repo audit, policy missing", Args: []string{"repo", "audit", "globex"}, Files: withQuarantine}, 0, []string{"(policy: ): stray"}},
		{Scenario{Name: "repo audit, policy empty", Args: []string{"repo", "audit", "globex"},
			Files: withFile(withQuarantine, "state/orgs/globex/org.env", File{Content: globexEnv + "REPO_POLICY=\n"})}, 0, []string{"(policy: enforce)"}},
		{Scenario{Name: "repo audit, policy warn", Args: []string{"repo", "audit", "globex"},
			Files: withFile(withQuarantine, "state/orgs/globex/org.env", File{Content: globexEnv + "REPO_POLICY=warn\n"})}, 0, []string{"(policy: warn)"}},
		{Scenario{Name: "repo audit, never started", Args: []string{"repo", "audit", "globex"}, Files: twoOrgs}, 2, nil},
		{Scenario{Name: "repo, unknown subcommand", Args: []string{"repo", "frob", "acme"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> repo add|new|publish|ls|rm|adopt|sync|audit|policy <org> ..."}},
		{Scenario{Name: "repo ls, missing org", Args: []string{"repo", "ls"}}, 1, []string{"missing <org>"}},
		{Scenario{Name: "repo ls, unknown org", Args: []string{"repo", "ls", "nope"}, Files: twoOrgs}, 1, []string{"unknown org 'nope'"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	for _, name := range []string{"repo ls, stopped", "repo ls, never started"} {
		ported[name] = true
	}
	readOnlySkipsWrites["repo ls, no repos.txt yet"] = true
}
