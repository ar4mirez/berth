package parity

// Synthetic fixtures only (acme, globex, t-*): nothing here comes from a real org.

const acmeEnv = `# ---- Claude account (use: ccenv token acme) ---------------------------------
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-FAKE-PARITY-TOKEN
GIT_USER_NAME=Test User
GIT_USER_EMAIL=test@example.com
GH_TOKEN=
BIND_ADDR=tailscale
SSH_PORT=2201
TTYD_PORT=7701
REMOTE_CONTROL=1
REMOTE_CAPACITY=8
REPO_POLICY=enforce
MEM_LIMIT=8g
CPUS=4
`

const globexEnv = `CLAUDE_CODE_OAUTH_TOKEN=
GIT_USER_NAME=Test User
GIT_USER_EMAIL=test@example.com
BIND_ADDR=127.0.0.1
SSH_PORT=2202
TTYD_PORT=7702
REMOTE_CONTROL=0
MEM_LIMIT=4g
CPUS=2
`

const backupKey = "# created: 2026-01-02\n# public key: age1parityfake\nAGE-SECRET-KEY-1FAKE\n"

// orgFiles is an org laid out like `ccenv init` makes it (fake key, fake password).
func orgFiles(org, env string) map[string]File {
	d := "state/orgs/" + org + "/"
	return map[string]File{
		d + "org.env":                        {Content: env},
		d + "workspace":                      {Dir: true, Mode: 0o755},
		d + "claude":                         {Dir: true},
		d + "ssh":                            {Dir: true},
		d + "ssh/id_ed25519":                 {Content: "FAKE PRIVATE KEY\n"},
		d + "ssh/id_ed25519.pub":             {Content: "ssh-ed25519 AAAAFAKE claude-" + org + "@parity-host\n", Mode: 0o644},
		d + "sshd":                           {Dir: true, Mode: 0o755},
		d + "mise":                           {Dir: true, Mode: 0o755},
		d + "home-config":                    {Dir: true},
		d + "config":                         {Dir: true, Mode: 0o755},
		d + "config/authorized_keys":         {Content: "", Mode: 0o644},
		d + "config/secrets":                 {Dir: true},
		d + "config/secrets/ttyd_credential": {Content: "node:PARITYpassword0123456789abcd"},
		d + "config/firewall.txt":            {Content: "mode on\n@mise\n@python\npypi.org\n", Mode: 0o644},
		d + "config/repos.txt":               {Content: "# Repos allowed in the '" + org + "' container.\napp git@github.com:acme/app.git\n", Mode: 0o644},
		d + "workspace/app":                  {Dir: true, Mode: 0o755},
		d + "workspace/app/.git":             {Dir: true, Mode: 0o755},
		d + "workspace/stray":                {Dir: true, Mode: 0o755},
	}
}

func merge(ms ...map[string]File) map[string]File {
	out := map[string]File{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// twoOrgs: acme (running, Remote Control on) and globex (stopped).
var twoOrgs = merge(orgFiles("acme", acmeEnv), orgFiles("globex", globexEnv))

// withQuarantine: as twoOrgs, after globex has been up once (compose creates quarantine/) and the
// sweep has moved a folder there.
var withQuarantine = merge(twoOrgs, map[string]File{
	"state/orgs/globex/quarantine":                     {Dir: true},
	"state/orgs/globex/quarantine/old.20260101-000000": {Dir: true, Mode: 0o755},
})

// withKey: as twoOrgs, with a backup key (scheduled backups need one).
var withKey = merge(twoOrgs, map[string]File{"home/.config/ccenv/backup.key": {Content: backupKey}})

var acmeRunning = []Rule{
	{Bin: "docker", Match: `^ps --format \{\{\.Names\}\}$`, Stdout: "claude-acme\n"},
	{Bin: "docker", Match: `^exec claude-acme test -f /home/node/\.claude/\.credentials\.json$`},
	{Bin: "docker", Match: `^exec claude-acme pgrep -f claude remote-control$`},
	{Bin: "docker", Match: `^exec claude-acme sh -c grep -ao`, Stdout: "https://claude.ai/code?environment=env_PARITY01\n"},
	{Bin: "docker", Match: `^exec claude-acme sh -c sed `, Stdout: "  Capacity: 1/8 sessions\n"},
	{Bin: "docker", Match: `^exec claude-acme cat /run/firewall\.status$`, Stdout: "on (12 entries)\n"},
	{Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"},
}

var nothingRunning = []Rule{
	{Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"},
}

// noSystemd makes `systemctl --user show-environment` fail, so schedule falls back to cron.
var noSystemd = Rule{Bin: "systemctl", Match: `--user show-environment`, Exit: 1}

// A scenario with the checks that it really exercised the command (and didn't just fail early).
type checked struct {
	Scenario
	WantExit int
	WantOut  []string // substrings of stdout+stderr
}

var scenarios = []checked{
	{Scenario{Name: "no args prints usage", Args: nil}, 0, []string{"one Claude Code container per organization"}},
	{Scenario{Name: "unknown command", Args: []string{"frobnicate"}}, 1, []string{"Setup"}},
	{Scenario{Name: "help", Args: []string{"help"}}, 0, []string{"Firewall"}},
	{Scenario{Name: "info unknown org", Args: []string{"info", "nope"}, Files: twoOrgs}, 1, []string{"<tool>: unknown org 'nope'"}},
	{Scenario{Name: "info missing org", Args: []string{"info"}}, 1, []string{"<tool>: missing <org>"}},
	{Scenario{Name: "ls", Args: []string{"ls"}, Files: twoOrgs, Rules: acmeRunning}, 0, []string{"acme", "globex", "on"}},
	{Scenario{Name: "info running org", Args: []string{"info", "acme"}, Files: twoOrgs, Rules: acmeRunning}, 0, []string{"env_PARITY01", "100.64.0.7"}},
	// Legacy quirk: `sp=$(envval "$org" SSH_PORT)` at function level, under set -e -o pipefail, ends
	// the command with exit 1 and no message when org.env has no SSH_PORT.
	{Scenario{Name: "info, no SSH_PORT", Args: []string{"info", "globex"}, Rules: nothingRunning,
		Files: merge(twoOrgs, map[string]File{"state/orgs/globex/org.env": {Content: "BIND_ADDR=127.0.0.1\nTTYD_PORT=7702\n"}})}, 1, nil},
	{Scenario{Name: "whoami, all down", Args: []string{"whoami"}, Files: twoOrgs, Rules: nothingRunning}, 0, []string{"(container down)"}},
	{Scenario{Name: "password show", Args: []string{"password", "acme"}, Files: twoOrgs}, 0, []string{"pass: PARITYpassword"}},
	{Scenario{Name: "fw show, running", Args: []string{"fw", "acme"}, Files: twoOrgs, Rules: acmeRunning}, 0, []string{"live: on"}},
	{Scenario{Name: "fw allow, stopped", Args: []string{"fw", "globex", "allow", "https://files.example.com:443/x", "@node", "pypi.org"},
		Files: twoOrgs, Rules: nothingRunning}, 0, []string{"allowed: files.example.com", "(saved; applies on"}},
	{Scenario{Name: "fw deny", Args: []string{"fw", "globex", "deny", "pypi.org", "nope.example"}, Files: twoOrgs, Rules: nothingRunning},
		0, []string{"removed: pypi.org", "not in list: nope.example"}},
	{Scenario{Name: "repo ls, stopped", Args: []string{"repo", "ls", "globex"}, Files: withQuarantine, Rules: nothingRunning},
		0, []string{"cloned (container down)", "Not allowed in /workspace", "stray", "Quarantined"}},
	// Legacy quirk: with no quarantine/ yet (init doesn't create it; the first up does), the audit's
	// qn=$(ls quarantine | wc -l) fails under pipefail, and set -e ends the command with exit 2.
	{Scenario{Name: "repo ls, never started", Args: []string{"repo", "ls", "globex"}, Files: twoOrgs, Rules: nothingRunning},
		2, []string{"cloned (container down)"}},
	// Legacy quirk: exit 1 after success, because the function ends with `running "$org" && ...`.
	{Scenario{Name: "repo policy set, stopped", Args: []string{"repo", "policy", "globex", "warn"}, Files: twoOrgs, Rules: nothingRunning},
		1, []string{"REPO_POLICY=warn"}},
	{Scenario{Name: "env set from stdin", Args: []string{"env", "globex", "set", "OPENROUTER_API_KEY", "--no-restart"},
		Stdin: "sk-or-FAKE\n", Files: twoOrgs, Rules: nothingRunning}, 0, []string{"set: OPENROUTER_API_KEY"}},
	{Scenario{Name: "env set reserved", Args: []string{"env", "globex", "set", "SSH_PORT"}, Files: twoOrgs}, 1, []string{"managed by ccenv"}},
	{Scenario{Name: "down", Args: []string{"down", "acme"}, Files: twoOrgs, Rules: acmeRunning}, 0, nil},
	{Scenario{
		Name: "init", Args: []string{"init", "t-new", "--name", "Test User", "--email", "test@example.com"},
		Files: twoOrgs, Rules: nothingRunning,
		Random: map[string]string{"state/orgs/t-new/config/secrets/ttyd_credential": `^node:[A-Za-z0-9]{32}$`},
	}, 0, []string{"Created", "ccenv up t-new"}},
	{Scenario{Name: "init bad name", Args: []string{"init", "Bad_Name"}}, 1, []string{"lowercase"}},
	{Scenario{Name: "schedule via systemd", Args: []string{"schedule", "--at", "02:30", "--keep", "7"}, Files: withKey},
		0, []string{"daily at 02:30", "keeping the newest 7"}},
	{Scenario{Name: "schedule via cron", Args: []string{"schedule"}, Files: withKey, Rules: []Rule{
		noSystemd,
		{Bin: "crontab", Match: `^-l$`, Stdout: "MAILTO=\"\"\n0 * * * * /usr/bin/true\n"},
	}}, 0, []string{"Scheduled (cron)"}},
	// Legacy bug: with no crontab yet, `crontab -l | grep -v` fails inside the set -e subshell, so the
	// job line is never echoed: an empty table is installed, and schedule exits 1 without a message.
	{Scenario{Name: "schedule via cron, no crontab yet", Args: []string{"schedule"}, Files: withKey, Rules: []Rule{
		noSystemd,
		{Bin: "crontab", Match: `^-l$`, Exit: 1},
	}}, 1, nil},
	{Scenario{Name: "schedule without a key", Args: []string{"schedule"}, Files: twoOrgs}, 1, []string{"need a key"}},
}
