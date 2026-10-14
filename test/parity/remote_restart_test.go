package parity

import (
	"strings"
	"testing"
)

// TestBerthRemoteRestart: `remote restart` stops `claude remote-control` (not the runuser it runs
// under), waits until it has exited, then asks the supervisor to try again now and waits for its
// log to show a new attempt (#7, #187, PARITY.md). ccenv's pkill alone does nothing while the
// supervisor waits out a blocked-by-policy hour, and its retry at once started a second Remote
// Control beside the one still exiting. A restart that restarted nothing says so.
func TestBerthRemoteRestart(t *testing.T) {
	starts := func(before, after string) []Rule {
		return []Rule{
			{Bin: "docker", Match: `grep -ac "starting remote-control"`, Stdout: before, Once: true},
			{Bin: "docker", Match: `grep -ac "starting remote-control"`, Stdout: after},
		}
	}
	// left is the stopped one still running, for n checks.
	left := func(n int) []Rule {
		var r []Rule
		for range n {
			r = append(r, Rule{Bin: "docker", Match: `^exec claude-acme sh -c pgrep -f '\^claude remote-control' \|\| true$`, Stdout: "4242\n", Once: true})
		}
		return r
	}
	const restarting = "Restarting; back in ~5s.\n"
	const kill = `docker "exec" "claude-acme" "pkill" "-f" "^claude remote-control"`
	const check = `docker "exec" "claude-acme" "sh" "-c" "pgrep -f '^claude remote-control' || true"`
	const retry = `docker "exec" "claude-acme" "touch" "/run/rc-retry"`

	// The supervisor tries again: ccenv's line, and nothing more.
	r := run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs,
		Rules: running("acme", append(starts("3\n", "4\n"), Rule{Bin: "docker", Match: `^exec claude-acme pkill`, Exit: 1})...)})
	if r.Exit != 0 || r.Stdout != restarting || r.Stderr != "" {
		t.Errorf("restart: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	calls := strings.Join(r.Calls, "\n")
	if k, c, rt := strings.Index(calls, kill), strings.Index(calls, check), strings.Index(calls, retry); k < 0 || c < k || rt < c || strings.Count(calls, `sleep "1"`) != 1 {
		t.Errorf("restart: the pkill, the check that it exited, the retry request, then one check of the log:\n%s", calls)
	}

	// The one stopped takes two seconds to exit: the retry is asked for only then.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs, Rules: running("acme", append(left(2), starts("3\n", "4\n")...)...)})
	calls = strings.Join(r.Calls, "\n")
	if r.Exit != 0 || strings.Count(calls, check) != 3 || strings.Count(calls, `sleep "1"`) != 3 || strings.LastIndex(calls, check) > strings.Index(calls, retry) {
		t.Errorf("slow exit: exit %d, stderr %q\n%s", r.Exit, r.Stderr, calls)
	}

	// One that never exits is killed after a minute.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs, Rules: running("acme", append(left(100), starts("3\n", "4\n")...)...)})
	calls = strings.Join(r.Calls, "\n")
	if r.Exit != 0 || r.Stderr != "<tool>: Remote Control in acme didn't exit within 60 seconds; killing it\n" ||
		strings.Index(calls, `"pkill" "-KILL" "-f" "^claude remote-control"`) > strings.Index(calls, retry) || strings.Count(calls, `sleep "1"`) != 62 {
		t.Errorf("no exit: exit %d, stderr %q\n%s", r.Exit, r.Stderr, calls)
	}

	// No new attempt in thirty seconds: an error that says where to look.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs, Rules: running("acme", starts("3\n", "3\n")...)})
	want := "<tool>: Remote Control didn't start again in acme within 30 seconds (see <tool> remote acme logs; <tool> restart acme recreates the container)\n"
	if r.Exit != 1 || r.Stdout != restarting || r.Stderr != want {
		t.Errorf("no retry: exit %d, stdout %q\nstderr %q\n  want %q", r.Exit, r.Stdout, r.Stderr, want)
	}
	if n := strings.Count(strings.Join(r.Calls, "\n"), `sleep "1"`); n != 30 {
		t.Errorf("no retry: waited %d times, want 30", n)
	}

	// --fresh: bridge-pointer.json is moved aside once Remote Control has exited, before the retry.
	moved := "/home/node/.claude/projects/-workspace/bridge-pointer.json.stale-20261014T155600Z"
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart", "--fresh"}, Files: twoOrgs, Rules: running("acme", append(starts("3\n", "4\n"),
		Rule{Bin: "docker", Match: `^exec claude-acme sh -c cat /home/node/\.claude/projects/-workspace/bridge-pointer\.json`, Stdout: `{"sessionId":"session_01","environmentId":"env_STALE","pid":7}` + "\n"},
		Rule{Bin: "docker", Match: `^exec -u node claude-acme sh -c p=/home/node/\.claude/projects/-workspace/bridge-pointer\.json; `, Stdout: moved + "\n"})...)})
	calls = strings.Join(r.Calls, "\n")
	mv := strings.Index(calls, `"-u" "node" "claude-acme" "sh" "-c" "p=/home/node/.claude/projects/-workspace/bridge-pointer.json; [ -e \"$p\" ] || exit 0; s=\"$p.stale-$(date -u +%Y%m%dT%H%M%SZ)\"; mv \"$p\" \"$s\" && echo \"$s\""`)
	wantOut := restarting + "Moved /home/node/.claude/projects/-workspace/bridge-pointer.json aside to bridge-pointer.json.stale-20261014T155600Z: Remote Control registers a new environment instead of env_STALE.\n"
	if r.Exit != 0 || r.Stdout != wantOut || mv < strings.Index(calls, check) || mv > strings.Index(calls, retry) {
		t.Errorf("--fresh: exit %d, stdout %q, stderr %q\n%s", r.Exit, r.Stdout, r.Stderr, calls)
	}
	// Without the file, there's nothing to move.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart", "--fresh"}, Files: twoOrgs, Rules: running("acme", starts("3\n", "4\n")...)})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "No /home/node/.claude/projects/-workspace/bridge-pointer.json: Remote Control registers a new environment.") {
		t.Errorf("--fresh, no file: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	// Any other flag is refused before anything runs.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart", "--hard"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "unknown flag --hard") || strings.Contains(strings.Join(r.Calls, "\n"), "pkill") {
		t.Errorf("--hard: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}

	// No login: the supervisor has nothing to start.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `test -f /home/node/\.claude/\.credentials\.json$`, Exit: 1})})
	if r.Exit != 1 || r.Stderr != "<tool>: Remote Control didn't start in acme: it has no login (<tool> login acme)\n" {
		t.Errorf("no login: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// REMOTE_CONTROL=0: nothing is supervised, and nothing is run.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Rules: running("acme"),
		Files: withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: acmeEnv + "REMOTE_CONTROL=0\n"})})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "Remote Control is off for acme (REMOTE_CONTROL=0 in its org.env)") || strings.Contains(strings.Join(r.Calls, "\n"), "pkill") {
		t.Errorf("off: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}

	// login and logout ask for the retry too, after ccenv's pkill, once Remote Control has exited.
	r = run(t, allCalls(), Scenario{Args: []string{"logout", "acme"}, Files: twoOrgs, Rules: running("acme", left(1)...)})
	calls = strings.Join(r.Calls, "\n")
	if r.Exit != 0 || strings.Count(calls, check) != 2 || strings.LastIndex(calls, check) > strings.Index(calls, retry) {
		t.Errorf("logout: exit %d\n%s", r.Exit, calls)
	}

	// remote status says when bridge-pointer.json names another environment than the log shows.
	pointer := func(env string) Rule {
		return Rule{Bin: "docker", Match: `^exec claude-acme sh -c cat /home/node/\.claude/projects/-workspace/bridge-pointer\.json`, Stdout: `{"environmentId":"` + env + `"}`}
	}
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "status"}, Files: twoOrgs, Rules: rcUp("  Capacity: 1/8\n", pointer("env_STALE"))})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "  Note:  its bridge-pointer.json names environment env_STALE, which it reuses at its next start. If an environment shows offline in the Claude app: <tool> remote acme restart --fresh\n") {
		t.Errorf("status, another environment: exit %d, stdout %q", r.Exit, r.Stdout)
	}
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "status"}, Files: twoOrgs, Rules: rcUp("  Capacity: 1/8\n", pointer("env_PARITY01"))})
	if r.Exit != 0 || strings.Contains(r.Stdout, "Note:") {
		t.Errorf("status, the same environment: exit %d, stdout %q", r.Exit, r.Stdout)
	}
}
