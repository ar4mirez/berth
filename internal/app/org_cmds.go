package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	"golang.org/x/term"

	"github.com/ar4mirez/berth/internal/host"
)

// writable is the check a writing subcommand of a reading command makes: not under --read-only,
// and the org is berth's.
func (a *App) writable(o, what string) error {
	if err := a.State.Writable(what); err != nil {
		return err
	}
	return a.needOwnedOrg(o)
}

// cutField2 is `cut -d: -f2- f`: per line, everything after the first ':', or the whole line if it
// has none; as `$( )`, trailing newlines stripped.
func cutField2(b []byte) string {
	var out []string
	for _, l := range fileLines(b) {
		if _, after, ok := strings.Cut(l, ":"); ok {
			l = after
		}
		out = append(out, l)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// Password is `ccenv password <org> [show|rotate]`.
func (a *App) Password(ctx context.Context, o, sub string) error {
	if sub == "" {
		sub = "show"
	}
	if sub == "rotate" {
		if err := a.writable(o, "rotate the password"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	f := path.Join(a.Orgs.Dir, o, "config", "secrets", "ttyd_credential")
	switch sub {
	case "show":
		b, err := a.Host.FS.ReadFile(f)
		if err != nil || len(b) == 0 { // [ -s "$f" ]
			return fmt.Errorf("no password set (%s password %s rotate)", Tool, o)
		}
		fmt.Fprintf(a.Stdout, "user: node\npass: %s\n", cutField2(b))
		return nil
	case "rotate":
		pw, err := newPassword()
		if err != nil {
			return err
		}
		if err := a.writeSecret(f, "node:"+pw); err != nil {
			return err
		}
		if a.running(ctx, o) {
			_ = a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "pkill", "-x", "ttyd") // || true
		}
		b, _ := a.Host.FS.ReadFile(f)
		fmt.Fprintf(a.Stdout, "Rotated; the browser terminal restarts with it in ~3s.\npass: %s\n", cutField2(b))
		return nil
	}
	return fmt.Errorf("usage: %s password <org> [show|rotate]", Tool)
}

// envKey is what env set/unset accept as a KEY.
var envKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// nth is "${n:-}": the nth argument, or "".
func nth(args []string, n int) string {
	if n < len(args) {
		return args[n]
	}
	return ""
}

// envReserved is ccenv's ENV_RESERVED: keys `env set` refuses.
var envReserved = strings.Fields(`ORG ORG_DIR HOST_UID HOST_GID MANAGER CCENV_ENV_KEYS CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY GH_TOKEN
  GIT_USER_NAME GIT_USER_EMAIL BIND_ADDR SSH_PORT TTYD_PORT REMOTE_CONTROL REMOTE_CAPACITY REPO_POLICY MEM_LIMIT CPUS
  SHARED_ACCOUNT IMAGE_TAG CLAUDE_CODE_VERSION CLAUDE_CONFIG_DIR DISABLE_AUTOUPDATER LANG PATH HOME USER SHELL`)

// berth sets these for compose too, so they are reserved as well (PARITY.md).
var berthReserved = []string{"CLAUDE_ENV_IMAGE", "CLAUDE_ENV_IMAGE_DIR"}

// Env is `ccenv env <org> [ls | set KEY | unset KEY] [--no-restart]`: custom env vars for the
// container, listed in CCENV_ENV_KEYS (the entrypoint snapshots them for SSH sessions).
func (a *App) Env(ctx context.Context, args []string) error {
	o, sub, key := nth(args, 0), nth(args, 1), nth(args, 2)
	if sub == "" {
		sub = "ls"
	}
	if sub == "set" || sub == "unset" || sub == "rm" {
		if err := a.writable(o, "change "+o+"'s env"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	f := a.Orgs.EnvPath(o)
	restart := nth(args, 3) != "--no-restart" // ccenv only looks at the 4th argument
	keys := a.env(o, "CCENV_ENV_KEYS")
	data, _ := a.Host.FS.ReadFile(f)
	hasLine := func(k string) bool {
		for _, l := range fileLines(data) {
			if strings.HasPrefix(l, k+"=") {
				return true
			}
		}
		return false
	}
	switch sub {
	case "ls", "list":
		if keys == "" {
			fmt.Fprintf(a.Stdout, "(no custom variables; add one: %s env %s set KEY)\n", Tool, o)
			return nil
		}
		for _, k := range strings.Fields(keys) {
			if hasLine(k) {
				fmt.Fprintln(a.Stdout, k)
			} else {
				fmt.Fprintln(a.Stdout, k+"  (listed but missing)")
			}
		}
		return nil
	case "set", "unset", "rm":
		if !envKey.MatchString(key) {
			return fmt.Errorf("usage: %s env %s %s KEY   (KEY like OPENROUTER_API_KEY)", Tool, o, sub)
		}
		for _, r := range append(envReserved, berthReserved...) {
			if r == key {
				return fmt.Errorf("%s is managed by %s; edit it in %s if you really mean to", key, Tool, f)
			}
		}
	default:
		return fmt.Errorf("usage: %s env <org> [ls | set KEY | unset KEY] [--no-restart]", Tool)
	}
	listed := strings.Fields(keys)
	contains := func(k string) bool {
		for _, x := range listed {
			if x == k {
				return true
			}
		}
		return false
	}
	if sub == "set" {
		val, err := a.readValue(key)
		if err != nil {
			return err
		}
		if val == "" {
			return errors.New("empty value; nothing set")
		}
		if strings.Contains(val, "'") {
			return errors.New("values containing a single quote (') aren't supported")
		}
		if err := a.Orgs.Set(o, key, "'"+val+"'"); err != nil {
			return err
		}
		if !contains(key) {
			listed = append(listed, key)
		}
		fmt.Fprintln(a.Stdout, "set: "+key)
	} else {
		if !hasLine(key) && !contains(key) {
			return fmt.Errorf("%s is not set for %s", key, o)
		}
		// awk '$0 !~ "^"KEY"="' f > tmp; cat tmp > f
		var kept strings.Builder
		for _, l := range fileLines(data) {
			if !strings.HasPrefix(l, key+"=") {
				kept.WriteString(l + "\n")
			}
		}
		if err := a.Host.FS.WriteFile(f, []byte(kept.String()), 0o600); err != nil {
			return err
		}
		var rest []string
		for _, k := range listed {
			if k != key {
				rest = append(rest, k)
			}
		}
		listed = rest
		fmt.Fprintln(a.Stdout, "removed: "+key)
	}
	if err := a.Orgs.Set(o, "CCENV_ENV_KEYS", strings.Join(listed, " ")); err != nil {
		return err
	}
	if restart && a.running(ctx, o) {
		// compose … >/dev/null 2>&1 && echo …: a failure ends the command with exit 1, silently.
		q := *a
		q.Stdout, q.Stderr = io.Discard, io.Discard
		if err := q.compose(ctx, o, "up", "-d", "--force-recreate"); err != nil {
			return &Exit{Code: 1}
		}
		fmt.Fprintf(a.Stdout, "claude-%s recreated; the change is live (new sessions)\n", o)
		return nil
	}
	fmt.Fprintf(a.Stdout, "applies on: %s restart %s\n", Tool, o)
	return nil
}

// readValue is env set's value: a hidden prompt on a terminal, else one line of stdin
// (`IFS= read -r val`, a last line without a newline included).
func (a *App) readValue(key string) (string, error) {
	if f, ok := a.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprintf(a.Stderr, "Value for %s (hidden): ", key)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}

// rcLog is rc_log: the last n non-blank lines of the Remote Control log, ANSI stripped.
func (a *App) rcLog(ctx context.Context, o string, n int, stdout io.Writer) error {
	script := fmt.Sprintf(`sed -e 's/\x1b\][^\x1b]*\x1b\\//g' -e 's/\x1b\[[0-9;]*[A-Za-z]//g' /home/node/.claude/remote-control.log 2>/dev/null | grep -av '^\s*$' | tail -%d`, n)
	return a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "exec", "claude-" + o, "sh", "-c", script}, Stdout: stdout, Stderr: a.Stderr})
}

// Remote is `ccenv remote <org> [status|logs|restart]`.
func (a *App) Remote(ctx context.Context, o, sub string) error {
	if sub == "" {
		sub = "status"
	}
	if sub == "restart" {
		if err := a.writable(o, "restart Remote Control"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	switch sub {
	case "status":
		if !a.rcLoggedIn(ctx, o) {
			fmt.Fprintf(a.Stdout, "Remote Control: not logged in. Run: %s login %s\n", Tool, o)
			return nil
		}
		switch {
		case a.rcRunning(ctx, o):
			fmt.Fprintln(a.Stdout, "Remote Control: running")
			fmt.Fprintln(a.Stdout, "  Open:  "+a.rcURL(ctx, o))
			fmt.Fprintf(a.Stdout, "  Or:    claude.ai/code or the Claude app -> pick environment '%s' -> New session\n", o)
			// rc_log 200 | grep -a Capacity | tail -1 | sed 's/^ */  /': under pipefail, no Capacity
			// line (grep exits 1) or a failing docker ends the command there, with exit 1.
			var log strings.Builder
			err := a.rcLog(ctx, o, 200, &log)
			last := ""
			for _, l := range fileLines([]byte(log.String())) {
				if strings.Contains(l, "Capacity") {
					last = l
				}
			}
			if last != "" {
				fmt.Fprintln(a.Stdout, "  "+strings.TrimLeft(last, " "))
			}
			if err != nil || last == "" {
				return &Exit{Code: 1}
			}
		case a.rcBlocked(ctx, o):
			fmt.Fprintf(a.Stdout, `Remote Control: BLOCKED by the Claude organization's policy for this account.
  An admin of that Claude organization must enable Remote Control. The service retries hourly and
  recovers on its own. Meanwhile use SSH, the browser terminal, VS Code or '%[1]s attach %[2]s'.
  To stop trying: set REMOTE_CONTROL=0 in orgs/%[2]s/org.env, then %[1]s restart %[2]s
`, Tool, o)
		default:
			fmt.Fprintf(a.Stdout, "Remote Control: restarting (see: %s remote %s logs)\n", Tool, o)
		}
		return nil
	case "logs":
		return a.rcLog(ctx, o, 40, a.Stdout)
	case "restart":
		_ = a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "pkill", "-f", "claude remote-control") // || true
		fmt.Fprintln(a.Stdout, "Restarting; back in ~5s.")
		return nil
	}
	return fmt.Errorf("usage: %s remote <org> [status|logs|restart]", Tool)
}
