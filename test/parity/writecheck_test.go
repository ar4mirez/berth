package parity

import (
	"strings"
	"testing"
)

// allCalls is berth with every call it makes in the result: the comparison with ccenv leaves
// berth's own calls out.
func allCalls() Tool {
	t := Berth(berthBin)
	t.Calls = nil
	return t
}

const dryRunPush = `docker "exec" "-u" "node" "-w" "/workspace/widget" "claude-acme" "git" "push" "--dry-run" "--quiet" "origin" "HEAD:refs/heads/berth-write-check"`

// TestBerthRepoAddWriteCheck: after a repo is cloned (or found already there), berth asks its
// remote, with a dry-run push, whether the org can write to it, and warns when it can't (#103;
// ccenv says nothing, PARITY.md). A clone of a public repo needs no write access, so without this
// the first sign is a failed push from inside the container. The add still succeeds.
func TestBerthRepoAddWriteCheck(t *testing.T) {
	refused := func(stderr string) Rule {
		return Rule{Bin: "docker", Match: ` git push --dry-run `, Stderr: stderr, Exit: 1}
	}
	const deployKey = "ERROR: Permission to acme/widget.git denied to deploy key\nfatal: Could not read from remote repository.\n"
	const viaSSH = `WARNING: acme can clone github.com/acme/widget but can't push to it: ERROR: Permission to acme/widget.git denied to deploy key
  git uses the org's SSH key there. A key added as a deploy key writes to one repo only.
  Fix: <tool> gh-login acme (git then uses that login's token for every registered repo),
       or add the key to the GitHub account instead of as a deploy key (<tool> info acme prints it).
`
	const viaToken = `WARNING: acme can clone github.com/acme/widget but can't push to it: remote: Permission to acme/widget.git denied to octo-acme.
  git uses the gh login's token there, and that account has no write access to the repo.
  Fix: give the account write access, or sign in with one that has it: <tool> gh-login acme --force
`
	for _, c := range []struct {
		name   string
		rules  []Rule
		stderr string
	}{
		{"writable", cloneRules("widget", 0), ""},
		{"a deploy key of another repo", append(cloneRules("widget", 0), refused(deployKey)), viaSSH},
		{"an image without git-transport", append(cloneRules("widget", 0), refused(deployKey),
			Rule{Bin: "docker", Match: `git-transport show$`, Stderr: "executable file not found\n", Exit: 127}), viaSSH},
		{"a token without push access", append(cloneRules("widget", 0),
			refused("remote: Permission to acme/widget.git denied to octo-acme.\nfatal: unable to access 'https://github.com/acme/widget.git/': The requested URL returned error: 403\n"),
			Rule{Bin: "docker", Match: `git-transport show$`, Stdout: "https\n"}), viaToken},
		// Not a refusal: an empty repo has no HEAD to push, and a network failure says nothing about access.
		{"an empty repo", append(cloneRules("widget", 0), refused("error: src refspec HEAD does not match any\nerror: failed to push some refs to 'origin'\n")), ""},
		{"no network", append(cloneRules("widget", 0), refused("ssh: Could not resolve hostname github.com\nfatal: Could not read from remote repository.\n")), ""},
	} {
		r := run(t, allCalls(), Scenario{Args: []string{"repo", "add", "acme", "acme/widget"}, Files: twoOrgs, Rules: c.rules})
		// "Cloning..." is the fake clone's own stderr.
		if r.Exit != 0 || r.Stderr != "Cloning...\n"+c.stderr || !strings.HasSuffix(r.Stdout, "Cloned into /workspace/widget\n") {
			t.Errorf("%s: exit %d\nstdout %q\nstderr %q\n  want %q", c.name, r.Exit, r.Stdout, r.Stderr, c.stderr)
		}
		wantCall(t, r, dryRunPush)
	}

	// Already in the workspace: checked too. Not cloned (stopped, --no-clone, a failed clone): not asked.
	r := run(t, allCalls(), Scenario{Args: []string{"repo", "add", "acme", "acme/widget"}, Files: twoOrgs,
		Rules: running("acme", refused(deployKey))})
	if r.Exit != 0 || r.Stderr != viaSSH || !strings.HasSuffix(r.Stdout, "/workspace/widget already present\n") {
		t.Errorf("already present: exit %d\nstdout %q\nstderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	for name, sc := range map[string]Scenario{
		"stopped":      {Args: []string{"repo", "add", "globex", "acme/widget"}, Files: twoOrgs},
		"--no-clone":   {Args: []string{"repo", "add", "acme", "acme/widget", "--no-clone"}, Files: twoOrgs, Rules: running("acme")},
		"clone failed": {Args: []string{"repo", "add", "acme", "acme/widget"}, Files: twoOrgs, Rules: cloneRules("widget", 128)},
	} {
		r := run(t, allCalls(), sc)
		for _, call := range r.Calls {
			if strings.Contains(call, `"push"`) {
				t.Errorf("%s: asked the remote: %s", name, call)
			}
		}
	}
}

// TestBerthGhLoginGitTransport: once gh is signed in, berth has the container choose how git
// reaches github.com (#103), and says so when that is the login's token. An image from before
// #103 has no git-transport: nothing is said, and gh-login still succeeds.
func TestBerthGhLoginGitTransport(t *testing.T) {
	signedIn := Rule{Bin: "docker", Match: `gh api user -q`, Stdout: "octo-acme\n"}
	onAccount := Rule{Bin: "docker", Match: `gh api users/octo-acme/keys`, Stdout: "ssh-ed25519 AAAAFAKE\n"}
	const says = "acme: git reaches github.com with this login's token (registered repos only). To keep the org's SSH key instead: GIT_TRANSPORT=ssh in org.env\n"
	for _, c := range []struct {
		name  string
		rules []Rule
		want  string
	}{
		{"https", []Rule{{Bin: "docker", Match: `git-transport show$`, Stdout: "https\n"}}, says},
		{"ssh (GIT_TRANSPORT=ssh)", []Rule{{Bin: "docker", Match: `git-transport show$`, Stdout: "ssh\n"}}, ""},
		{"an image without git-transport", []Rule{{Bin: "docker", Match: `git-transport `, Stderr: "executable file not found\n", Exit: 127}}, ""},
	} {
		r := run(t, allCalls(), Scenario{Args: []string{"gh-login", "acme"}, Files: twoOrgs, Rules: running("acme", append(c.rules, signedIn, onAccount)...)})
		want := "acme: gh already signed in as octo-acme (use --force to sign in again)\n" + c.want
		if r.Exit != 0 || r.Stdout != want || r.Stderr != "" {
			t.Errorf("%s: exit %d\nstdout %q\n  want %q\nstderr %q", c.name, r.Exit, r.Stdout, want, r.Stderr)
		}
		wantCall(t, r, `docker "exec" "claude-acme" "git-transport" "apply"`)
	}
}
