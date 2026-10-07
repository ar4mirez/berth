package parity

import (
	"strings"
	"testing"
)

// TestBerthEnvAccept: `berth env <org> accept` (#10, berth-only) takes the secrets dropped inside
// the org with berth-secret-drop and sets them as `env set` would. The names and values come out of
// the container, so each is checked before anything is stored; a value never appears in the output.
func TestBerthEnvAccept(t *testing.T) {
	list := func(names string) Rule {
		return Rule{Bin: "docker", Match: `^exec -u node claude-acme berth-secret-drop --list$`, Stdout: names}
	}
	show := func(key, val string) Rule {
		return Rule{Bin: "docker", Match: `^exec -u node claude-acme berth-secret-drop --show ` + key + `$`, Stdout: val}
	}
	envLine := func(r Result) string { return treeLine(r, "state/orgs/acme/org.env") }
	const secret = "sk-or-DROPPED"

	// Two drops: both are stored, listed in CCENV_ENV_KEYS, removed from the drop, and the org restarts.
	r := run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept"}, Files: twoOrgs,
		Rules: running("acme", list("OPENROUTER_API_KEY\nSENTRY_DSN\n"), show("OPENROUTER_API_KEY", secret), show("SENTRY_DSN", "https://k@sentry.example/1"))})
	if r.Exit != 0 || r.Stdout != "accepted: OPENROUTER_API_KEY\naccepted: SENTRY_DSN\nclaude-acme recreated; the change is live (new sessions)\n" || r.Stderr != "" {
		t.Errorf("accept: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	if e := envLine(r); !strings.Contains(e, `OPENROUTER_API_KEY='`+secret+`'\n`) || !strings.Contains(e, `SENTRY_DSN='https://k@sentry.example/1'\n`) ||
		!strings.Contains(e, `CCENV_ENV_KEYS=OPENROUTER_API_KEY SENTRY_DSN\n`) {
		t.Errorf("org.env: %s", e)
	}
	calls := strings.Join(r.Calls, "\n")
	for _, want := range []string{`"berth-secret-drop" "--cancel" "OPENROUTER_API_KEY"`, `"berth-secret-drop" "--cancel" "SENTRY_DSN"`, `"up" "-d" "--force-recreate"`} {
		if !strings.Contains(calls, want) {
			t.Errorf("accept: no call with %s:\n%s", want, calls)
		}
	}
	if strings.Contains(r.Stdout+r.Stderr+calls, secret) {
		t.Error("the value is in the output or on a command line")
	}

	// One named key, --no-restart: only that one, and nothing restarts.
	r = run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept", "SENTRY_DSN", "--no-restart"}, Files: twoOrgs,
		Rules: running("acme", list("OPENROUTER_API_KEY\nSENTRY_DSN\n"), show("SENTRY_DSN", "dsn\n"))})
	if r.Exit != 0 || r.Stdout != "accepted: SENTRY_DSN\napplies on: <tool> restart acme\n" || strings.Contains(envLine(r), "OPENROUTER_API_KEY") ||
		!strings.Contains(envLine(r), `SENTRY_DSN='dsn'\n`) || strings.Contains(strings.Join(r.Calls, "\n"), `"compose"`) {
		t.Errorf("accept KEY --no-restart: exit %d, stdout %q\n%s", r.Exit, r.Stdout, envLine(r))
	}

	// --list shows the names, and which would be refused; it stores nothing.
	r = run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept", "--list"}, Files: twoOrgs,
		Rules: running("acme", list("OPENROUTER_API_KEY\nGH_TOKEN\nnot-a-key\n"))})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "OPENROUTER_API_KEY\nGH_TOKEN   (will be refused: GH_TOKEN is managed by <tool>;") || strings.Contains(r.Stdout, "not-a-key") ||
		strings.Contains(envLine(r), "OPENROUTER_API_KEY") {
		t.Errorf("--list: exit %d, stdout %q", r.Exit, r.Stdout)
	}

	// Refused, with nothing stored and nothing restarted: a name berth manages, and values that
	// aren't one line of text. The good drop beside it isn't accepted either.
	for name, c := range map[string]struct {
		names, key, val, want string
	}{
		"a reserved name":  {"OPENROUTER_API_KEY\nCLAUDE_CODE_OAUTH_TOKEN\n", "CLAUDE_CODE_OAUTH_TOKEN", "x", "CLAUDE_CODE_OAUTH_TOKEN is managed by <tool>"},
		"PATH":             {"OPENROUTER_API_KEY\nPATH\n", "PATH", "/evil", "PATH is managed by <tool>"},
		"two lines":        {"OPENROUTER_API_KEY\nMULTI\n", "MULTI", "a\nb\n", "isn't one line of text"},
		"a control char":   {"OPENROUTER_API_KEY\nCTRL\n", "CTRL", "a\x1b[0m", "isn't one line of text"},
		"a single quote":   {"OPENROUTER_API_KEY\nQUOTE\n", "QUOTE", "it's", "single quote"},
		"an empty value":   {"OPENROUTER_API_KEY\nEMPTY\n", "EMPTY", "\n", "is empty"},
		"too long a value": {"OPENROUTER_API_KEY\nHUGE\n", "HUGE", strings.Repeat("x", 16<<10+1), "longer than 16384 bytes"},
	} {
		r = run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept"}, Files: twoOrgs,
			Rules: running("acme", list(c.names), show("OPENROUTER_API_KEY", secret), show(c.key, c.val))})
		if r.Exit != 1 || !strings.Contains(r.Stderr, c.want) || !strings.Contains(r.Stderr, "nothing was accepted") || r.Stdout != "" ||
			strings.Contains(envLine(r), "OPENROUTER_API_KEY") || strings.Contains(strings.Join(r.Calls, "\n"), `"compose"`) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, r.Exit, r.Stdout, r.Stderr)
		}
	}

	// Nothing waiting; a key that wasn't dropped; a stopped org; an older container; --read-only.
	r = run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept"}, Files: twoOrgs, Rules: running("acme", list(""))})
	if r.Exit != 0 || r.Stdout != "Nothing is waiting in acme. In its terminal: berth-secret-drop KEY\n" {
		t.Errorf("nothing waiting: exit %d, stdout %q", r.Exit, r.Stdout)
	}
	r = run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept", "NOPE"}, Files: twoOrgs, Rules: running("acme", list("OPENROUTER_API_KEY\n"))})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "nothing dropped for NOPE in acme (waiting: OPENROUTER_API_KEY)") {
		t.Errorf("not dropped: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, allCalls(), Scenario{Args: []string{"env", "globex", "accept"}, Files: twoOrgs})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "claude-globex is not running") {
		t.Errorf("stopped: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, allCalls(), Scenario{Args: []string{"env", "acme", "accept"}, Files: twoOrgs,
		Rules: running("acme", Rule{Bin: "docker", Match: `berth-secret-drop --list$`, Stderr: "executable file not found\n", Exit: 127})})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "acme's container has no berth-secret-drop (it was started by an older berth)") {
		t.Errorf("older container: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, allCalls(), Scenario{Args: []string{"--read-only", "env", "acme", "accept"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "read-only") || len(r.Calls) != 0 {
		t.Errorf("--read-only: exit %d, stderr %q, calls %v", r.Exit, r.Stderr, r.Calls)
	}
}
