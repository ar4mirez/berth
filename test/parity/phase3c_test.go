package parity

// Phase 3c: repo new|create (on GitHub, or --local) and repo publish.

// gitRepoWithCommit is gitRepo with a main branch. The commit object itself isn't there, which
// `git rev-parse --verify HEAD` doesn't check.
func gitRepoWithCommit(dir, origin string) map[string]File {
	return merge(gitRepo(dir, origin), map[string]File{
		dir + "/.git/refs/heads":      {Dir: true, Mode: 0o755},
		dir + "/.git/refs/heads/main": {Content: "1111111111111111111111111111111111111111\n", Mode: 0o644},
	})
}

const globexWS = "state/orgs/globex/workspace/"

// publishOrg is globex with local-only repos: notes has a commit and the origin gh would have added
// (gh is faked); empty has no commits; bare has a commit but no origin.
var publishOrg = merge(twoOrgs,
	gitRepoWithCommit(globexWS+"notes", "git@github.com:acme/notes.git"),
	gitRepo(globexWS+"empty", ""),
	gitRepoWithCommit(globexWS+"bare", ""),
	map[string]File{"state/orgs/globex/config/repos.txt": {Mode: 0o644, Content: "# h\napp git@github.com:acme/app.git\n  notes\tlocal  dev\nempty local\nbare local\ngone local\ntwice local\ntwice local"}})

var noHostGh = Rule{Bin: "gh", Match: `^auth status$`, Stderr: "not logged in\n", Exit: 1}

func init() {
	more := []checked{
		// new|create on GitHub
		{Scenario{Name: "repo new", Args: []string{"repo", "new", "globex", "acme/widget"}, Files: twoOrgs},
			0, []string{"Created https://github.com/acme/widget (private)", "Registered github.com/acme/widget as /workspace/widget", "(container stopped"}},
		{Scenario{Name: "repo create, every option", Args: []string{"repo", "create", "acme", "GitHub.com/Acme/Widget", "--public", "-d", "A widget",
			"--template", "acme/tpl", "--readme", "-g", "Go", "-l", "mit", "--dir", "w"}, Files: twoOrgs, Rules: cloneRules("w", 0)},
			0, []string{"Created https://github.com/acme/widget (public)", "Cloned into /workspace/w"}},
		{Scenario{Name: "repo new, internal, long flags", Args: []string{"repo", "new", "globex", "--internal", "--description", "d", "-p", "acme/tpl",
			"--add-readme", "--gitignore", "Node", "--license", "apache-2.0", "initech/tool"}, Files: twoOrgs}, 0, []string{"(internal)"}},
		{Scenario{Name: "repo new, container gh", Args: []string{"repo", "new", "acme", "acme/widget", "--dir", "w"}, Files: twoOrgs,
			Rules: append([]Rule{noHostGh}, cloneRules("w", 0)...)}, 0, []string{"Created https://github.com/acme/widget (private)", "Cloned into"}},
		{Scenario{Name: "repo new, no host gh, stopped", Args: []string{"repo", "new", "globex", "acme/widget"}, Files: twoOrgs, Rules: []Rule{noHostGh}},
			1, []string{"<tool>: claude-globex is not running (<tool> up globex)"}},
		{Scenario{Name: "repo new, GitHub refuses", Args: []string{"repo", "new", "globex", "acme/widget"}, Files: twoOrgs,
			Rules: []Rule{{Bin: "gh", Match: `^repo create `, Stdout: "ignored\n", Stderr: "GraphQL: Name already exists\n", Exit: 1}}},
			1, []string{"GraphQL: Name already exists", "<tool>: GitHub refused to create acme/widget (name taken, or no permission in that org?)"}},
		{Scenario{Name: "repo new, container gh fails", Args: []string{"repo", "new", "acme", "acme/widget"}, Files: twoOrgs,
			Rules: append([]Rule{noHostGh}, running("acme", Rule{Bin: "docker", Match: `gh repo create`, Stderr: "auth required\n", Exit: 4})...)},
			1, []string{"<tool>: couldn't create acme/widget: no host gh, and the container's gh failed (<tool> gh-login acme?)"}},
		{Scenario{Name: "repo new, not GitHub", Args: []string{"repo", "new", "globex", "gitlab.com/globex/tool"}, Files: twoOrgs},
			1, []string{"<tool>: repo new creates GitHub repos: use <owner/repo> (got 'gitlab.com/globex/tool'). For a repo with no remote use --local."}},
		{Scenario{Name: "repo new, nested path", Args: []string{"repo", "new", "globex", "acme/team/tool"}, Files: twoOrgs}, 1, []string{"repo new creates GitHub repos"}},
		{Scenario{Name: "repo new, already registered", Args: []string{"repo", "new", "globex", "git@github.com:acme/app.git"}, Files: twoOrgs},
			1, []string{"<tool>: github.com/acme/app is already registered"}},
		{Scenario{Name: "repo new, dir registered", Args: []string{"repo", "new", "globex", "initech/app"}, Files: twoOrgs},
			1, []string{"<tool>: /workspace/app is already registered"}},
		// The dir is only checked by repo add, after the GitHub repo was created.
		{Scenario{Name: "repo new, bad dir", Args: []string{"repo", "new", "globex", "acme/widget", "--dir", "a/b"}, Files: twoOrgs},
			1, []string{"Created https://github.com/acme/widget", "<tool>: invalid dir name 'a/b'"}},
		{Scenario{Name: "repo new, no spec", Args: []string{"repo", "new", "globex", "--public"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> repo new <org> <owner/repo> [--private|--public|--internal]", "<tool> repo new <org> <name> --local"}},
		{Scenario{Name: "repo new, unknown flag", Args: []string{"repo", "new", "globex", "acme/widget", "--frob"}, Files: twoOrgs},
			1, []string{"<tool>: unknown flag --frob"}},

		// new --local
		{Scenario{Name: "repo new --local", Args: []string{"repo", "new", "acme", "initech/scratch", "--local"}, Files: twoOrgs,
			Rules: running("acme", Rule{Bin: "docker", Match: `^exec claude-acme test -e /workspace/scratch$`, Exit: 1})},
			0, []string{"Created local repo /workspace/scratch (no remote). Publish it later with: <tool> repo publish acme scratch <owner/repo>"}},
		{Scenario{Name: "repo new --local, exists", Args: []string{"repo", "new", "acme", "stray", "--local"}, Files: twoOrgs, Rules: running("acme")},
			1, []string{"<tool>: /workspace/stray already exists (register it with: <tool> repo adopt acme stray)"}},
		{Scenario{Name: "repo new --local, stopped", Args: []string{"repo", "new", "globex", "scratch", "--local"}, Files: twoOrgs}, 1, []string{"not running"}},
		{Scenario{Name: "repo new --local, registered", Args: []string{"repo", "new", "acme", "--local", "app"}, Files: twoOrgs, Rules: running("acme")},
			1, []string{"<tool>: /workspace/app is already registered"}},
		{Scenario{Name: "repo new --local, invalid dir", Args: []string{"repo", "new", "acme", "x", "--local", "--dir", ".claude"}, Files: twoOrgs},
			1, []string{"invalid dir name '.claude'"}},
		{Scenario{Name: "repo new --local, git init fails", Args: []string{"repo", "new", "acme", "scratch", "--local"}, Files: twoOrgs,
			Rules: running("acme", Rule{Bin: "docker", Match: `test -e`, Exit: 1}, Rule{Bin: "docker", Match: `git init`, Stderr: "read-only\n", Exit: 5})}, 5, []string{"read-only"}},

		// publish
		{Scenario{Name: "repo publish", Args: []string{"repo", "publish", "globex", "notes", "acme/notes", "--public"}, Files: publishOrg},
			0, []string{"Published /workspace/notes to https://github.com/acme/notes (public); it now pushes/pulls from there."}},
		{Scenario{Name: "repo publish, not local", Args: []string{"repo", "publish", "globex", "app", "acme/app"}, Files: publishOrg},
			1, []string{"<tool>: /workspace/app is not a local-only repo"}},
		{Scenario{Name: "repo publish, registered twice", Args: []string{"repo", "publish", "globex", "twice", "acme/twice"}, Files: publishOrg},
			1, []string{"not a local-only repo"}},
		// shift 2 fails with one argument, so the flag loop sees the dir.
		{Scenario{Name: "repo publish, one argument", Args: []string{"repo", "publish", "globex", "notes"}, Files: publishOrg}, 1, []string{"<tool>: unknown flag notes"}},
		{Scenario{Name: "repo publish, no arguments", Args: []string{"repo", "publish", "globex"}, Files: publishOrg},
			1, []string{"<tool>: usage: <tool> repo publish <org> <dir> <owner/repo> [--private|--public|--internal]"}},
		{Scenario{Name: "repo publish, unknown flag", Args: []string{"repo", "publish", "globex", "notes", "acme/notes", "--public", "--frob"}, Files: publishOrg},
			1, []string{"unknown flag --frob"}},
		{Scenario{Name: "repo publish, no host gh", Args: []string{"repo", "publish", "globex", "notes", "acme/notes"}, Files: publishOrg, Rules: []Rule{noHostGh}},
			1, []string{"<tool>: publish needs gh signed in on the host"}},
		{Scenario{Name: "repo publish, no commits", Args: []string{"repo", "publish", "globex", "empty", "acme/empty"}, Files: publishOrg},
			1, []string{"<tool>: /workspace/empty has no commits yet; commit something first"}},
		{Scenario{Name: "repo publish, folder missing", Args: []string{"repo", "publish", "globex", "gone", "acme/gone"}, Files: publishOrg},
			1, []string{"has no commits yet"}},
		{Scenario{Name: "repo publish, gh fails", Args: []string{"repo", "publish", "globex", "notes", "acme/notes"}, Files: publishOrg,
			Rules: []Rule{{Bin: "gh", Match: `^repo create `, Stderr: "HTTP 422\n", Exit: 1}}}, 1, []string{"HTTP 422", "<tool>: publishing failed (name taken, or no permission?)"}},
		{Scenario{Name: "repo publish, no origin", Args: []string{"repo", "publish", "globex", "bare", "acme/bare"}, Files: publishOrg}, 2, []string{"origin"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
}
