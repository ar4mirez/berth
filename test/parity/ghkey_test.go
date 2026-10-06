package parity

import (
	"strings"
	"testing"
)

// TestBerthGhLoginGitKey: gh-login is ccenv's (same messages, same sign-in call), and then berth
// puts the org's git key on that GitHub account with the host's gh, or says how to (PARITY.md).
// The container's gh never adds it: its token has no admin:public_key scope.
func TestBerthGhLoginGitKey(t *testing.T) {
	const (
		already = "acme: gh already signed in as octo-acme (use --force to sign in again)\n"
		addCall = `gh "ssh-key" "add" "<RUN>/state/orgs/acme/ssh/id_ed25519.pub" "--title" "claude-acme@parity-host"`
		added   = "acme: git key added to octo-acme's GitHub as 'claude-acme@parity-host' (with the host's gh)\n"
		manual  = "acme: the git key isn't on octo-acme's GitHub yet, so clones over SSH are refused.\n" +
			"  -> Add it at https://github.com/settings/ssh/new, signed in as octo-acme:\n" +
			"     ssh-ed25519 AAAAFAKE claude-acme@parity-host\n"
		refused = "acme: the host's gh couldn't add the git key to octo-acme's GitHub (gh auth refresh -h github.com -s admin:public_key, then run this again).\n"
	)
	signedIn := Rule{Bin: "docker", Match: `gh api user -q`, Stdout: "octo-acme\n"}
	keys := func(stdout string) Rule {
		return Rule{Bin: "docker", Match: `^exec -u node claude-acme gh api users/octo-acme/keys -q`, Stdout: stdout}
	}
	hostIs := func(login string) Rule { return Rule{Bin: "gh", Match: `^api user -q \.login$`, Stdout: login + "\n"} }

	for _, c := range []struct {
		name   string
		args   []string
		rules  []Rule
		stdout string
		adds   bool
	}{
		{"already on the account", []string{"gh-login", "acme"},
			[]Rule{signedIn, keys("ssh-rsa AAAAOTHER\nssh-ed25519 AAAAFAKE\n"), hostIs("octo-acme")}, already, false},
		{"added with the host's gh", []string{"gh-login", "acme"},
			[]Rule{signedIn, keys("ssh-rsa AAAAOTHER\n"), hostIs("octo-acme")}, already + added, true},
		{"the account's keys can't be listed", []string{"gh-login", "acme"},
			[]Rule{signedIn, {Bin: "docker", Match: `gh api users/`, Stderr: "HTTP 403\n", Exit: 1}, hostIs("octo-acme")}, already + added, true},
		{"the host's gh is another account", []string{"gh-login", "acme"},
			[]Rule{signedIn, keys(""), hostIs("octo-globex")}, already + manual, false},
		{"no host gh", []string{"gh-login", "acme"},
			[]Rule{signedIn, keys(""), noHostGh}, already + manual, false},
		{"the host's gh is refused", []string{"gh-login", "acme"},
			[]Rule{signedIn, keys(""), hostIs("octo-acme"), {Bin: "gh", Match: `^ssh-key add `, Stderr: "HTTP 404\n", Exit: 1}},
			already + refused + manual, true},
		{"after signing in", []string{"gh-login", "acme"},
			[]Rule{{Bin: "docker", Match: `gh api user -q`, Once: true}, signedIn, keys(""), hostIs("octo-acme")},
			"\nacme: gh signed in as octo-acme\n" + added, true},
		{"--force", []string{"gh-login", "acme", "--force"},
			[]Rule{signedIn, keys("ssh-ed25519 AAAAFAKE\n")}, "\nacme: gh signed in as octo-acme\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := run(t, Berth(berthBin), Scenario{Args: c.args, Files: twoOrgs, Rules: running("acme", c.rules...)})
			if r.Exit != 0 || !strings.HasSuffix(r.Stdout, c.stdout) || r.Stderr != "" {
				t.Errorf("exit %d\nstdout %q\n  want suffix %q\nstderr %q", r.Exit, r.Stdout, c.stdout, r.Stderr)
			}
			if adds := strings.Contains(strings.Join(r.Calls, "\n"), addCall); adds != c.adds {
				t.Errorf("ssh-key add called: %v, want %v:\n%s", adds, c.adds, strings.Join(r.Calls, "\n"))
			}
			// The sign-in itself stays ccenv's: the container's gh gets no key and no key scope.
			if strings.HasPrefix(c.stdout, "\n") {
				wantCall(t, r, `"gh" "auth" "login" "--hostname" "github.com" "--git-protocol" "ssh" "--skip-ssh-key" "--web" "--scopes" "read:org,repo,workflow"`)
			}
		})
	}
}
