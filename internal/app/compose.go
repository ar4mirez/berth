package app

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/assets"
	"github.com/ar4mirez/berth/internal/host"
)

// composeAssets picks the compose.yml (and image) berth runs with:
//   - normally, its own embedded copy, written to <state>/berth/ first if needed (BERTH_IMAGE_DIR
//     overrides the image dir);
//   - under --read-only, where nothing may be written: that copy if it's already there, else the state
//     root's compose.yml (a ccenv checkout's own), else the copy's path anyway (compose will say it's
//     missing).
//
// berth reports whether the set is berth's own, which is when the CLAUDE_ENV_IMAGE* variables apply.
func (a *App) composeAssets() (assets.Set, bool, error) {
	root := a.State.Home.Path
	if a.State.ReadOnly {
		set, err := assets.Embedded(root)
		if err != nil || assets.Materialized(a.Host.FS, root) {
			return set, err == nil, err
		}
		if legacy := path.Join(root, "compose.yml"); a.isFile(legacy) {
			return assets.Set{Compose: legacy}, false, nil
		}
		return set, true, nil
	}
	set, err := assets.Materialize(a.Host.FS, root)
	if err != nil {
		return assets.Set{}, false, err
	}
	if dir := a.Getenv("BERTH_IMAGE_DIR"); dir != "" {
		if set, err = assets.Override(a.Host.FS, set, dir); err != nil {
			return assets.Set{}, false, err
		}
	}
	return set, true, nil
}

// compose is ccenv's compose(): create the bind-mount sources Docker would otherwise create
// root-owned (home-config, quarantine: 0700), then run `docker compose -f <compose.yml> --env-file
// <org.env> args...` with BIND_ADDR, ORG, ORG_DIR, HOST_UID and HOST_GID set, on berth's stdio.
//
// Under --read-only the directories are not created. Only `up` needs them to exist, and up doesn't
// run under --read-only.
func (a *App) compose(ctx context.Context, o string, args ...string) error {
	dir := path.Join(a.Orgs.Dir, o)
	if !a.State.ReadOnly {
		for _, sub := range []string{"home-config", "quarantine"} {
			p := path.Join(dir, sub)
			if a.isDir(p) {
				continue
			}
			if err := a.Host.FS.MkdirAll(p, 0o700); err != nil {
				return err
			}
			if err := a.Host.FS.Chmod(p, 0o700); err != nil {
				return err
			}
		}
	}
	set, own, err := a.composeAssets()
	if err != nil {
		return err
	}
	// Evaluated in ccenv's order: BIND_ADDR="$(resolve_bind)" first, then HOST_UID/HOST_GID.
	bind := a.bindOrWarn(ctx, o)
	uid, _ := a.capture(ctx, false, "id", "-u")
	gid, _ := a.capture(ctx, false, "id", "-g")
	env := []string{"BIND_ADDR=" + bind, "ORG=" + o, "ORG_DIR=" + dir, "HOST_UID=" + uid, "HOST_GID=" + gid}
	if own {
		repo, tag, _ := strings.Cut(set.Tag, ":")
		env = append(env, "CLAUDE_ENV_IMAGE="+repo, "IMAGE_TAG="+tag, "CLAUDE_ENV_IMAGE_DIR="+set.ImageDir)
	}
	argv := append([]string{"docker", "compose", "-f", set.Compose, "--env-file", a.Orgs.EnvPath(o)}, args...)
	return a.Host.Exec.Run(ctx, host.Cmd{
		Args:   argv,
		Env:    env,
		Stdin:  a.Stdin,
		Stdout: a.Stdout,
		Stderr: a.Stderr,
	})
}

// Logs is `ccenv logs <org>`: compose logs -f, ending with docker's exit code.
func (a *App) Logs(ctx context.Context, o string) error {
	if err := a.needOrg(o); err != nil {
		return err
	}
	return a.compose(ctx, o, "logs", "-f")
}

// umask is the host's umask (`sh -c umask`), for files ccenv creates with a shell redirection
// (mode 0666 &^ umask). Read once per invocation.
func (a *App) umask(ctx context.Context) fs.FileMode {
	if a.umaskVal != nil {
		return *a.umaskVal
	}
	m := fs.FileMode(0o022)
	if out, err := a.capture(ctx, true, "sh", "-c", "umask"); err == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(out), 8, 32); err == nil {
			m = fs.FileMode(n) & 0o777
		}
	}
	a.umaskVal = &m
	return m
}

// writeNew is `cmd > f` for a file that doesn't exist yet: created with 0666 &^ umask.
func (a *App) writeNew(ctx context.Context, p string, data []byte) error {
	if err := a.Host.FS.WriteFile(p, data, 0o666&^a.umask(ctx)); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}
