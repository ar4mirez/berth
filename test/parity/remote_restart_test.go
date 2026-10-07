package parity

import (
	"strings"
	"testing"
)

// TestBerthRemoteRestart: `remote restart` kills the service as ccenv does, then asks the
// supervisor to try again now and waits for its log to show a new attempt (#7, PARITY.md). ccenv's
// pkill alone does nothing while the supervisor waits out a blocked-by-policy hour. A restart that
// restarted nothing says so.
func TestBerthRemoteRestart(t *testing.T) {
	starts := func(before, after string) []Rule {
		return []Rule{
			{Bin: "docker", Match: `grep -ac "starting remote-control"`, Stdout: before, Once: true},
			{Bin: "docker", Match: `grep -ac "starting remote-control"`, Stdout: after},
		}
	}
	const restarting = "Restarting; back in ~5s.\n"

	// The supervisor tries again: ccenv's line, and nothing more.
	r := run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs,
		Rules: running("acme", append(starts("3\n", "4\n"), Rule{Bin: "docker", Match: `^exec claude-acme pkill`, Exit: 1})...)})
	if r.Exit != 0 || r.Stdout != restarting || r.Stderr != "" {
		t.Errorf("restart: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	calls := strings.Join(r.Calls, "\n")
	kill, retry := strings.Index(calls, `docker "exec" "claude-acme" "pkill" "-f" "claude remote-control"`), strings.Index(calls, `docker "exec" "claude-acme" "touch" "/run/rc-retry"`)
	if kill < 0 || retry < kill || strings.Count(calls, `sleep "1"`) != 1 {
		t.Errorf("restart: the pkill, then the retry request, then one check:\n%s", calls)
	}

	// No new attempt in ten seconds: an error that says where to look.
	r = run(t, allCalls(), Scenario{Args: []string{"remote", "acme", "restart"}, Files: twoOrgs, Rules: running("acme", starts("3\n", "3\n")...)})
	want := "<tool>: Remote Control didn't start again in acme within 10 seconds (see <tool> remote acme logs; a container started by an older <tool> can't be asked to retry, and <tool> restart acme recreates it)\n"
	if r.Exit != 1 || r.Stdout != restarting || r.Stderr != want {
		t.Errorf("no retry: exit %d, stdout %q\nstderr %q\n  want %q", r.Exit, r.Stdout, r.Stderr, want)
	}
	if n := strings.Count(strings.Join(r.Calls, "\n"), `sleep "1"`); n != 10 {
		t.Errorf("no retry: waited %d times, want 10", n)
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

	// login and logout ask for the retry too, after ccenv's pkill.
	r = run(t, allCalls(), Scenario{Args: []string{"logout", "acme"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || !strings.Contains(strings.Join(r.Calls, "\n"), `docker "exec" "claude-acme" "touch" "/run/rc-retry"`) {
		t.Errorf("logout: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}
}
