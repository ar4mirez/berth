package parity

import (
	"regexp"
	"strings"
	"testing"
)

// TestProcCommand: `berth proc` (#216, berth-only). A process starts in a running org's container
// through berth-proc, as the container's user, its spec (variables' values included) on stdin and
// never on a command line; a stopped org, a name in use, a spec it doesn't take and an image that
// predates berth-proc are each refused with what to do; nothing restarts.
func TestProcCommand(t *testing.T) {
	raw := allCalls()
	start := Rule{Bin: "docker", Match: `^exec -u node -i claude-acme .*/usr/local/bin/berth-proc start worker$`, Stdout: procWorker + "\n"}
	r := run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--env", "API_KEY", "--", "worker", "--once"},
		Stdin: "sk-proc-SECRET\n", Files: twoOrgs, Rules: running("acme", start)})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "Started worker in acme.\nworker  running since 2026-01-02T03:04:05Z, 0 restarts\n  worker --once\n  variables: API_KEY\n") {
		t.Fatalf("start: exit %d\nstdout %q\nstderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	if strings.Contains(strings.Join(r.Calls, "\n"), "sk-proc-SECRET") {
		t.Errorf("a variable's value is on a command line:\n%s", strings.Join(r.Calls, "\n"))
	}
	if callsMatching(r, regexp.MustCompile(`^docker "exec" "-u" "node" "-i" "claude-acme" .*"/usr/local/bin/berth-proc" "start" "worker"$`)) != 1 ||
		callsMatching(r, regexp.MustCompile(`"compose"|"restart"|"-u" "root"|"-u" "0"`)) != 0 {
		t.Errorf("start: one berth-proc as node, and nothing else:\n%s", strings.Join(r.Calls, "\n"))
	}

	// Several from stdin, and from --env-file; the values go on berth-proc's stdin.
	envFile := merge(twoOrgs, map[string]File{"home/api.env": {Content: "# the worker's\nAPI_URL=http://example.com/?a=b\n\n", Mode: 0o600}})
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--env", "API_KEY", "--env", "DEBUG", "--env-file", "api.env", "--", "worker"},
		Stdin: "DEBUG=1\nAPI_KEY=sk-proc-SECRET\n", Files: envFile, Rules: running("acme", start)})
	if r.Exit != 0 || strings.Contains(strings.Join(r.Calls, "\n"), "sk-proc-SECRET") {
		t.Errorf("start with stdin and --env-file: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}

	// Refused before berth-proc runs: a stopped org, and specs berth doesn't take.
	for name, tc := range map[string]struct {
		args  []string
		rules []Rule
		want  string
		stdin string
	}{
		"a stopped org":      {[]string{"proc", "acme", "start", "worker", "--", "worker"}, nil, "claude-acme is not running", ""},
		"no --":              {[]string{"proc", "acme", "start", "worker", "worker"}, running("acme"), "missing -- before the command", ""},
		"a relative argv[0]": {[]string{"proc", "acme", "start", "worker", "--", "bin/worker"}, running("acme"), "an absolute path, or a name on the container user's PATH", ""},
		"a bad name":         {[]string{"proc", "acme", "start", "Worker", "--", "worker"}, running("acme"), "lowercase letters", ""},
		"a bad variable":     {[]string{"proc", "acme", "start", "worker", "--env", "api", "--", "worker"}, running("acme"), "capitals, digits and underscores", "x"},
		"a value on argv":    {[]string{"proc", "acme", "start", "worker", "--env", "API_KEY=sk-proc-SECRET", "--", "worker"}, running("acme"), "never on the command line: give --env API_KEY and its value on stdin, or NAME=value lines in --env-file", ""},
		"a value not given":  {[]string{"proc", "acme", "start", "worker", "--env", "A", "--env", "B", "--", "worker"}, running("acme"), "stdin has no value for B", "A=1\n"},
		"an extra on stdin":  {[]string{"proc", "acme", "start", "worker", "--env", "A", "--env", "B", "--", "worker"}, running("acme"), "C, which no --env names", "A=1\nB=2\nC=3\n"},
		"no --env-file":      {[]string{"proc", "acme", "start", "worker", "--env-file", "nope.env", "--", "worker"}, running("acme"), "--env-file", ""},
		"an option argv[0]":  {[]string{"proc", "acme", "start", "worker", "--", "-i", "worker"}, running("acme"), "an absolute path, or a name on the container user's PATH", ""},
		"a NAME=value argv0": {[]string{"proc", "acme", "start", "worker", "--", "FOO=bar", "worker"}, running("acme"), "an absolute path, or a name on the container user's PATH", ""},
		"a long grace":       {[]string{"proc", "acme", "start", "worker", "--grace", "301", "--", "worker"}, running("acme"), "1 to 300", ""},
		"a restart":          {[]string{"proc", "acme", "start", "worker", "--restart", "always", "--", "worker"}, running("acme"), "never or on-failure", ""},
	} {
		r := run(t, raw, Scenario{Args: tc.args, Stdin: tc.stdin, Files: twoOrgs, Rules: tc.rules})
		if r.Exit == 0 || !strings.Contains(r.Stderr, tc.want) || strings.Contains(r.Stderr, "sk-proc-SECRET") || callsMatching(r, regexp.MustCompile(`berth-proc`)) != 0 {
			t.Errorf("%s: exit %d, stderr %q", name, r.Exit, r.Stderr)
		}
	}
	// berth-proc's own refusals: a name in use, and an image without berth-proc.
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--", "worker"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `berth-proc start worker$`, Stderr: "berth-proc: a process named worker exists: stop it first\n", Exit: 3})})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "a process named worker exists: stop it first") {
		t.Errorf("a name in use: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "ls"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `berth-proc status$`, Stderr: "OCI runtime exec failed: exec: \"/usr/local/bin/berth-proc\": stat /usr/local/bin/berth-proc: no such file or directory\n", Exit: 127})})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "runs an image that has no supervised processes") || !strings.Contains(r.Stderr, "<tool> restart acme, which stops the work running in it") {
		t.Errorf("an older image: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// ls, show and stop read berth-proc's state; a stopped org has no processes.
	r = run(t, raw, Scenario{Args: []string{"--output", "json", "proc", "acme"}, Files: twoOrgs, Rules: running("acme", mcpRules...)})
	if r.Exit != 0 || !strings.Contains(r.Stdout, `"schema": "berth.processes/v1"`) || !strings.Contains(r.Stdout, `"env_names": [`) {
		t.Errorf("ls: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "ls"}, Files: twoOrgs})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "(no processes in acme;") || callsMatching(r, regexp.MustCompile(`berth-proc`)) != 0 {
		t.Errorf("ls of a stopped org: exit %d, stdout %q", r.Exit, r.Stdout)
	}
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "stop", "worker"}, Files: twoOrgs, Rules: running("acme", mcpRules...)})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "Stopped worker in acme.\nworker  exited") {
		t.Errorf("stop: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "show", "nope"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `berth-proc status nope$`, Stderr: "berth-proc: no process named nope\n", Exit: 4})})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "no process named nope") {
		t.Errorf("show, unknown: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"--read-only", "proc", "acme", "start", "worker", "--", "worker"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "read-only") || callsMatching(r, regexp.MustCompile(`berth-proc`)) != 0 {
		t.Errorf("--read-only: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}
