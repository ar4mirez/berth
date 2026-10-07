package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/ops"
)

// browserHint is ccenv's browser_hint, followed by its blank line.
func (a *App) browserHint(o string) {
	sayf(a.Stdout, `Sign in as the %[1]s Claude account. The flow runs inside the %[1]s container, so your host's Claude
session is not touched. Only your browser session matters:
  -> open the link in a PRIVATE/INCOGNITO window (or a browser profile for %[1]s),
     sign in to claude.ai with the %[1]s account, approve, and paste the code back here.

`, o)
}

// errNoNewline is `read` hitting end of input before a newline: under set -e, ccenv stops there
// with exit 1 and no message.
var errNoNewline = &Exit{Code: 1}

// readSecretLine is `read -r -s -p "<prompt>" v; echo`: on a terminal, the prompt (stderr) and a
// hidden read; otherwise one line from stdin, byte by byte so nothing after it is consumed. A last
// line without a newline is an error, as it is for bash's read under set -e, and then the trailing
// echo (to stdout) never runs.
func (a *App) readSecretLine(prompt string) (string, error) {
	line, err := a.readSecretLineRaw(prompt)
	if err == nil {
		fmt.Fprintln(a.Stdout)
	}
	return line, err
}

func (a *App) readSecretLineRaw(prompt string) (string, error) {
	if f, ok := a.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(a.Stderr, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		return string(b), err
	}
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := a.Stdin.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return string(line), nil
			}
			line = append(line, buf[0])
			continue
		}
		if errors.Is(err, io.EOF) {
			return string(line), errNoNewline
		}
		if err != nil {
			return "", err
		}
	}
}

// Token is `ccenv token <org> [--paste] [--no-restart]`: a 1-year token from `claude setup-token`
// in the org's container (captured through /dev/shm), or pasted.
func (a *App) Token(ctx context.Context, args []string) error {
	o, paste, restart := "", false, true
	for _, x := range args {
		switch x {
		case "--paste":
			paste = true
		case "--no-restart":
			restart = false
		default:
			o = x
		}
	}
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	t := ""
	if !paste {
		if err := a.needUp(ctx, o); err != nil {
			return err
		}
		a.browserHint(o)
		// A wide pty keeps the token on one line; the session is kept in RAM, then wiped.
		_ = a.execIn(ctx, true, []string{"-it", "-u", "node", "-w", "/workspace"}, o, "bash", "-c", `
      umask 077; log=$(mktemp -p /dev/shm); trap "rm -f $log" EXIT
      script -qfec "stty cols 2000 2>/dev/null; env -u CLAUDE_CODE_OAUTH_TOKEN claude setup-token" "$log" >/dev/tty
      grep -ao "sk-ant-oat[0-9A-Za-z_-]*" "$log" | tail -1 > /dev/shm/.ccenv-token`) // || true
		t, _ = a.capture(ctx, false, "docker", "exec", "claude-"+o, "sh", "-c", "cat /dev/shm/.ccenv-token 2>/dev/null; rm -f /dev/shm/.ccenv-token")
		if t == "" {
			fmt.Fprintln(a.Stdout, "\nCouldn't read the token from the output; paste it instead.")
		}
	}
	if t == "" {
		var err error
		if t, err = a.readSecretLine("Paste CLAUDE_CODE_OAUTH_TOKEN for " + o + ": "); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(t, "sk-ant-") {
		return errors.New("that doesn't look like a Claude token (sk-ant-...)")
	}
	if err := a.setSecret(o, "CLAUDE_CODE_OAUTH_TOKEN", t, t); err != nil {
		return err
	}
	sayf(a.Stdout, "Token saved for %s.\n", o)
	if restart && a.running(ctx, o) {
		sayf(a.Stdout, "Restarting claude-%s to apply it (open terminal sessions in it will reconnect)...\n", o)
		if err := a.quiet().compose(ctx, o, "up", "-d", "--force-recreate"); err != nil {
			return err // set -e: compose's exit code, output discarded
		}
		return a.passthrough(ctx, false, "sleep", "3")
	}
	return nil
}

// quiet is a copy of a with stdout and stderr discarded (`>/dev/null 2>&1`).
func (a *App) quiet() *App {
	q := *a
	q.Stdout, q.Stderr = io.Discard, io.Discard
	return &q
}

// Auth is `ccenv auth <org>`: both credentials in one go (token, then the full login).
func (a *App) Auth(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "== Step 1/2: long-lived token (used by every session)")
	if err := a.Token(ctx, []string{o}); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "\n== Step 2/2: full login (used by Remote Control / claude.ai)")
	fmt.Fprintln(a.Stdout, "Use the SAME private window: you're already signed in there, so it's one click.")
	fmt.Fprintln(a.Stdout)
	return a.Login(ctx, o)
}

const logoutScript = "env -u CLAUDE_CODE_OAUTH_TOKEN claude auth logout >/dev/null 2>&1; rm -f $CLAUDE_CONFIG_DIR/.credentials.json"

// Login is `ccenv login <org>`: the full login Remote Control needs, starting from a clean state,
// then Remote Control's status and a warning if another org uses the same Claude organization.
func (a *App) Login(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	if err := a.passthrough(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "sh", "-c", logoutScript); err != nil {
		return err
	}
	a.browserHint(o)
	if err := a.execIn(ctx, true, []string{"-it", "-u", "node", "-w", "/workspace"}, o, "env", "-u", "CLAUDE_CODE_OAUTH_TOKEN", "claude", "auth", "login"); err != nil {
		return err
	}
	a.pkillRemoteControl(ctx, o)
	fmt.Fprintln(a.Stdout, "Waiting for the Remote Control service...")
	for i := 0; i < 20; i++ {
		_ = a.passthrough(ctx, false, "sleep", "2")
		if a.rcRunning(ctx, o) && a.rcURL(ctx, o) != "" {
			break
		}
		if ops.RemoteBlocked(ctx, a, o) {
			break
		}
	}
	if err := a.Remote(ctx, o, "status"); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout)
	return a.checkAccount(ctx, o)
}

// pkillRemoteControl is `docker exec claude-$o pkill -f "claude remote-control" 2>/dev/null || true`,
// and then berth asks the supervisor to try again now (#7; PARITY.md): during its hour-long wait
// for a Claude organization that has Remote Control turned off, there is no process to kill.
func (a *App) pkillRemoteControl(ctx context.Context, o string) {
	q := *a
	q.Stderr = io.Discard
	_ = q.passthrough(ctx, false, "docker", "exec", "claude-"+o, "pkill", "-f", "claude remote-control")
	a.retryRemoteControl(ctx, o)
}

// retryRemoteControl ends the supervisor's wait: it starts Remote Control again within two seconds.
// A container from before #7 ignores the file.
func (a *App) retryRemoteControl(ctx context.Context, o string) {
	_ = a.quietRun(ctx, "docker", "exec", "claude-"+o, "touch", contract.RemoteRetry)
}

// remoteStarts is how many times the supervisor has started Remote Control, by its log.
func (a *App) remoteStarts(ctx context.Context, o string) int {
	out, _ := a.capture(ctx, true, "docker", "exec", "claude-"+o, "sh", "-c",
		`grep -ac "`+contract.RemoteControlStarting+`" `+contract.RemoteControlLog+` 2>/dev/null; true`)
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// remoteRestart is `ccenv remote <org> restart`, made to work (#7; PARITY.md). ccenv only kills the
// service, which does nothing while the supervisor waits out a blocked-by-policy hour. berth also
// asks the supervisor to try now, and then checks that it did: a restart that restarted nothing
// says so, and why.
func (a *App) remoteRestart(ctx context.Context, o string) error {
	if a.env(o, "REMOTE_CONTROL") == "0" {
		return &ops.Error{Kind: ops.KindState, Code: 1, Msg: "Remote Control is off for " + o + " (REMOTE_CONTROL=0 in its org.env)",
			Hint: "set REMOTE_CONTROL=1, then " + Tool + " restart " + o}
	}
	before := a.remoteStarts(ctx, o)
	_ = a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "pkill", "-f", "claude remote-control") // || true
	a.retryRemoteControl(ctx, o)
	fmt.Fprintln(a.Stdout, "Restarting; back in ~5s.")
	for i := 0; i < 10; i++ {
		_ = a.passthrough(ctx, false, "sleep", "1")
		if a.remoteStarts(ctx, o) > before {
			return nil
		}
	}
	if !ops.RemoteLoggedIn(ctx, a, o) {
		return &ops.Error{Kind: ops.KindState, Code: 1, Msg: "Remote Control didn't start in " + o + ": it has no login", Hint: Tool + " login " + o}
	}
	return &ops.Error{Kind: ops.KindState, Code: 1,
		Msg:  "Remote Control didn't start again in " + o + " within 10 seconds",
		Hint: "see " + Tool + " remote " + o + " logs; a container started by an older berth can't be asked to retry, and " + Tool + " restart " + o + " recreates it"}
}

// rcAccount is rc_account: "<orgId> <email> [<orgName>]" of the org's full login, or "".
func (a *App) rcAccount(ctx context.Context, o string) string {
	out, _ := a.captureRaw(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "sh", "-c",
		"env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status 2>/dev/null; true")
	lines, _ := ops.AccountLines([]byte(out))
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// checkAccount is check_account: warn if another running org is signed in to the same Claude
// organization, unless either sets SHARED_ACCOUNT=1.
func (a *App) checkAccount(ctx context.Context, o string) error {
	me := a.rcAccount(ctx, o)
	if me == "" {
		return nil
	}
	_, who, _ := strings.Cut(me, " ")
	fmt.Fprintln(a.Stdout, "Signed in as: "+who)
	if a.env(o, "SHARED_ACCOUNT") == "1" {
		return nil
	}
	myID, _, _ := strings.Cut(me, " ")
	for _, other := range a.allDirs() { // "$ORGS"/*/, whether or not they have an org.env
		if other == o || !a.running(ctx, other) || a.envWarn(other, "SHARED_ACCOUNT") == "1" {
			continue
		}
		theirs := a.rcAccount(ctx, other)
		if id, _, _ := strings.Cut(theirs, " "); theirs != "" && id == myID {
			sayf(a.Stdout, `
WARNING: '%[1]s' is using the same Claude organization as '%[2]s': %[3]s
  Intended?  Silence this: echo SHARED_ACCOUNT=1 >> orgs/%[1]s/org.env
  Mistake?   %[4]s logout %[1]s && %[4]s auth %[1]s. In the private window, check which account
             claude.ai shows first, and if you belong to several organizations, pick the right one
             on the organization screen during sign-in.
`, o, other, who, Tool)
		}
	}
	return nil
}

// allDirs is `for d in "$ORGS"/*/`: every non-hidden directory, in byte order.
func (a *App) allDirs() []string { return a.orgDirs() }

// envWarn is envval for an org dir that may lack org.env: grep's error goes to stderr, as in ccenv.
func (a *App) envWarn(o, key string) string {
	if !a.isFile(a.Orgs.EnvPath(o)) {
		sayf(a.Stderr, "grep: %s: No such file or directory\n", a.Orgs.EnvPath(o))
		return ""
	}
	return a.env(o, key)
}

// Logout is `ccenv logout <org> [--all]`: remove the Remote Control login (and with --all, the token).
func (a *App) Logout(ctx context.Context, o, flag string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	if a.running(ctx, o) {
		if err := a.passthrough(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "sh", "-c", logoutScript); err != nil {
			return err
		}
		a.pkillRemoteControl(ctx, o)
	} else if err := a.Host.FS.Remove(path.Join(a.Orgs.Dir, o, "claude", ".credentials.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sayf(a.Stdout, "Logged out %s (Remote Control login removed).\n", o)
	if flag == "--all" {
		if err := a.setSecret(o, "CLAUDE_CODE_OAUTH_TOKEN", "", ""); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "Token removed too.")
		if a.running(ctx, o) {
			if err := a.quiet().compose(ctx, o, "up", "-d", "--force-recreate"); err != nil {
				return err
			}
			sayf(a.Stdout, "Restarted claude-%s.\n", o)
		}
	}
	return nil
}

// ghAccount is gh_account: the container's gh login, or "".
func (a *App) ghAccount(ctx context.Context, o string) string {
	out, _ := a.capture(ctx, false, append([]string{"docker", "exec", "-u", "node", "claude-" + o}, a.execLoader(o, "sh", "-c", "gh api user -q .login 2>/dev/null; true")...)...)
	return out
}

// GhLogin is `ccenv gh-login <org> [--force]`: sign the gh CLI in inside the container (device code).
func (a *App) GhLogin(ctx context.Context, o, flag string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	if acct := a.ghAccount(ctx, o); acct != "" && flag != "--force" {
		sayf(a.Stdout, "%s: gh already signed in as %s (use --force to sign in again)\n", o, acct)
		a.gitTransport(ctx, o)
		a.ensureGitKey(ctx, o, acct)
		return nil
	}
	sayf(a.Stdout, `Signing the gh CLI in for %[1]s, inside its container (your host gh is not touched).
  -> Open https://github.com/login/device in a browser signed in to the GitHub account for %[1]s
     and enter the one-time code shown below.

`, o)
	// BROWSER=true: there's no browser in the container, so gh just prints the URL and code.
	if err := a.execIn(ctx, true, []string{"-it", "-u", "node", "-w", "/workspace", "-e", "BROWSER=true"}, o,
		"gh", "auth", "login", "--hostname", "github.com", "--git-protocol", "ssh", "--skip-ssh-key", "--web", "--scopes", "read:org,repo,workflow"); err != nil {
		return err
	}
	acct := a.ghAccount(ctx, o)
	if acct == "" {
		return fmt.Errorf("gh sign-in didn't complete for %s", o)
	}
	sayf(a.Stdout, "\n%s: gh signed in as %s\n", o, acct)
	a.gitTransport(ctx, o)
	a.ensureGitKey(ctx, o, acct)
	return nil
}

// gitTransport has the container choose how git reaches github.com now that gh is signed in
// (#103; berth-only, PARITY.md): over HTTPS with gh's token, unless org.env says GIT_TRANSPORT=ssh.
// The container checks every 30 seconds anyway; this makes it immediate. An image from before #103
// has no such script, and keeps SSH.
func (a *App) gitTransport(ctx context.Context, o string) {
	if _, err := a.capture(ctx, true, "docker", "exec", "claude-"+o, contract.GitTransport, "apply"); err != nil {
		return
	}
	if how, _ := a.capture(ctx, true, "docker", "exec", "claude-"+o, contract.GitTransport, "show"); how == "https" {
		sayf(a.Stdout, "%s: git reaches github.com with this login's token (registered repos only). To keep the org's SSH key instead: GIT_TRANSPORT=ssh in org.env\n", o)
	}
}

// ensureGitKey puts the org's git key on acct's GitHub, so clones over SSH work after gh-login
// (berth-only; PARITY.md). The container's gh token can't add keys, on purpose (--skip-ssh-key and
// no admin:public_key scope), so the upload uses the host's gh, and only when that is signed in as
// acct. Otherwise it says how to add the key. It never fails gh-login.
func (a *App) ensureGitKey(ctx context.Context, o, acct string) {
	pubPath := path.Join(a.Orgs.Dir, o, "ssh", "id_ed25519.pub")
	b, err := a.Host.FS.ReadFile(pubPath)
	pub := strings.Fields(string(b))
	if err != nil || len(pub) < 2 {
		return
	}
	// A user's public keys need no scope to list.
	onAccount, _ := a.capture(ctx, true, append([]string{"docker", "exec", "-u", "node", "claude-" + o},
		a.execLoader(o, "gh", "api", "users/"+acct+"/keys", "-q", ".[].key")...)...)
	for _, k := range strings.Split(onAccount, "\n") {
		if f := strings.Fields(k); len(f) >= 2 && f[0] == pub[0] && f[1] == pub[1] {
			return
		}
	}
	title := "claude-" + o
	if len(pub) > 2 {
		title = pub[2]
	}
	if a.hostGh(ctx) {
		if hostAcct, _ := a.capture(ctx, true, "gh", "api", "user", "-q", ".login"); hostAcct == acct {
			if _, err := a.capture(ctx, true, "gh", "ssh-key", "add", pubPath, "--title", title); err == nil {
				sayf(a.Stdout, "%s: git key added to %s's GitHub as '%s' (with the host's gh)\n", o, acct, title)
				return
			}
			sayf(a.Stdout, "%s: the host's gh couldn't add the git key to %s's GitHub (gh auth refresh -h github.com -s admin:public_key, then run this again).\n", o, acct)
		}
	}
	sayf(a.Stdout, `%[1]s: the git key isn't on %[2]s's GitHub yet, so clones over SSH are refused.
  -> Add it at https://github.com/settings/ssh/new, signed in as %[2]s:
     %[3]s
`, o, acct, strings.Join(pub, " "))
}
