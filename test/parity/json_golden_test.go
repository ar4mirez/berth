package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonCase is one `--output json` document pinned in testdata/json/<name>.json.
type jsonCase struct {
	name  string
	args  []string
	files map[string]File
	rules []Rule
	// exit is the expected exit code (0 unless the operation reports a failure with its data).
	exit int
}

// jsonRepos is acme with three registered repos (one cloned, one local, one missing), a stray
// folder in its workspace and something in quarantine.
var jsonRepos = merge(repoOrg, map[string]File{
	"state/orgs/acme/config/repos.txt":    {Content: "app git@github.com:acme/app.git\nnotes local\napi git@github.com:acme/api.git develop\n", Mode: 0o644},
	"state/orgs/acme/workspace/app/.git":  {Dir: true},
	"state/orgs/acme/workspace/stray":     {Dir: true},
	"state/orgs/acme/quarantine":          {Dir: true, Mode: 0o700},
	"state/orgs/acme/quarantine/old.2026": {Dir: true},
})

const authStatusOcto = `{"loggedIn":true,"orgId":"org-acme","email":"dev@example.com","orgName":"Acme Corp"}`

// jsonCases are the documents of every command that returns data (internal/ops, docs/json.md),
// against the parity fixtures. Run with BERTH_UPDATE_GOLDEN=1 to rewrite the files after an
// intended change, and review the diff: removing or renaming a field needs a new schema version.
var jsonCases = []jsonCase{
	{name: "orgs", args: []string{"ls"}, files: twoOrgs, rules: acmeRunning},
	{name: "info-running", args: []string{"info", "acme"}, files: twoOrgs, rules: acmeRunning},
	{name: "info-stopped", args: []string{"info", "globex"}, files: twoOrgs, rules: nothingRunning},
	{name: "whoami", args: []string{"whoami"}, files: twoOrgs, rules: acmeUp(authStatusOcto, "octo-acme\n")},
	{name: "whoami-signed-out", args: []string{"whoami", "acme"}, files: twoOrgs, rules: acmeUp(`{"loggedIn":false}`, "")},
	{name: "remote-on", args: []string{"remote", "acme", "status"}, files: twoOrgs, rules: rcUp("  Capacity: 1/8 sessions\n")},
	{name: "remote-login-needed", args: []string{"remote", "acme"}, files: twoOrgs, rules: running("acme",
		Rule{Bin: "docker", Match: `test -f /home/node/\.claude/\.credentials\.json$`, Exit: 1})},
	{name: "repos", args: []string{"repo", "ls", "acme"}, files: jsonRepos, rules: running("acme",
		Rule{Bin: "docker", Match: `test -d /workspace/app/\.git$`},
		Rule{Bin: "docker", Match: `-w /workspace/app claude-acme sh -c printf`, Stdout: "feature/x 3"},
		Rule{Bin: "docker", Match: `test -d /workspace/`, Exit: 1})},
	{name: "repos-stopped", args: []string{"repo", "ls", "acme"}, files: jsonRepos},
	{name: "repo-audit", args: []string{"repo", "audit", "acme"}, files: jsonRepos},
	{name: "repo-policy", args: []string{"repo", "policy", "acme"}, files: twoOrgs},
	{name: "firewall", args: []string{"fw", "acme", "show"}, files: twoOrgs, rules: acmeRunning},
	{name: "firewall-presets", args: []string{"fw", "acme", "presets"}, files: twoOrgs, rules: running("acme",
		Rule{Bin: "docker", Match: `init-firewall\.sh presets$`, Stdout: "@go         proxy.golang.org sum.golang.org\n@python     pypi.org files.pythonhosted.org\n"})},
	{name: "firewall-test", args: []string{"fw", "acme", "test", "pypi.org", "http://blocked.example"}, files: twoOrgs, rules: running("acme",
		Rule{Bin: "docker", Match: `curl .* http://blocked\.example$`, Exit: 7})},
	{name: "env", args: []string{"env", "acme", "ls"}, files: withFile(twoOrgs, "state/orgs/acme/org.env",
		File{Content: acmeEnv + "CCENV_ENV_KEYS=OPENROUTER_API_KEY  GONE_KEY\nOPENROUTER_API_KEY='sk-secret'\n"})},
	{name: "schedule-none", args: []string{"schedule", "status"}, files: twoOrgs, rules: []Rule{
		{Bin: "systemctl", Match: `show-environment`, Exit: 1}, {Bin: "crontab", Match: `^-l$`, Exit: 1}}},
	{name: "schedule-cron", args: []string{"schedule", "status"}, files: twoOrgs, rules: []Rule{
		{Bin: "systemctl", Match: `show-environment`, Exit: 1},
		{Bin: "crontab", Match: `^-l$`, Stdout: "0 * * * * /usr/bin/true\n30 2 * * * /opt/berth backup --all --keep 7 >> /b/cron.log 2>&1  # <SCHEDULE>\n"}}},
	{name: "hosts", args: []string{"host", "ls"}, files: twoOrgs, rules: []Rule{{Bin: "docker", Match: `^version`, Stdout: "29.0.0\n"}}},
	{name: "default-org-none", args: []string{"use"}, files: twoOrgs},
	{name: "default-org", args: []string{"use"}, files: withFile(twoOrgs, "home/.config/berth/context", File{Content: "acme\n", Mode: 0o600})},
}

// TestBerthJSONGolden compares each document with its golden file. Secrets never appear.
func TestBerthJSONGolden(t *testing.T) {
	update := os.Getenv("BERTH_UPDATE_GOLDEN") != ""
	seen := map[string]bool{}
	for _, c := range jsonCases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, Berth(berthBin), Scenario{Args: append([]string{"--output", "json"}, c.args...), Files: c.files, Rules: c.rules})
			if r.Exit != c.exit || r.Stderr != "" {
				t.Fatalf("%v: exit %d, stderr %q\n%s", c.args, r.Exit, r.Stderr, r.Stdout)
			}
			for _, secret := range []string{"sk-ant-", "PARITY-TOKEN", "ttyd_credential"} {
				if strings.Contains(r.Stdout, secret) {
					t.Errorf("%v: the document has %q", c.args, secret)
				}
			}
			file := filepath.Join("testdata", "json", c.name+".json")
			if update {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(r.Stdout), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (BERTH_UPDATE_GOLDEN=1 writes it)", err)
			}
			if r.Stdout != string(want) {
				t.Errorf("%v:\n%s\nwant (%s):\n%s", c.args, r.Stdout, file, want)
			}
		})
		seen[c.name+".json"] = true
	}
	// A golden file no case uses is a document nothing checks any more.
	files, _ := os.ReadDir(filepath.Join("testdata", "json"))
	for _, f := range files {
		if !seen[f.Name()] {
			t.Errorf("testdata/json/%s has no case", f.Name())
		}
	}
}

// TestBerthJSONErrors: under --output json a failure is one berth.error/v1 document on stderr, with
// nothing on stdout and the same exit code as the text output. Where ccenv exits without a message,
// the document still says what happened.
func TestBerthJSONErrors(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		files map[string]File
		rules []Rule
		exit  int
		want  string
	}{
		{"an unknown org", []string{"info", "nope"}, twoOrgs, nil, 1,
			`{"schema":"berth.error/v1","kind":"not-found","code":1,"message":"unknown org 'nope'","hint":"run: <tool> init nope"}`},
		{"a stopped org", []string{"remote", "globex", "status"}, twoOrgs, nil, 1,
			`{"schema":"berth.error/v1","kind":"not-running","code":1,"message":"claude-globex is not running","hint":"<tool> up globex"}`},
		{"ccenv's silent exit", []string{"repo", "audit", "globex"}, twoOrgs, nil, 2,
			`{"schema":"berth.error/v1","kind":"state","code":2,"message":"globex has no quarantine folder yet: it has never started","hint":"<tool> up globex"}`},
		{"a writing command", []string{"up", "acme"}, twoOrgs, nil, 1,
			`{"schema":"berth.error/v1","kind":"usage","code":1,"message":"--output json isn't available for \"up\": it changes things, and returns no data","hint":""}`},
		{"a stream", []string{"logs", "acme"}, twoOrgs, nil, 1,
			`{"schema":"berth.error/v1","kind":"usage","code":1,"message":"--output json isn't available for \"logs\": it is a stream of log lines","hint":""}`},
		{"a failing command", []string{"fw", "acme", "presets"}, twoOrgs, running("acme", Rule{Bin: "docker", Match: `presets$`, Exit: 4}), 4,
			`"kind":"command","code":4,`},
	} {
		r := run(t, Berth(berthBin), Scenario{Args: append([]string{"--output", "json"}, c.args...), Files: c.files, Rules: c.rules})
		// Compact, fields in the document's order.
		var doc struct {
			Schema  string `json:"schema"`
			Kind    string `json:"kind"`
			Code    int    `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		}
		if err := json.Unmarshal([]byte(r.Stderr), &doc); err != nil {
			t.Errorf("%s: stderr isn't one JSON document: %v\n%s", c.name, err, r.Stderr)
			continue
		}
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(doc)
		got := b.String()
		if r.Exit != c.exit || r.Stdout != "" || !strings.Contains(got, c.want) {
			t.Errorf("%s: exit %d, stdout %q\nstderr %s\n  want %s", c.name, r.Exit, r.Stdout, got, c.want)
		}
	}
	// Without --output json nothing changes: the message as before, and silence where ccenv is silent.
	r := run(t, Berth(berthBin), Scenario{Args: []string{"repo", "audit", "globex"}, Files: twoOrgs})
	if r.Exit != 2 || r.Stderr != "" || r.Stdout != "" {
		t.Errorf("text, silent exit: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
}
