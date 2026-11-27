package parity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/apiclient"
	"github.com/ar4mirez/berth/internal/ops"
)

// TestProcCommand: `berth proc` (#216, berth-only). A process starts in a running org's container
// through berth-proc, as the container's user, its spec (variables' values included) on stdin and
// never on a command line; a stopped org, a name in use, a spec it doesn't take and an image that
// predates berth-proc are each refused with what to do; nothing restarts.
func TestProcCommand(t *testing.T) {
	raw := allCalls()
	start := Rule{Bin: "docker", Match: `^exec -u node -i claude-acme .*/usr/local/bin/berth-proc start worker$`, Stdout: procWorker + "\n", Stdin: true}
	token := Rule{Bin: "docker", Match: `^exec -i -u node claude-acme sh -c .* berth-placement /run/berth/placement worker$`, Stdin: true}
	// What a start with placement runs: berth-proc's state, the container its token names (#216),
	// the token, then berth-proc.
	starting := append([]Rule{start, token}, mcpRules[:3]...)
	r := run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--env", "API_KEY", "--", "worker", "--once"},
		Stdin: "sk-proc-SECRET\n", Files: twoOrgs, Rules: running("acme", starting...)})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "Started worker in acme.\nworker  running since 2026-01-02T03:04:05Z, 0 restarts\n  worker --once\n  variables: API_KEY\n") {
		t.Fatalf("start: exit %d\nstdout %q\nstderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	// The value goes on berth-proc's stdin, never in an argument, nor anywhere else.
	for _, c := range r.Calls {
		args, stdin, _ := strings.Cut(c, "  <stdin ")
		if strings.Contains(args, "sk-proc-SECRET") || (strings.Contains(stdin, "sk-proc-SECRET") && !strings.Contains(args, `berth-proc" "start"`)) {
			t.Errorf("a variable's value is on a command line, or went elsewhere: %s", c)
		}
	}
	// Without --placement, a start is as it was: no token, and no BERTH_PLACEMENT_FILE.
	if callsMatching(r, regexp.MustCompile(`berth-placement|BERTH_PLACEMENT_FILE|"inspect"`)) != 0 {
		t.Errorf("a start without placement got a token:\n%s", strings.Join(r.Calls, "\n"))
	}
	if callsMatching(r, regexp.MustCompile(`^docker "exec" "-u" "node" "-i" "claude-acme" .*"/usr/local/bin/berth-proc" "start" "worker"`)) != 1 ||
		callsMatching(r, regexp.MustCompile(`"compose"|"restart"|"-u" "root"|"-u" "0"`)) != 0 {
		t.Errorf("start: one berth-proc as node, and nothing else:\n%s", strings.Join(r.Calls, "\n"))
	}

	// With --placement, and a berth serve keeping tokens fresh: its placement token, written
	// first, as the container's user: a JWS naming the org, the container and the process; the
	// process is told where it is.
	beat := File{Content: fmt.Sprintf(`{"pid":%d,"at":%q}`+"\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))}
	served := withFile(twoOrgs, "state/placement-serve.json", beat)
	none := Rule{Bin: "docker", Match: `^exec -u node claude-acme /usr/local/bin/berth-proc status$`, Stdout: "[]\n"}
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--placement", "--", "worker", "--once"}, Files: served, Rules: running("acme", append([]Rule{none}, starting...)...)})
	tokenAt := slices.IndexFunc(r.Calls, func(c string) bool { return strings.Contains(c, `"berth-placement" "/run/berth/placement" "worker"`) })
	startAt := slices.IndexFunc(r.Calls, func(c string) bool { return strings.Contains(c, `berth-proc" "start" "worker"`) })
	if r.Exit != 0 || tokenAt < 0 || startAt < tokenAt || !strings.Contains(r.Calls[startAt], `BERTH_PLACEMENT_FILE\":\"/run/berth/placement/worker`) {
		t.Errorf("--placement: the token, then the start with its path: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	} else if claims := jwsClaims(t, r.Calls[tokenAt]); claims["org"] != "acme" || claims["container"] != "f00dcafe0123" ||
		claims["container_started_at"] != "2026-01-02T03:04:05.123456789Z" || claims["process"] != "worker" {
		t.Errorf("the token's claims: %v", claims)
	}
	if strings.Contains(r.Stdout+r.Stderr, ".eyJ") {
		t.Errorf("the token is in berth's output: %q %q", r.Stdout, r.Stderr)
	}
	// With --placement and nothing to keep it fresh, it is refused before a token or a process:
	// no berth serve, one that ended (its pid gone), one whose heartbeat is old (a read-only
	// serve keeps none).
	gone := File{Content: `{"pid":999999999,"at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}` + "\n"}
	old := File{Content: fmt.Sprintf(`{"pid":%d,"at":%q}`+"\n", os.Getpid(), time.Now().Add(-3*time.Minute).UTC().Format(time.RFC3339Nano))}
	for name, files := range map[string]map[string]File{"no serve": twoOrgs, "a serve that ended": withFile(twoOrgs, "state/placement-serve.json", gone),
		"an old heartbeat": withFile(twoOrgs, "state/placement-serve.json", old)} {
		r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--placement", "--", "worker"}, Files: files, Rules: running("acme", starting...)})
		if r.Exit != 1 || !strings.Contains(r.Stderr, "placement needs <tool> serve") || !strings.Contains(r.Stderr, "start <tool> serve (not --read-only)") ||
			callsMatching(r, regexp.MustCompile(`berth-placement|berth-proc" "start"`)) != 0 {
			t.Errorf("--placement, %s: exit %d, stderr %q\n%s", name, r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
		}
	}
	// An image that predates supervised processes: the same refusal, with or without placement,
	// and no token written.
	olderImage := Rule{Bin: "docker", Match: `berth-proc (status|start worker)$`, Stderr: "OCI runtime exec failed: exec: \"/usr/local/bin/berth-proc\": stat /usr/local/bin/berth-proc: no such file or directory\n", Exit: 127}
	for _, args := range [][]string{{"proc", "acme", "start", "worker", "--", "worker"}, {"proc", "acme", "start", "worker", "--placement", "--", "worker"}} {
		r = run(t, raw, Scenario{Args: args, Files: served, Rules: running("acme", olderImage, token, mcpRules[0], mcpRules[1])})
		if r.Exit != 1 || !strings.Contains(r.Stderr, "runs an image that has no supervised processes") || !strings.Contains(r.Stderr, "<tool> restart acme, which stops the work running in it") ||
			callsMatching(r, regexp.MustCompile(`berth-placement`)) != 0 {
			t.Errorf("%v on an older image: exit %d, stderr %q", args, r.Exit, r.Stderr)
		}
	}

	// Right after the container starts, before its entrypoint has made berth-proc's directory:
	// `starting`, with or without placement, and no token or process. Once it is ready, the same
	// start succeeds.
	for _, args := range [][]string{{"proc", "acme", "start", "worker", "--", "worker"}, {"proc", "acme", "start", "worker", "--placement", "--", "worker"}} {
		r = run(t, raw, Scenario{Args: args, Files: served, Rules: running("acme", stillStarting, token, mcpRules[0], mcpRules[1])})
		if r.Exit != 1 || !strings.Contains(r.Stderr, "acme's container is still starting (try again in a few seconds)") ||
			callsMatching(r, regexp.MustCompile(`berth-placement`)) != 0 {
			t.Errorf("%v while the container starts: exit %d, stderr %q", args, r.Exit, r.Stderr)
		}
		r = run(t, raw, Scenario{Args: args, Files: served, Rules: running("acme", append([]Rule{none}, starting...)...)})
		if r.Exit != 0 {
			t.Errorf("%v once it is ready: exit %d, stderr %q", args, r.Exit, r.Stderr)
		}
	}

	// Several from stdin, and from --env-file; the values go on berth-proc's stdin.
	envFile := merge(twoOrgs, map[string]File{"home/api.env": {Content: "# the worker's\nAPI_URL=http://example.com/?a=b\n\n", Mode: 0o600}})
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--env", "API_KEY", "--env", "DEBUG", "--env-file", "api.env", "--", "worker"},
		Stdin: "DEBUG=1\nAPI_KEY=sk-proc-SECRET\n", Files: envFile, Rules: running("acme", starting...)})
	if r.Exit != 0 || slices.ContainsFunc(r.Calls, func(c string) bool {
		a, _, _ := strings.Cut(c, "  <stdin ")
		return strings.Contains(a, "sk-proc-SECRET")
	}) {
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
		"an empty stdin":     {[]string{"proc", "acme", "start", "worker", "--env", "API_KEY", "--", "worker"}, running("acme"), "stdin has no value for API_KEY", "\n"},
		"--env=NAME=value":   {[]string{"proc", "acme", "start", "worker", "--env=API_KEY=sk-proc-SECRET", "--", "worker"}, running("acme"), "--env API_KEY: a variable's value is never on the command line", ""},
		"--grace=N":          {[]string{"proc", "acme", "start", "worker", "--grace=5", "--", "worker"}, running("acme"), "--grace=: give --grace and its value as two arguments", ""},
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
	// With --placement, a name in use is refused before a token is written.
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--placement", "--", "worker"}, Files: served, Rules: running("acme", starting...)})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "a process named worker exists: stop it first") ||
		callsMatching(r, regexp.MustCompile(`berth-placement|berth-proc" "start"`)) != 0 {
		t.Errorf("--placement, a name in use: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}
	// berth-proc's own refusals: a name in use, and an image without berth-proc.
	r = run(t, raw, Scenario{Args: []string{"proc", "acme", "start", "worker", "--", "worker"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `berth-proc start worker$`, Stderr: "berth-proc: a process named worker exists: stop it first\n", Exit: 3}, mcpRules[0], mcpRules[1])})
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

// stillStarting is berth-proc in a container whose entrypoint hasn't made its directory yet.
var stillStarting = Rule{Bin: "docker", Match: `berth-proc (status|start worker)$`, Stderr: "berth-proc: the container is still starting: try again in a few seconds\n", Exit: 5}

// TestProcStartWhileStarting: over the API, a start right after the org's container starts, before
// its supervisor is ready, is 409 `starting` with a Retry-After, with and without placement, and
// writes no token.
func TestProcStartWhileStarting(t *testing.T) {
	token := Rule{Bin: "docker", Match: `berth-placement`, Stdin: true}
	c, _, snap := apiSession(t, twoOrgs, running("acme", append([]Rule{stillStarting, token}, mcpRules...)...))
	for _, body := range []string{`{"argv":["worker"]}`, `{"argv":["worker"],"placement":true}`} {
		var resp *http.Response
		for i := 0; ; i++ { // with placement, once serve's refresher has had its first round
			req, err := http.NewRequest("POST", c.Base+"/v1/orgs/acme/processes/worker", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if resp, err = c.HTTP.Do(req); err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if strings.Contains(string(b), "placement needs") && i < 50 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			var doc ops.ErrorDoc
			if resp.StatusCode != 409 || json.Unmarshal(b, &doc) != nil || doc.Kind != ops.KindStarting || doc.Schema != "berth.error/v1" || resp.Header.Get("Retry-After") != "3" {
				t.Errorf("%s: %d %q, Retry-After %q", body, resp.StatusCode, b, resp.Header.Get("Retry-After"))
			}
			break
		}
	}
	if r := snap(); callsMatching(r, regexp.MustCompile(`berth-placement`)) != 0 {
		t.Errorf("a token was written while the container starts:\n%s", strings.Join(r.Calls, "\n"))
	}
}

// TestPlacementReadOnlyServe: a `berth serve --read-only` keeps no placement tokens fresh, so it
// writes no heartbeat, and a start with placement on the command line beside it is refused before
// a token or a process is made (#216).
func TestPlacementReadOnlyServe(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "berth-ro-") // a socket's path must be short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	tool := allCalls()
	command := tool.Command
	tool.Command = func(run string, args []string) []string {
		return append(append(command(run, nil), "--read-only", "serve"), args...)
	}
	sess, err := env.Start(t.TempDir(), tool, Scenario{Args: []string{"--socket", sock}, Files: twoOrgs,
		Rules: running("acme", append([]Rule{{Bin: "docker", Match: `berth-placement`, Stdin: true}}, mcpRules...)...)})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Cmd.Process.Signal(os.Interrupt)
		_ = sess.Cmd.Wait()
	})
	c := apiclient.Unix(sock)
	for i := 0; ; i++ {
		if info, err := c.Info(context.Background()); err == nil {
			if !info.ReadOnly {
				t.Fatalf("the serve isn't read-only: %+v", info)
			}
			break
		} else if i > 100 {
			t.Fatalf("berth serve didn't start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // a refresher would have had its first round
	state := sess.Cmd.Args[2]
	if _, err := os.Stat(filepath.Join(state, "placement-serve.json")); !os.IsNotExist(err) {
		t.Fatalf("a read-only serve wrote a heartbeat: %v", err)
	}
	cli := exec.Command(sess.Cmd.Args[0], "--home", state, "proc", "acme", "start", "worker", "--placement", "--", "worker")
	cli.Dir, cli.Env = sess.Cmd.Dir, sess.Cmd.Env
	var stderr strings.Builder
	cli.Stderr = &stderr
	if err := cli.Run(); err == nil || !strings.Contains(stderr.String(), "placement needs") || !strings.Contains(stderr.String(), "serve (not --read-only)") {
		t.Errorf("--placement beside a read-only serve: %v, stderr %q", err, stderr.String())
	}
	r, err := sess.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if callsMatching(r, regexp.MustCompile(`berth-placement|berth-proc" "start"`)) != 0 {
		t.Errorf("a token or a process beside a read-only serve:\n%s", strings.Join(r.Calls, "\n"))
	}
}

// jwsClaims are the claims of the token a logged call got on its stdin.
func jwsClaims(t *testing.T, call string) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`<stdin "([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\\n">`).FindStringSubmatch(call)
	if m == nil {
		t.Fatalf("no token on stdin: %s", call)
	}
	b, err := base64.RawURLEncoding.DecodeString(m[2])
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
