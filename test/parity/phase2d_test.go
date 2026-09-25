package parity

// Phase 2d: token, auth, login, logout, gh-login.

const authStatusAcme = `{"loggedIn":true,"orgId":"org-acme","email":"dev@example.com","orgName":"Acme Corp"}`

// tokenCapture scripts `claude setup-token` in the container leaving tok in /dev/shm ("" = nothing).
func tokenCapture(tok string) []Rule {
	return []Rule{
		{Bin: "docker", Match: `^exec -it -u node -w /workspace claude-acme bash -c`},
		{Bin: "docker", Match: `^exec claude-acme sh -c cat /dev/shm/\.ccenv-token`, Stdout: tok},
	}
}

// loginFlow scripts a full login for acme that ends with Remote Control running.
func loginFlow(status string, more ...Rule) []Rule {
	return append(append([]Rule{
		{Bin: "docker", Match: `^exec -u node claude-acme sh -c env -u CLAUDE_CODE_OAUTH_TOKEN claude auth logout`},
		{Bin: "docker", Match: `^exec -it -u node -w /workspace claude-acme env -u CLAUDE_CODE_OAUTH_TOKEN claude auth login$`},
		{Bin: "docker", Match: `^exec -u node claude-acme sh -c env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status`, Stdout: status},
	}, more...), rcUp("  Capacity: 0/8\n")...)
}

func init() {
	sameOrg := `{"loggedIn":true,"orgId":"org-acme","email":"other@example.com","orgName":"Acme Corp"}`
	withScratch := merge(twoOrgs, map[string]File{"state/orgs/t-scratch": {Dir: true}})
	more := []checked{
		// token
		{Scenario{Name: "token, captured, restarts", Args: []string{"token", "acme"}, Files: twoOrgs,
			Rules: running("acme", tokenCapture("sk-ant-oat01-CAPTURED\n")...)}, 0, []string{"Token saved for acme.", "Restarting claude-acme"}},
		{Scenario{Name: "token, captured, --no-restart", Args: []string{"token", "--no-restart", "acme"}, Files: twoOrgs,
			Rules: running("acme", tokenCapture("sk-ant-oat01-CAPTURED")...)}, 0, []string{"Token saved"}},
		{Scenario{Name: "token, capture fails, pasted", Args: []string{"token", "acme"}, Stdin: "sk-ant-oat01-PASTED\nrest\n", Files: twoOrgs,
			Rules: running("acme", tokenCapture("")...)}, 0, []string{"Couldn't read the token from the output; paste it instead."}},
		{Scenario{Name: "token --paste", Args: []string{"token", "globex", "--paste"}, Stdin: "sk-ant-api03-PASTED\n", Files: twoOrgs},
			0, []string{"Token saved for globex."}},
		// Legacy quirk: read -r hits end of input without a newline, and set -e stops the command.
		{Scenario{Name: "token --paste, no newline", Args: []string{"token", "globex", "--paste"}, Stdin: "sk-ant-api03-PASTED", Files: twoOrgs}, 1, nil},
		{Scenario{Name: "token --paste, not a token", Args: []string{"token", "globex", "--paste"}, Stdin: "hello\n", Files: twoOrgs},
			1, []string{"<tool>: that doesn't look like a Claude token (sk-ant-...)"}},
		{Scenario{Name: "token, not running", Args: []string{"token", "globex"}, Files: twoOrgs}, 1, []string{"not running"}},
		{Scenario{Name: "token, restart fails", Args: []string{"token", "acme"}, Files: twoOrgs, Rules: running("acme", append(tokenCapture("sk-ant-oat01-X\n"),
			Rule{Bin: "docker", Match: `^compose `, Stderr: "nope\n", Exit: 5})...)}, 5, []string{"Restarting"}},

		// login / auth
		{Scenario{Name: "login", Args: []string{"login", "acme"}, Files: twoOrgs, Rules: loginFlow(authStatusAcme)},
			0, []string{"Waiting for the Remote Control service...", "Signed in as: dev@example.com [Acme Corp]"}},
		{Scenario{Name: "login, same Claude organization as another org", Args: []string{"login", "acme"}, Files: withScratch,
			Rules: append([]Rule{
				{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\nclaude-globex\nclaude-t-scratch\n"},
				{Bin: "docker", Match: `^exec -u node claude-globex sh -c env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status`, Stdout: sameOrg},
			}, loginFlow(authStatusAcme)...)},
			0, []string{"WARNING: 'acme' is using the same Claude organization as 'globex'", "grep: <RUN>/state/orgs/t-scratch/org.env: No such file or directory"}},
		{Scenario{Name: "login, shared account", Args: []string{"login", "acme"}, Rules: append([]Rule{
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-acme\nclaude-globex\n"},
			{Bin: "docker", Match: `^exec -u node claude-globex sh -c env`, Stdout: sameOrg},
		}, loginFlow(authStatusAcme)...),
			Files: withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: acmeEnv + "SHARED_ACCOUNT=1\n"})}, 0, []string{"Signed in as"}},
		{Scenario{Name: "login, not signed in afterwards", Args: []string{"login", "acme"}, Files: twoOrgs, Rules: loginFlow(`{"loggedIn":false}`)},
			0, []string{"Remote Control: running"}},
		{Scenario{Name: "login, blocked", Args: []string{"login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme pgrep`, Exit: 1},
			Rule{Bin: "docker", Match: `^exec claude-acme sh -c tail`, Stdout: "blocked by organization policy\n"})},
			0, []string{"BLOCKED"}},
		{Scenario{Name: "login, never ready", Args: []string{"login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme pgrep`, Exit: 1})}, 0, []string{"restarting"}},
		{Scenario{Name: "login, no capacity line", Args: []string{"login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec claude-acme sh -c grep -ao`, Stdout: "https://claude.ai/code?environment=env_X\n"})}, 1, []string{"running"}},
		{Scenario{Name: "login, sign-in fails", Args: []string{"login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `claude auth login$`, Exit: 2})}, 2, []string{"Sign in as the acme Claude account"}},
		{Scenario{Name: "auth", Args: []string{"auth", "acme", "ignored"}, Files: twoOrgs,
			Rules: append(tokenCapture("sk-ant-oat01-AUTH\n"), loginFlow(authStatusAcme)...)},
			0, []string{"== Step 1/2", "== Step 2/2", "Signed in as"}},
		{Scenario{Name: "auth, not running", Args: []string{"auth", "globex"}, Files: twoOrgs}, 1, []string{"not running"}},

		// logout
		{Scenario{Name: "logout, running", Args: []string{"logout", "acme"}, Files: twoOrgs, Rules: running("acme")},
			0, []string{"Logged out acme (Remote Control login removed)."}},
		{Scenario{Name: "logout, stopped", Args: []string{"logout", "globex"},
			Files: withFile(twoOrgs, "state/orgs/globex/claude/.credentials.json", File{Content: "{}"})}, 0, []string{"Logged out globex"}},
		{Scenario{Name: "logout --all, running", Args: []string{"logout", "acme", "--all"}, Files: twoOrgs, Rules: running("acme")},
			0, []string{"Token removed too.", "Restarted claude-acme."}},
		{Scenario{Name: "logout --all, running, restart fails", Args: []string{"logout", "acme", "--all"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^compose `, Exit: 4})}, 4, []string{"Token removed too."}},
		{Scenario{Name: "logout --all, stopped", Args: []string{"logout", "globex", "--all"}, Files: twoOrgs}, 0, []string{"Token removed too."}},
		{Scenario{Name: "logout, logout command fails", Args: []string{"logout", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^exec -u node claude-acme sh -c env`, Exit: 3})}, 3, nil},

		// gh-login
		{Scenario{Name: "gh-login, already signed in", Args: []string{"gh-login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `gh api user`, Stdout: "octo-acme\n"})}, 0, []string{"acme: gh already signed in as octo-acme (use --force to sign in again)"}},
		{Scenario{Name: "gh-login", Args: []string{"gh-login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `gh api user`, Once: true},
			Rule{Bin: "docker", Match: `gh api user`, Stdout: "octo-acme\n"})}, 0, []string{"acme: gh signed in as octo-acme"}},
		{Scenario{Name: "gh-login --force", Args: []string{"gh-login", "acme", "--force"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `gh api user`, Stdout: "octo-acme\n"})}, 0, []string{"Signing the gh CLI in"}},
		{Scenario{Name: "gh-login, didn't complete", Args: []string{"gh-login", "acme"}, Files: twoOrgs, Rules: running("acme")},
			1, []string{"<tool>: gh sign-in didn't complete for acme"}},
		{Scenario{Name: "gh-login, gh fails", Args: []string{"gh-login", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `gh auth login`, Exit: 1})}, 1, nil},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
}
