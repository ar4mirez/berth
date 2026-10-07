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
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
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
		sayf(a.Stdout, "user: node\npass: %s\n", cutField2(b))
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
		sayf(a.Stdout, "Rotated; the browser terminal restarts with it in ~3s.\npass: %s\n", cutField2(b))
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
	if sub == "set" || sub == "unset" || sub == "rm" || sub == "accept" {
		if err := a.writable(o, "change "+o+"'s env"); err != nil {
			return err
		}
		if sub == "accept" {
			return a.envAccept(ctx, o, args[2:])
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	f := a.Orgs.EnvPath(o)
	restart := nth(args, 3) != "--no-restart" // ccenv only looks at the 4th argument
	keys := a.env(o, contract.EnvKeys)
	data, _ := a.Host.FS.ReadFile(f)
	hasLine := func(k string) bool {
		if a.migrated(o) && a.exists(path.Join(a.secretsDir(o), k)) {
			return true // kept as a file (#37)
		}
		for _, l := range fileLines(data) {
			if strings.HasPrefix(l, k+"=") {
				return true
			}
		}
		return false
	}
	switch sub {
	case "ls", "list":
		vars, err := ops.GetEnv(a, o)
		if err != nil {
			return err
		}
		if a.Output == OutputJSON {
			return a.writeJSON(vars)
		}
		if keys == "" {
			sayf(a.Stdout, "(no custom variables; add one: %s env %s set KEY)\n", Tool, o)
			return nil
		}
		for _, v := range vars.Vars {
			if v.Present {
				fmt.Fprintln(a.Stdout, v.Name)
			} else {
				fmt.Fprintln(a.Stdout, v.Name+"  (listed but missing)")
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
	// The edit runs under the state root's lock, re-reading org.env; never across the value prompt
	// or the restart.
	unlock := func() {}
	defer func() { unlock() }()
	relock := func() error {
		u, err := a.lock(ctx)
		if err != nil {
			return err
		}
		unlock = u
		data, _ = a.Host.FS.ReadFile(f)
		listed = strings.Fields(a.env(o, contract.EnvKeys))
		return nil
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
		if err := relock(); err != nil {
			return err
		}
		if err := a.setSecret(o, key, val, "'"+val+"'"); err != nil {
			return err
		}
		if !contains(key) {
			listed = append(listed, key)
		}
		fmt.Fprintln(a.Stdout, "set: "+key)
	} else {
		if err := relock(); err != nil {
			return err
		}
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
		if a.migrated(o) { // and its file (#37)
			if err := a.setSecret(o, key, "", ""); err != nil {
				return err
			}
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
	if err := a.Orgs.Set(o, contract.EnvKeys, strings.Join(listed, " ")); err != nil {
		return err
	}
	unlock()
	unlock = func() {}
	return a.applyEnv(ctx, o, restart)
}

// applyEnv is how env set and unset end: a running org is recreated so the change reaches its
// sessions, unless told not to.
func (a *App) applyEnv(ctx context.Context, o string, restart bool) error {
	if restart && a.running(ctx, o) {
		// compose … >/dev/null 2>&1 && echo …: a failure ends the command with exit 1, silently.
		q := *a
		q.Stdout, q.Stderr = io.Discard, io.Discard
		if err := q.compose(ctx, o, "up", "-d", "--force-recreate"); err != nil {
			return &Exit{Code: 1}
		}
		sayf(a.Stdout, "claude-%s recreated; the change is live (new sessions)\n", o)
		return nil
	}
	sayf(a.Stdout, "applies on: %s restart %s\n", Tool, o)
	return nil
}

// maxDropped is the longest value env accept takes from a drop.
const maxDropped = 16 << 10

// checkEnvKey refuses a name env set refuses: not a variable name, or one berth manages.
func (a *App) checkEnvKey(o, key string) error {
	if !envKey.MatchString(key) {
		return fmt.Errorf("'%s' isn't a variable name (KEY like OPENROUTER_API_KEY)", key)
	}
	for _, r := range append(envReserved, berthReserved...) {
		if r == key {
			return fmt.Errorf("%s is managed by %s; edit it in %s if you really mean to", key, Tool, a.Orgs.EnvPath(o))
		}
	}
	return nil
}

// envAccept is `berth env <org> accept [KEY...] [--list] [--no-restart]` (#10; berth-only): take
// the secrets dropped inside the org with berth-secret-drop, and set them as env set would. The
// value is typed in the org's own terminal and read here through the container, so it is never
// typed, pasted or passed on a command line on the host.
//
// What comes out of the container is not trusted: each name is checked as env set checks it, and
// each value must be one line of printable text. The drop is read inside the container (docker
// exec), never as a file of the host's, so a link planted in the drop folder can't make the host
// read a file of its own.
func (a *App) envAccept(ctx context.Context, o string, args []string) error {
	restart, list := true, false
	var want []string
	for _, x := range args {
		switch {
		case x == "--no-restart":
			restart = false
		case x == "--list":
			list = true
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("usage: %s env %s accept [KEY...] [--list] [--no-restart]", Tool, o)
		default:
			want = append(want, x)
		}
	}
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	out, err := a.capture(ctx, true, "docker", "exec", "-u", "node", "claude-"+o, contract.SecretDrop, "--list")
	if err != nil {
		return &ops.Error{Kind: ops.KindState, Code: 1, Msg: o + "'s container has no " + contract.SecretDrop + " (it was started by an older " + Tool + ")",
			Hint: Tool + " restart " + o + " recreates it, which stops the work running in it"}
	}
	var pending []string
	for _, k := range strings.Fields(out) {
		if envKey.MatchString(k) { // anything else isn't a drop: never echoed back
			pending = append(pending, k)
		}
	}
	if len(pending) == 0 {
		sayf(a.Stdout, "Nothing is waiting in %s. In its terminal: %s KEY\n", o, contract.SecretDrop)
		return nil
	}
	if list {
		for _, k := range pending {
			note := ""
			if err := a.checkEnvKey(o, k); err != nil {
				note = "   (will be refused: " + err.Error() + ")"
			}
			fmt.Fprintln(a.Stdout, k+note)
		}
		return nil
	}
	keys := pending
	if len(want) > 0 {
		keys = nil
		for _, k := range want {
			if !slices.Contains(pending, k) {
				return fmt.Errorf("nothing dropped for %s in %s (waiting: %s)", k, o, strings.Join(pending, " "))
			}
			keys = append(keys, k)
		}
	}
	// Every name and value is checked before anything is stored.
	vals := map[string]string{}
	for _, k := range keys {
		if err := a.checkEnvKey(o, k); err != nil {
			return fmt.Errorf("%w; nothing was accepted (in the org: %s --cancel %s)", err, contract.SecretDrop, k)
		}
		v, err := a.captureRaw(ctx, true, "docker", "exec", "-u", "node", "claude-"+o, contract.SecretDrop, "--show", k)
		if err != nil {
			return fmt.Errorf("couldn't read the drop for %s; nothing was accepted", k)
		}
		v = strings.TrimSuffix(v, "\n")
		switch {
		case v == "":
			return fmt.Errorf("the value dropped for %s is empty; nothing was accepted", k)
		case len(v) > maxDropped:
			return fmt.Errorf("the value dropped for %s is longer than %d bytes; nothing was accepted", k, maxDropped)
		case strings.Contains(v, "'"):
			return fmt.Errorf("the value dropped for %s has a single quote ('), which isn't supported; nothing was accepted", k)
		case strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }):
			return fmt.Errorf("the value dropped for %s isn't one line of text; nothing was accepted", k)
		}
		vals[k] = v
	}
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	listed := strings.Fields(a.env(o, contract.EnvKeys))
	for _, k := range keys {
		if err := a.setSecret(o, k, vals[k], "'"+vals[k]+"'"); err != nil {
			unlock()
			return err
		}
		if !slices.Contains(listed, k) {
			listed = append(listed, k)
		}
	}
	err = a.Orgs.Set(o, contract.EnvKeys, strings.Join(listed, " "))
	unlock()
	if err != nil {
		return err
	}
	for _, k := range keys {
		// Stored: the drop has done its job. Best effort: a leftover is offered again, and is the same value.
		_ = a.quietRun(ctx, "docker", "exec", "-u", "node", "claude-"+o, contract.SecretDrop, "--cancel", k)
		fmt.Fprintln(a.Stdout, "accepted: "+k)
	}
	return a.applyEnv(ctx, o, restart)
}

// readValue is env set's value: a hidden prompt on a terminal, else one line of stdin
// (`IFS= read -r val`, a last line without a newline included).
func (a *App) readValue(key string) (string, error) {
	if f, ok := a.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		sayf(a.Stderr, "Value for %s (hidden): ", key)
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
	script := ops.RemoteLogScript(n)
	return a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "exec", "claude-" + o, "sh", "-c", script}, Stdout: stdout, Stderr: a.Stderr})
}

// Remote is `ccenv remote <org> [status|logs|restart]`.
func (a *App) Remote(ctx context.Context, o, sub string) error {
	if sub == "" {
		sub = "status"
	}
	if sub == "status" {
		return a.remoteStatus(ctx, o)
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
	case "logs":
		return a.rcLog(ctx, o, 40, a.Stdout)
	case "restart":
		return a.remoteRestart(ctx, o)
	}
	return fmt.Errorf("usage: %s remote <org> [status|logs|restart]", Tool)
}

// remoteStatus is `ccenv remote <org> status`. When Remote Control is on and its log has no
// capacity line, ccenv's pipeline fails after the lines above it are printed: exit 1, no message.
func (a *App) remoteStatus(ctx context.Context, o string) error {
	st, err := ops.GetRemoteStatus(ctx, a, o)
	if st.State == "" {
		return err // not an org, or not running
	}
	if a.Output == OutputJSON {
		// The state is known even when the capacity line isn't: JSON reports it, with an empty one.
		return a.writeJSON(st)
	}
	switch st.State {
	case "login-needed":
		sayf(a.Stdout, "Remote Control: not logged in. Run: %s login %s\n", Tool, o)
	case "on":
		fmt.Fprintln(a.Stdout, "Remote Control: running")
		fmt.Fprintln(a.Stdout, "  Open:  "+st.URL)
		sayf(a.Stdout, "  Or:    claude.ai/code or the Claude app -> pick environment '%s' -> New session\n", o)
		if st.Capacity != "" {
			fmt.Fprintln(a.Stdout, "  "+st.Capacity)
		}
	case "blocked-by-org":
		sayf(a.Stdout, `Remote Control: BLOCKED by the Claude organization's policy for this account.
  An admin of that Claude organization must enable Remote Control. The service retries hourly and
  recovers on its own. Meanwhile use SSH, the browser terminal, VS Code or '%[1]s attach %[2]s'.
  To stop trying: set REMOTE_CONTROL=0 in orgs/%[2]s/org.env, then %[1]s restart %[2]s
`, Tool, o)
	default:
		sayf(a.Stdout, "Remote Control: restarting (see: %s remote %s logs)\n", Tool, o)
	}
	return err
}
