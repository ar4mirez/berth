package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// newPassword is ccenv's `head -c 64 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32`.
func newPassword() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var out []byte
	for _, c := range []byte(base64.StdEncoding.EncodeToString(b)) {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			out = append(out, c)
		}
	}
	if len(out) < 32 {
		return "", errors.New("could not generate a password")
	}
	return string(out[:32]), nil
}

// writeSecret is write_secret: `(umask 077; printf '%s' "$2" > "$1")`.
func (a *App) writeSecret(p, value string) error {
	return a.Host.FS.WriteFile(p, []byte(value), 0o600)
}

// Init is `ccenv init <org> [--name N] [--email E]`. The org it creates is berth's (MANAGER=berth,
// the first line of org.env); everything else is ccenv's scaffold, byte for byte.
func (a *App) Init(ctx context.Context, args []string) error {
	o := ""
	if len(args) > 0 {
		o, args = args[0], args[1:]
	}
	if !orgName.MatchString(o) {
		return errors.New("org name must be lowercase [a-z0-9-]")
	}
	d := path.Join(a.Orgs.Dir, o)
	if _, err := a.Host.FS.Stat(path.Join(d, "org.env")); err == nil {
		return fmt.Errorf("%s already exists", o)
	}
	var name, email string
	for len(args) > 0 {
		switch args[0] {
		case "--name", "--email":
			if len(args) < 2 {
				return errors.New("$2: unbound variable") // bash's set -u error for `name="$2"`
			}
			if args[0] == "--name" {
				name = args[1]
			} else {
				email = args[1]
			}
			args = args[2:]
		default:
			return fmt.Errorf("unknown flag %s", args[0])
		}
	}
	if name == "" {
		name, _ = a.capture(ctx, true, "git", "config", "--global", "user.name")
	}
	if email == "" {
		email, _ = a.capture(ctx, true, "git", "config", "--global", "user.email")
	}

	// mkdir -p "$d"/{workspace,claude,ssh,sshd,mise,home-config,config/secrets} (umask modes), then
	// chmod 700 on five of them.
	dirMode := 0o777 &^ a.umask(ctx)
	for _, sub := range []string{"workspace", "claude", "ssh", "sshd", "mise", "home-config", "config/secrets"} {
		// MkdirAll creates only what's missing, each with dirMode, like mkdir -p under the umask.
		if err := a.Host.FS.MkdirAll(path.Join(d, sub), dirMode); err != nil {
			return err
		}
	}
	for _, p := range []string{d, path.Join(d, "ssh"), path.Join(d, "claude"), path.Join(d, "home-config"), path.Join(d, "config/secrets")} {
		if err := a.Host.FS.Chmod(p, 0o700); err != nil {
			return err
		}
	}
	host, _ := a.capture(ctx, false, "hostname")
	if err := a.passthrough(ctx, false, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "claude-"+o+"@"+host,
		"-f", path.Join(d, "ssh", "id_ed25519")); err != nil {
		return err
	}
	if err := a.writeNew(ctx, path.Join(d, "config", "authorized_keys"), a.homePubKeys()); err != nil {
		return err
	}
	pw, err := newPassword()
	if err != nil {
		return err
	}
	if err := a.writeSecret(path.Join(d, "config", "secrets", "ttyd_credential"), "node:"+pw); err != nil {
		return err
	}
	if err := a.writeNew(ctx, path.Join(d, "config", "firewall.txt"), []byte(firewallTemplate(o))); err != nil {
		return err
	}

	bind := "127.0.0.1"
	if _, err := a.capture(ctx, true, "sh", "-c", "command -v tailscale"); err == nil {
		bind = "tailscale"
	}
	ssh, err := a.Orgs.NextPort("SSH_PORT", 2201)
	if err != nil {
		return err
	}
	ttyd, err := a.Orgs.NextPort("TTYD_PORT", 7701)
	if err != nil {
		return err
	}
	env := orgEnvTemplate(o, name, email, bind, ssh, ttyd)
	if err := a.writeNew(ctx, a.Orgs.EnvPath(o), []byte(env)); err != nil {
		return err
	}
	if err := a.Host.FS.Chmod(a.Orgs.EnvPath(o), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, `Created %[1]s

Next:
  1. Start:   %[2]s up %[3]s
  2. Git key: add to %[3]s's GitHub:  %[4]s
  3. Sign in: %[2]s auth %[3]s   (private browser window, %[3]s's Claude account)
  4. Repos:   %[2]s repo add %[3]s <owner/repo>   (only registered repos are allowed)
`, d, Tool, o, a.catFile(path.Join(d, "ssh", "id_ed25519.pub")))
	return nil
}

// homePubKeys is `cat "$HOME"/.ssh/*.pub > f 2>/dev/null || : > f`: every key, in glob order, or
// nothing at all if there are none or any of them can't be read.
func (a *App) homePubKeys() []byte {
	dir := path.Join(a.Getenv("HOME"), ".ssh")
	entries, err := a.Host.FS.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".pub") && !strings.HasPrefix(n, ".") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []byte
	for _, n := range names {
		b, err := a.Host.FS.ReadFile(path.Join(dir, n))
		if err != nil {
			return nil
		}
		out = append(out, b...)
	}
	return out
}

// orgEnvTemplate is init's org.env, with MANAGER=berth first.
func orgEnvTemplate(o, name, email, bind string, ssh, ttyd int) string {
	return "MANAGER=berth\n" + `# ---- Claude account (use: ` + Tool + ` token ` + o + `) ---------------------------------
CLAUDE_CODE_OAUTH_TOKEN=
# Alternative, API/Console billing:
# ANTHROPIC_API_KEY=

# ---- Git identity for this org ------------------------------------------------
GIT_USER_NAME=` + name + `
GIT_USER_EMAIL=` + email + `
# Optional: gh CLI + HTTPS git for this org
GH_TOKEN=

# ---- Access ---------------------------------------------------------------------
# tailscale = this host's Tailscale IP (reachable from your tailnet anywhere)
# 127.0.0.1 = this host only; 0.0.0.0 = all interfaces (not recommended)
BIND_ADDR=` + bind + `
SSH_PORT=` + strconv.Itoa(ssh) + `
TTYD_PORT=` + strconv.Itoa(ttyd) + `
# Remote Control service: start sessions from claude.ai/code (needs: ` + Tool + ` login ` + o + `)
REMOTE_CONTROL=1
REMOTE_CAPACITY=8

# ---- Repos ----------------------------------------------------------------------------
# Only repos registered with '` + Tool + ` repo add' may be used (config/repos.txt).
# enforce = block Claude + git, and quarantine anything else in /workspace; warn = log only; off
REPO_POLICY=enforce

# ---- Resources --------------------------------------------------------------------
MEM_LIMIT=8g
CPUS=4
`
}
