package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/org"
)

// Secrets as files (#37). An org keeps its tokens and custom variables either in org.env (as ccenv
// did; compose's env_file puts them in the container's config, where `docker inspect` shows them)
// or, once migrated, one file per variable in <org>/config/secrets/env (0600, dir 0700), which the
// container reads at start (image/secrets-env.sh) and berth's exec loader reads for `claude`/`run`.
// An org is migrated when that directory exists. Orgs that aren't behave exactly as before.

// secretKeys are the variables that are secrets whatever CCENV_ENV_KEYS says.
var secretKeys = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "GH_TOKEN"}

func (a *App) secretsDir(o string) string {
	return path.Join(a.Orgs.Dir, o, "config", "secrets", "env")
}

// migrated reports whether o keeps its secrets as files.
func (a *App) migrated(o string) bool { return a.isDir(a.secretsDir(o)) }

// secret is a variable's value: its file for a migrated org (if there is one), else org.env's.
func (a *App) secret(o, key string) string {
	if a.migrated(o) {
		if b, err := a.Host.FS.ReadFile(path.Join(a.secretsDir(o), key)); err == nil {
			return string(b)
		}
	}
	return a.env(o, key)
}

// setSecret writes a variable where the org keeps it: a file (0600; "" removes it) for a migrated
// org, else org.env (setval, the value as given).
func (a *App) setSecret(o, key, fileValue, envValue string) error {
	if !a.migrated(o) {
		return a.Orgs.Set(o, key, envValue)
	}
	unlock, err := a.lock(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	p := path.Join(a.secretsDir(o), key)
	if fileValue == "" {
		if err := a.Host.FS.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return a.Host.FS.WriteFileAtomic(p, []byte(fileValue), 0o600)
}

// execLoader wraps a command run with `docker exec` for a migrated org, so it gets the org's secrets
// and custom variables (exec'd processes don't inherit the entrypoint's environment). It's inline,
// so it also works in containers still running an older image. For an org that isn't migrated the
// command is unchanged (its values come from the container's environment, as before).
func (a *App) execLoader(o string, cmd ...string) []string {
	if !a.migrated(o) {
		return cmd
	}
	script := `for f in ` + contract.SecretsEnv + `/*; do [ -f "$f" ] || continue; k=${f##*/}; ` +
		`case "$k" in ""|[0-9]*|*[!A-Z0-9_]*) continue ;; esac; export "$k=$(cat "$f")"; done; exec "$@"`
	return append([]string{"sh", "-c", script, "berth-secrets"}, cmd...)
}

// SecretsMigrate is `berth secrets migrate <org> [--no-backup]`: move the org's secrets and custom
// variables out of org.env into files. A backup comes first. Nothing restarts: the running
// container keeps its environment until its next restart, which takes the values from the files.
func (a *App) SecretsMigrate(ctx context.Context, args []string) error {
	o, backup := "", true
	for _, x := range args {
		switch {
		case x == "--no-backup":
			backup = false
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		case o == "":
			o = x
		default:
			return fmt.Errorf("usage: %s secrets migrate <org> [--no-backup]", Tool)
		}
	}
	if err := a.writable(o, "move "+o+"'s secrets"); err != nil {
		return err
	}
	keys := append(append([]string{}, secretKeys...), strings.Fields(a.env(o, contract.EnvKeys))...)
	data, err := a.Host.FS.ReadFile(a.Orgs.EnvPath(o))
	if err != nil {
		return err
	}
	var moving []string
	for _, k := range keys {
		if _, ok := org.Lookup(data, k); ok {
			moving = append(moving, k)
		}
	}
	if len(moving) == 0 && a.migrated(o) {
		fmt.Fprintf(a.Stdout, "%s keeps its secrets as files already; nothing to move.\n", o)
		return nil
	}
	if backup {
		fmt.Fprintf(a.Stdout, "Backing up %s first…\n", o)
		if err := a.Backup(ctx, []string{o}); err != nil {
			return fmt.Errorf("the backup failed, so nothing was moved: %w", err)
		}
	}

	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	data, err = a.Host.FS.ReadFile(a.Orgs.EnvPath(o)) // again, under the lock
	if err != nil {
		return err
	}
	dir := a.secretsDir(o)
	if err := a.Host.FS.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := a.Host.FS.Chmod(dir, 0o700); err != nil {
		return err
	}
	var moved []string
	drop := map[string]bool{}
	for _, k := range keys {
		v, ok := org.Lookup(data, k)
		if !ok {
			continue
		}
		drop[k] = true
		if v = unquote(v); v == "" {
			continue // an empty line (GH_TOKEN=) is dropped, with no file
		}
		if err := a.Host.FS.WriteFileAtomic(path.Join(dir, k), []byte(v), 0o600); err != nil {
			return err
		}
		moved = append(moved, k)
	}
	// The files are written; now take the lines out of org.env, in place (same inode and mode).
	var kept strings.Builder
	for _, l := range fileLines(data) {
		if k, _, ok := strings.Cut(l, "="); ok && drop[k] {
			continue
		}
		kept.WriteString(l + "\n")
	}
	if err := a.Host.FS.WriteFile(a.Orgs.EnvPath(o), []byte(kept.String()), 0o600); err != nil {
		return err
	}
	if len(moved) == 0 {
		fmt.Fprintf(a.Stdout, "%s now keeps its secrets as files (%s); there were none to move.\n", o, dir)
	} else {
		fmt.Fprintf(a.Stdout, "Moved %s to %s (one file each, mode 0600).\n", strings.Join(moved, ", "), dir)
	}
	fmt.Fprintf(a.Stdout, "Nothing was restarted: the running container keeps its current environment.\n")
	fmt.Fprintf(a.Stdout, "Its next restart (%s restart %s, a short restart: plan it) takes the values from the files, and docker inspect shows none of them.\n", Tool, o)
	return nil
}

// unquote is what compose's env_file makes of a value berth or ccenv wrote: env set wraps values
// in single quotes.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}
