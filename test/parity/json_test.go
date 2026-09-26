package parity

import (
	"strings"
	"testing"
)

// TestBerthJSONOutput pins `--output json` (#54, docs/json.md) against the parity fixtures: the same
// data as the text output, in the documented schema. Secrets never appear (env lists names only).
func TestBerthJSONOutput(t *testing.T) {
	jsonRun := func(args []string, files map[string]File, rules []Rule) Result {
		t.Helper()
		r := run(t, Berth(berthBin), Scenario{Args: append([]string{"--output", "json"}, args...), Files: files, Rules: rules})
		if r.Exit != 0 || r.Stderr != "" {
			t.Fatalf("%v: exit %d, stderr %q", args, r.Exit, r.Stderr)
		}
		return r
	}

	r := jsonRun([]string{"ls"}, twoOrgs, acmeRunning)
	want := `{
  "schema": "berth.orgs/v1",
  "orgs": [
    {
      "name": "acme",
      "manager": "berth",
      "state": "up",
      "ssh_port": 2201,
      "ttyd_port": 7701,
      "token": true,
      "remote": "on",
      "host": "local"
    },
    {
      "name": "globex",
      "manager": "berth",
      "state": "down",
      "ssh_port": 2202,
      "ttyd_port": 7702,
      "token": false,
      "remote": "-",
      "host": "local"
    }
  ],
  "unreachable": []
}
`
	if r.Stdout != want {
		t.Errorf("ls:\n%s\nwant:\n%s", r.Stdout, want)
	}

	r = jsonRun([]string{"env", "acme", "ls"}, withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: acmeEnv + "CCENV_ENV_KEYS=OPENROUTER_API_KEY  GONE_KEY\nOPENROUTER_API_KEY='sk-secret'\n"}), nil)
	want = `{
  "schema": "berth.env/v1",
  "org": "acme",
  "vars": [
    {
      "name": "OPENROUTER_API_KEY",
      "present": true
    },
    {
      "name": "GONE_KEY",
      "present": false
    }
  ]
}
`
	if r.Stdout != want || strings.Contains(r.Stdout, "sk-secret") {
		t.Errorf("env ls:\n%s\nwant:\n%s", r.Stdout, want)
	}

	r = jsonRun([]string{"fw", "globex", "show"}, twoOrgs, nothingRunning)
	want = `{
  "schema": "berth.firewall/v1",
  "org": "globex",
  "file": "<RUN>/state/orgs/globex/config/firewall.txt",
  "entries": [
    "mode on",
    "@mise",
    "@python",
    "pypi.org"
  ],
  "live": null
}
`
	if r.Stdout != want {
		t.Errorf("fw show (down):\n%s\nwant:\n%s", r.Stdout, want)
	}
	r = jsonRun([]string{"fw", "acme", "show"}, twoOrgs, running("acme", Rule{Bin: "docker", Match: `cat /run/firewall\.status$`, Stdout: "on 42\n"}))
	if !strings.Contains(r.Stdout, `"live": "on 42"`) {
		t.Errorf("fw show (up): %s", r.Stdout)
	}
	// No entries is an empty list in JSON, not ccenv's exit 1.
	r = jsonRun([]string{"fw", "globex", "show"}, withFile(twoOrgs, "state/orgs/globex/config/firewall.txt", File{Content: "# only comments\n", Mode: 0o644}), nil)
	if !strings.Contains(r.Stdout, `"entries": []`) {
		t.Errorf("fw show, no entries: %s", r.Stdout)
	}
}
