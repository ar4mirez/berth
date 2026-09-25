package parity

// Phase 3b: repo add (and clone), rm|remove, adopt, sync, policy.

// gitRepo is a minimal git repository at dir (relative to the run dir), with origin as its remote
// ("" for none): enough for the host's `git remote get-url` and `git rev-parse --git-dir`.
func gitRepo(dir, origin string) map[string]File {
	config := "[core]\n\trepositoryformatversion = 0\n"
	if origin != "" {
		config += "[remote \"origin\"]\n\turl = " + origin + "\n"
	}
	return map[string]File{
		dir:                   {Dir: true, Mode: 0o755},
		dir + "/.git":         {Dir: true, Mode: 0o755},
		dir + "/.git/HEAD":    {Content: "ref: refs/heads/main\n", Mode: 0o644},
		dir + "/.git/config":  {Content: config, Mode: 0o644},
		dir + "/.git/objects": {Dir: true, Mode: 0o755},
		dir + "/.git/refs":    {Dir: true, Mode: 0o755},
	}
}

const acmeWS, acmeQ = "state/orgs/acme/workspace/", "state/orgs/acme/quarantine/"

// adoptOrg is repoOrg with folders to adopt: repos with and without a remote, a remote with no
// canonical form, a folder that isn't a repo, a registered repo, and two quarantined copies of one.
var adoptOrg = merge(repoOrg,
	gitRepo(acmeWS+"widget", "https://GitHub.com/Acme/Widget.git"),
	gitRepo(acmeWS+"scratchpad", ""),
	gitRepo(acmeWS+"weird", "ftp://files.example.com/x"),
	gitRepo(acmeWS+"app", "git@github.com:acme/app.git"),
	gitRepo(acmeQ+"gone.20260101-0000", "git@github.com:acme/gone-old.git"),
	gitRepo(acmeQ+"gone.20260201-0000", "git@github.com:acme/gone.git"),
	map[string]File{
		acmeWS + "plain":             {Dir: true, Mode: 0o755},
		acmeQ + "junk.20260101-0000": {Dir: true, Mode: 0o755},
	})

// cloneRules script acme running, with /workspace/<dir> missing and the clone's exit code.
func cloneRules(dir string, exit int) []Rule {
	return running("acme",
		Rule{Bin: "docker", Match: `^exec claude-acme test -e /workspace/` + dir + `$`, Exit: 1},
		Rule{Bin: "docker", Match: `^exec -u node -w /workspace claude-acme git clone `, Stderr: "Cloning...\n", Exit: exit})
}

func init() {
	acmeFile := func(content string) map[string]File {
		return withFile(repoOrg, "state/orgs/acme/config/repos.txt", File{Content: content, Mode: 0o644})
	}
	more := []checked{
		// add
		{Scenario{Name: "repo add, running", Args: []string{"repo", "add", "acme", "acme/widget"}, Files: twoOrgs, Rules: cloneRules("widget", 0)},
			0, []string{"Registered github.com/acme/widget as /workspace/widget", "Cloned into /workspace/widget"}},
		{Scenario{Name: "repo add, stopped", Args: []string{"repo", "add", "globex", "acme/widget", "--branch", "dev", "--dir", "w"}, Files: twoOrgs},
			0, []string{"Registered github.com/acme/widget as /workspace/w", "(container stopped; it will be cloned by: <tool> repo sync globex)"}},
		{Scenario{Name: "repo add --no-clone, URL", Args: []string{"repo", "add", "globex", "-b", "main", "https://GitLab.com/Globex/Tool.git", "--no-clone"}, Files: twoOrgs},
			0, []string{"Registered gitlab.com/globex/tool as /workspace/tool"}},
		// A spec already in host/owner/repo form goes into the clone URL as written.
		{Scenario{Name: "repo add, host/owner/repo with capitals", Args: []string{"repo", "add", "globex", "github.com/Acme/Widget.git"}, Files: twoOrgs},
			0, []string{"Registered github.com/acme/widget as /workspace/widget"}},
		{Scenario{Name: "repo add, no repos.txt yet", Args: []string{"repo", "add", "globex", "acme/widget"},
			Files: withoutFile(twoOrgs, "state/orgs/globex/config/repos.txt")}, 0, []string{"Registered"}},
		{Scenario{Name: "repo add, already present", Args: []string{"repo", "add", "acme", "acme/widget"}, Files: twoOrgs, Rules: running("acme")},
			0, []string{"/workspace/widget already present"}},
		{Scenario{Name: "repo add, already registered", Args: []string{"repo", "add", "acme", "git@github.com:acme/app.git"}, Files: repoOrg, Rules: cloneRules("app", 0)},
			0, []string{"github.com/acme/app is already registered as /workspace/app", "Cloned into"}},
		{Scenario{Name: "repo add, dir taken", Args: []string{"repo", "add", "acme", "initech/app"}, Files: repoOrg},
			1, []string{"<tool>: /workspace/app is already registered for github.com/acme/app"}},
		{Scenario{Name: "repo add, dir registered with no canonical form", Args: []string{"repo", "add", "acme", "acme/odd"}, Files: repoOrg},
			1, []string{"/workspace/odd is already registered for -"}},
		{Scenario{Name: "repo add, dir registered twice", Args: []string{"repo", "add", "acme", "acme/app", "--no-clone"},
			Files: acmeFile("app git@github.com:acme/app.git\napp git@github.com:acme/app.git\n")}, 1, []string{"already registered for github.com/acme/app"}},
		{Scenario{Name: "repo add, can't parse", Args: []string{"repo", "add", "globex", "ftp://example.com/a/b"}, Files: twoOrgs},
			1, []string{"<tool>: can't parse repo 'ftp://example.com/a/b'"}},
		{Scenario{Name: "repo add, one segment", Args: []string{"repo", "add", "globex", "widget"}, Files: twoOrgs}, 1, []string{"can't parse repo 'widget'"}},
		{Scenario{Name: "repo add, invalid dir", Args: []string{"repo", "add", "globex", "acme/widget", "--dir", ".claude"}, Files: twoOrgs},
			1, []string{"<tool>: invalid dir name '.claude'"}},
		{Scenario{Name: "repo add, dir with a slash", Args: []string{"repo", "add", "globex", "acme/widget", "--dir", "a/b"}, Files: twoOrgs},
			1, []string{"invalid dir name 'a/b'"}},
		{Scenario{Name: "repo add, unknown flag", Args: []string{"repo", "add", "globex", "acme/widget", "--frob"}, Files: twoOrgs},
			1, []string{"<tool>: unknown flag --frob"}},
		{Scenario{Name: "repo add, no spec", Args: []string{"repo", "add", "globex", "--no-clone"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> repo add <org> <owner/repo|git-url> [--dir name] [--branch b] [--no-clone]"}},
		// The rollback is grep -v "^$dir ": the '.' in my.app also matches the myXapp line.
		{Scenario{Name: "repo add, clone fails, rolled back", Args: []string{"repo", "add", "acme", "acme/my.app"},
			Files: acmeFile("# header\nmyXapp git@github.com:acme/x.git\nkeep git@github.com:acme/keep.git"), Rules: cloneRules(`my\.app`, 128)},
			1, []string{"Registered", "<tool>: clone failed; registration rolled back. Does this org's key have access? (<tool> info acme)"}},
		{Scenario{Name: "repo add, clone fails, already registered", Args: []string{"repo", "add", "acme", "acme/app"}, Files: repoOrg, Rules: cloneRules("app", 128)},
			1, []string{"clone failed"}},
		// Legacy quirk: when the new line is all repos.txt holds, grep -v selects nothing and set -e
		// ends the command (exit 1, no message, the registration kept).
		{Scenario{Name: "repo add, clone fails, only line", Args: []string{"repo", "add", "acme", "acme/widget"}, Files: acmeFile(""), Rules: cloneRules("widget", 128)},
			1, []string{"Registered"}},
		{Scenario{Name: "repo add, missing org", Args: []string{"repo", "add"}, Files: twoOrgs}, 1, []string{"missing <org>"}},
		{Scenario{Name: "clone", Args: []string{"clone", "globex", "acme/widget", "--dir", "w2"}, Files: twoOrgs}, 0, []string{"Registered github.com/acme/widget as /workspace/w2"}},
		{Scenario{Name: "clone, missing org", Args: []string{"clone"}, Files: twoOrgs}, 1, []string{"missing <org>"}},

		// rm|remove
		{Scenario{Name: "repo rm, stopped", Args: []string{"repo", "rm", "acme", "api"}, Files: repoOrg}, 0, []string{"Unregistered /workspace/api"}},
		{Scenario{Name: "repo remove, running", Args: []string{"repo", "remove", "acme", "last"}, Files: repoOrg, Rules: running("acme")},
			0, []string{"Its folder will be moved to orgs/acme/quarantine/ by the workspace sweep (within 30s)."}},
		{Scenario{Name: "repo rm --delete, running", Args: []string{"repo", "rm", "acme", "app", "--delete"}, Files: repoOrg, Rules: running("acme")},
			0, []string{"Deleted /workspace/app"}},
		{Scenario{Name: "repo rm --delete, stopped", Args: []string{"repo", "rm", "acme", "app", "--delete"}, Files: repoOrg}, 0, []string{"Unregistered"}},
		{Scenario{Name: "repo rm --delete, rm fails", Args: []string{"repo", "rm", "acme", "app", "--delete"}, Files: repoOrg, Rules: running("acme",
			Rule{Bin: "docker", Match: `rm -rf /workspace/app$`, Stderr: "busy\n", Exit: 7})}, 7, []string{"busy"}},
		{Scenario{Name: "repo rm, leading blanks and duplicates", Args: []string{"repo", "rm", "acme", "app"},
			Files: acmeFile("# c\n  app git@github.com:acme/app.git\n\napp\tgit@github.com:acme/app2.git\napplet git@github.com:acme/applet.git")}, 0, []string{"Unregistered"}},
		{Scenario{Name: "repo rm, not registered", Args: []string{"repo", "rm", "acme", "stray"}, Files: repoOrg}, 1, []string{"<tool>: /workspace/stray is not registered"}},
		{Scenario{Name: "repo rm, no dir", Args: []string{"repo", "rm", "acme"}, Files: repoOrg}, 1, []string{"<tool>: usage: <tool> repo rm <org> <dir> [--delete]"}},

		// adopt. repoOrg's repos.txt has no final newline, so the first append is glued onto its last
		// line (printf >>, as in fw allow): widget isn't registered after all, and is again at the end.
		{Scenario{Name: "repo adopt", Args: []string{"repo", "adopt", "acme", "widget", "scratchpad", "weird", "plain", "app", "gone", "junk", "widget"}, Files: adoptOrg},
			0, []string{"Registered widget -> github.com/acme/widget", "Registered scratchpad -> local only", "Registered weird -> ",
				"skip plain: not a git repo", "app already registered", "Restored gone from quarantine and registered it (git@github.com:acme/gone.git)",
				"skip junk: not a git repo"}},
		{Scenario{Name: "repo adopt --all", Args: []string{"repo", "adopt", "acme", "--all"}, Files: adoptOrg}, 0, []string{"Registered widget", "skip Zeta: not a git repo"}},
		// Legacy quirk: a name in neither the workspace nor quarantine ends the command with exit 2
		// (the quarantine lookup fails under pipefail), before its "skip" message.
		{Scenario{Name: "repo adopt, nowhere", Args: []string{"repo", "adopt", "acme", "widget", "nothing", "scratchpad"}, Files: adoptOrg}, 2, []string{"Registered widget"}},
		{Scenario{Name: "repo adopt, no names", Args: []string{"repo", "adopt", "acme"}, Files: adoptOrg},
			1, []string{"<tool>: usage: <tool> repo adopt <org> <dir>...   (or --all for everything unregistered)"}},

		// sync
		{Scenario{Name: "repo sync", Args: []string{"repo", "sync", "acme"}, Files: repoOrg, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme test -e /workspace/(app|api|notes|odd)$`, Exit: 1},
			Rule{Bin: "docker", Match: `git clone --branch develop `, Stderr: "fatal: no such branch\n", Exit: 128})},
			0, []string{"Cloned app", "FAILED api", "SKIP notes: local-only repo is missing (restore it from a backup)", "Cloned odd"}},
		{Scenario{Name: "repo sync, all present", Args: []string{"repo", "sync", "acme"}, Files: repoOrg, Rules: running("acme")},
			0, []string{"All registered repos are present."}},
		{Scenario{Name: "repo sync, only a local repo missing", Args: []string{"repo", "sync", "acme"}, Files: repoOrg, Rules: running("acme",
			Rule{Bin: "docker", Match: `test -e /workspace/notes$`, Exit: 1})}, 0, []string{"SKIP notes", "All registered repos are present."}},
		{Scenario{Name: "repo sync, not running", Args: []string{"repo", "sync", "globex"}, Files: twoOrgs}, 1, []string{"not running"}},

		// policy
		{Scenario{Name: "repo policy", Args: []string{"repo", "policy", "acme"}, Files: twoOrgs}, 0, []string{"REPO_POLICY=enforce"}},
		{Scenario{Name: "repo policy, not set", Args: []string{"repo", "policy", "globex"}, Files: twoOrgs}, 0, []string{"REPO_POLICY="}},
		{Scenario{Name: "repo policy set, running", Args: []string{"repo", "policy", "acme", "off"}, Files: twoOrgs, Rules: running("acme")},
			0, []string{"REPO_POLICY=off", "Restarted claude-acme to apply it."}},
		{Scenario{Name: "repo policy set, restart fails", Args: []string{"repo", "policy", "acme", "warn"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^compose `, Stderr: "nope\n", Exit: 3})}, 3, []string{"REPO_POLICY=warn"}},
		{Scenario{Name: "repo policy, bad mode", Args: []string{"repo", "policy", "acme", "strict"}, Files: twoOrgs},
			1, []string{"<tool>: policy must be enforce|warn|off"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
	ported["repo policy set, stopped"] = true
}
