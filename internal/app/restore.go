package app

import (
	"bytes"
	"context"
	_ "embed" // rehydrate.sh
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/org"
)

// rehydrateScript is the script cmd_rehydrate feeds to bash in the container, byte for byte.
//
//go:embed rehydrate.sh
var rehydrateScript string

// Restore is `ccenv restore <file|-> [--as name] [--identity|-i key] [--force] [--no-start]
// [--no-rehydrate]`. The backup is decrypted and extracted in one stream into a staging dir, so a
// wrong key or a tampered file leaves nothing behind. The restored org is berth's (MANAGER=berth),
// and --force only replaces an org berth owns (PARITY.md).
func (a *App) Restore(ctx context.Context, args []string) error {
	src, as, ident, force, start, rehydrate := "", "", "", false, true, true
	for i := 0; i < len(args); i++ {
		switch x := args[i]; {
		case x == "--as" || x == "--identity" || x == "-i":
			// as="$2"; shift 2: under set -u a missing value ends the command (PARITY.md).
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", x)
			}
			i++
			if x == "--as" {
				as = args[i]
			} else {
				ident = args[i]
			}
		case x == "--force":
			force = true
		case x == "--no-start":
			start = false
		case x == "--no-rehydrate":
			rehydrate = false
		case x == "-":
			src = "-"
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		default:
			src = x
		}
	}
	if src == "" {
		return fmt.Errorf("usage: %s restore <file|-> [--as name] [--identity key] [--force] [--no-start] [--no-rehydrate]", Tool)
	}
	if src != "-" && !a.isFile(src) {
		return fmt.Errorf("no such file: %s", src)
	}
	if err := a.ensureImage(ctx); err != nil {
		return err
	}

	// Supply whatever this backup needs: for a file, from its first byte; for stdin, what's configured.
	kind := "unknown"
	if src != "-" {
		kind = a.backupKind(src)
	}
	sec := &secrets{a: a}
	defer sec.cleanup()
	if kind == "gpg" || (kind == "unknown" && a.backupEnv("BACKUP_PASSPHRASE") != "") {
		if err := a.setPassphrase(sec, false); err != nil {
			return err
		}
	}
	kf := a.keyFile()
	switch {
	case ident != "":
		if err := a.setIdentity(sec, ident); err != nil {
			return err
		}
	case a.isFile(kf) && kind != "zstd" && kind != "gpg":
		if err := a.setIdentity(sec, kf); err != nil {
			return err
		}
	case kind == "age":
		return fmt.Errorf("key-encrypted backup: pass --identity <key> (default %s not found)", kf)
	}

	stage, err := a.Host.FS.MkdirTemp(a.Orgs.Dir, ".restore-XXXXXX")
	if err != nil {
		return err
	}
	defer func() {
		// The EXIT trap: extracted files can be root's (sshd/), so the engine removes them.
		if stage != "" && a.isDir(stage) {
			set, _, _ := a.composeAssets()
			_ = a.quietRun(ctx, "docker", "run", "--rm", "-v", a.Orgs.Dir+":/orgs", "--entrypoint", "rm", set.Tag, "-rf", "/orgs/"+path.Base(stage))
		}
	}()
	uid, _ := a.capture(ctx, false, "id", "-u")
	gid, _ := a.capture(ctx, false, "id", "-g")
	in := a.Stdin
	if src != "-" {
		f, err := a.Host.FS.Open(src)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		in = f
	}
	if a.archive(ctx, sec, "", "", stage+":/dst", in, a.Stdout, "extract", uid, gid) != nil {
		return errors.New("restore failed (wrong passphrase/key, or the backup is corrupted/tampered). Nothing was changed.") //nolint:staticcheck // ccenv's exact text
	}
	raw, err := a.Host.FS.ReadFile(stage + "/.ccenv-manifest.json")
	if err != nil {
		return errors.New("not a ccenv backup (no manifest)")
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("not a ccenv backup (bad manifest: %w)", err)
	}
	field := func(k string) string { return jqString(manifest[k]) } // jq -r .k: null when missing
	name := as
	if name == "" {
		name = field("org")
	}
	if !orgName.MatchString(name) {
		return fmt.Errorf("invalid org name '%s'", name)
	}
	format := a.catFile(stage + "/.ccenv-format")
	fmt.Fprintf(a.Stdout, "Restoring '%s' from %s (%s, %s) as '%s'\n", field("org"), field("source_host"), field("created"), format, name)

	d := path.Join(a.Orgs.Dir, name)
	if a.exists(d) {
		if !force {
			return fmt.Errorf("%s already exists (use --force to replace it, or --as <other-name>)", name)
		}
		if a.isFile(a.Orgs.EnvPath(name)) {
			if err := a.needOwnedOrg(name); err != nil {
				return err
			}
		}
		if a.running(ctx, name) {
			q := *a
			q.Stdout, q.Stderr = io.Discard, io.Discard
			_ = q.compose(ctx, name, "down")
		}
		replaced := a.backupsDir() + "/.replaced"
		if err := a.Host.FS.MkdirAll(replaced, 0o777&^a.umask(ctx)); err != nil {
			return err
		}
		stamp, err := a.capture(ctx, false, "date", "+%Y%m%d-%H%M%S")
		if err != nil {
			return err
		}
		if err := a.Host.FS.Rename(d, replaced+"/"+name+"-"+stamp); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Existing %s moved to %s/\n", name, replaced)
	}
	_ = a.Host.FS.Remove(stage + "/.ccenv-format")
	if err := a.Host.FS.Rename(stage, d); err != nil {
		return err
	}
	stage = ""
	if err := a.Host.FS.Chmod(d, 0o700); err != nil {
		return err
	}
	if err := a.markOwned(name); err != nil {
		return err
	}

	// Make it fit this host: free ports, and Tailscale only if this host has it.
	for _, k := range []struct {
		key  string
		base int
	}{{"SSH_PORT", 2201}, {"TTYD_PORT", 7701}} {
		p := a.env(name, k.key)
		if !a.portUsedElsewhere(name, k.key+"="+p) {
			continue
		}
		if err := a.Orgs.Set(name, k.key, "0"); err != nil {
			return err
		}
		n, err := a.Orgs.NextPort(k.key, k.base)
		if err != nil {
			return err
		}
		if err := a.Orgs.Set(name, k.key, fmt.Sprint(n)); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "%s was taken here; now %s\n", k.key, a.env(name, k.key))
	}
	if a.env(name, "BIND_ADDR") == "tailscale" && a.quietRun(ctx, "tailscale", "ip", "-4") != nil {
		if err := a.Orgs.Set(name, "BIND_ADDR", "127.0.0.1"); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "Tailscale not up on this host: bound to 127.0.0.1 (set BIND_ADDR=tailscale later)")
	}

	fmt.Fprintf(a.Stdout, "Restored to %s\n", d)
	if !start {
		fmt.Fprintf(a.Stdout, "Start with: %s up %s\n", Tool, name)
		return nil
	}
	q := *a
	q.Stdout = io.Discard
	if err := q.compose(ctx, name, "up", "-d", "--force-recreate"); err != nil {
		return err
	}
	if err := a.passthrough(ctx, false, "sleep", "5"); err != nil {
		return err
	}
	if rehydrate {
		if err := a.Rehydrate(ctx, name); err != nil {
			return err
		}
	}
	return a.Ls(ctx)
}

// backupKind is `od -An -tx1 -N1 "$src"`: the format, from the first byte.
func (a *App) backupKind(src string) string {
	f, err := a.Host.FS.Open(src)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = f.Close() }()
	var b [1]byte
	if n, _ := f.Read(b[:]); n == 0 {
		return "unknown"
	}
	switch b[0] {
	case 0x28:
		return "zstd"
	case 0x8c, 0xc3:
		return "gpg"
	case 0x61:
		return "age"
	}
	return "unknown"
}

// setIdentity is set_identity: the age identity (key file) copied into the secrets dir, 0600.
func (a *App) setIdentity(sec *secrets, f string) error {
	if !a.isFile(f) {
		return fmt.Errorf("identity not found: %s", f)
	}
	b, err := a.Host.FS.ReadFile(f)
	if err != nil {
		return err
	}
	p, err := sec.path("identity")
	if err != nil {
		return err
	}
	return a.Host.FS.WriteFile(p, b, 0o600)
}

// markOwned makes a restored org berth's: MANAGER=berth, as the first line when the backup has no
// MANAGER (a ccenv org), or in place of another value.
func (a *App) markOwned(name string) error {
	p := a.Orgs.EnvPath(name)
	b, err := a.Host.FS.ReadFile(p)
	if err != nil {
		return nil // no org.env: the port and bind fixes below report it, as in ccenv
	}
	if v, ok := org.Lookup(b, "MANAGER"); ok {
		if v == "berth" {
			return nil
		}
		return a.Orgs.Set(name, "MANAGER", "berth")
	}
	return a.Host.FS.WriteFile(p, append([]byte("MANAGER=berth\n"), b...), 0o600)
}

// portUsedElsewhere is `grep -qx "$k=$p"` over every other org's org.env.
func (a *App) portUsedElsewhere(name, line string) bool {
	for _, o := range a.orgDirs() {
		if o == name {
			continue
		}
		b, err := a.Host.FS.ReadFile(a.Orgs.EnvPath(o))
		if err != nil {
			continue
		}
		for _, l := range fileLines(b) {
			if l == line {
				return true
			}
		}
	}
	return false
}

// Rehydrate is `ccenv rehydrate <org>`: reinstall what backups skip (repos, mise toolchains,
// project dependencies) inside the running container.
func (a *App) Rehydrate(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "== Rehydrating %s: repos, toolchains and dependencies (this can take a while)\n", o)
	if err := a.Repo(ctx, "sync", o, nil); err != nil {
		return err
	}
	return a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "exec", "-i", "-u", "node", "-w", "/workspace", "claude-" + o, "bash", "-s"},
		Stdin: strings.NewReader(rehydrateScript), Stdout: a.Stdout, Stderr: a.Stderr})
}

// remoteBerth finds berth on the target host, as ccenv looks for ccenv there.
const remoteBerth = `command -v berth || { [ -x ~/.local/bin/berth ] && echo ~/.local/bin/berth; }`

// Migrate is `ccenv migrate <org> <[user@]host> [--as name] [--remote-dir dir]`: stream the org, as
// a plain zstd tar (the frozen contract), into `berth restore -` on another host over ssh. berth
// must already be installed there; --remote-dir is the state root it restores into (PARITY.md).
func (a *App) Migrate(ctx context.Context, args []string) error {
	o, target, as, rdir := nth(args, 0), nth(args, 1), "", ""
	// shift 2 || true: with fewer than two arguments nothing is shifted, so the flag loop sees them.
	flags := args
	if len(args) >= 2 {
		flags = args[2:]
	}
	for i := 0; i < len(flags); i++ {
		switch x := flags[i]; x {
		case "--as", "--remote-dir":
			if i+1 >= len(flags) {
				return fmt.Errorf("%s needs a value", x)
			}
			i++
			if x == "--as" {
				as = flags[i]
			} else {
				rdir = flags[i]
			}
		default:
			return fmt.Errorf("unknown flag %s", x)
		}
	}
	if err := a.needOrg(o); err != nil {
		return err
	}
	if target == "" {
		return fmt.Errorf("usage: %s migrate <org> <[user@]host> [--as name] [--remote-dir dir]", Tool)
	}
	if a.passthrough(ctx, false, "ssh", target, "docker info >/dev/null 2>&1") != nil {
		return fmt.Errorf("%s: docker not reachable over ssh (is it installed, and is the user in the docker group?)", target)
	}
	rc, _ := a.capture(ctx, false, "ssh", target, remoteBerth)
	if rc == "" {
		return fmt.Errorf("berth isn't installed on %s: install it there first (a release binary on its PATH, or ~/.local/bin/berth)", target)
	}
	if rdir != "" {
		rc += " --home " + rdir
	}
	fmt.Fprintf(a.Stdout, "== Streaming %s to %s (skipping regenerable data)\n", o, target)
	sec := &secrets{a: a}
	mount := path.Join(a.Orgs.Dir, o) + ":/src:ro"
	// archive plan | sed -n '2,3p'
	var planOut bytes.Buffer
	perr := a.archive(ctx, sec, o, "", mount, a.Stdin, &planOut, "plan")
	_, _ = a.Stdout.Write(linesRange(planOut.Bytes(), 2, 3))
	if perr != nil {
		return perr
	}
	remote := rc + " restore - "
	if as != "" {
		remote += "--as " + as
	}
	if err := a.pipe(ctx,
		func(w io.Writer) error { return a.archive(ctx, sec, o, "", mount, a.Stdin, w, "create") },
		host.Cmd{Args: []string{"ssh", target, remote}, Stdout: a.Stdout, Stderr: a.Stderr}); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout)
	fmt.Fprintf(a.Stdout, "Migrated. '%s' is still running here too. Once the copy on %s looks right, stop this one:\n", o, target)
	fmt.Fprintf(a.Stdout, "  %s down %s     (both would share the same Remote Control login and git identity)\n", Tool, o)
	return nil
}

// pipe is `producer | cmd` under pipefail: both run at once, and the result is the last failure
// (cmd's if both fail).
func (a *App) pipe(ctx context.Context, producer func(io.Writer) error, cmd host.Cmd) error {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := producer(pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	cmd.Stdin = pr
	cerr := a.Host.Exec.Run(ctx, cmd)
	_ = pr.Close() // unblock the producer if cmd stopped reading
	perr := <-done
	if cerr != nil {
		return cerr
	}
	return perr
}

// linesRange is `sed -n 'FROM,TO p'` (1-based, inclusive).
func linesRange(b []byte, from, to int) []byte {
	var out []byte
	for i, rest := 1, b; len(rest) > 0 && i <= to; i++ {
		j := bytes.IndexByte(rest, '\n')
		line := rest
		if j >= 0 {
			line, rest = rest[:j+1], rest[j+1:]
		} else {
			rest = nil
		}
		if i >= from {
			out = append(out, line...)
		}
	}
	return out
}
