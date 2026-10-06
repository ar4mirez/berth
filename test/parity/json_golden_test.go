package parity

import (
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
