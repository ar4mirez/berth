package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/ar4mirez/berth/internal/assets"
	"github.com/ar4mirez/berth/internal/hosts"
	"github.com/ar4mirez/berth/internal/ops"
)

// destroyedFile lists the orgs destroy removed from this state root, 1 per line: "<org> <RFC 3339
// time>". It's in berth's own directory, and `ls --output json` reports it as `destroyed`, so a
// tool that expected the org can tell it was offboarded rather than never created.
func (a *App) destroyedFile() string { return path.Join(a.State.Home.Path, "berth", "destroyed") }

// Destroy is `berth destroy <org> [--yes] [--keep-backups]`: offboarding. It removes the org's
// container, its directory (workspace, Claude config and history, keys, secrets, toolchains),
// its backups in the backups dir unless --keep-backups, its lease and the default org if it
// names it, and records the org as destroyed. A person confirms by typing the org's name, or
// passes --yes.
func (a *App) Destroy(ctx context.Context, args []string) error {
	o, yes, keepBackups := "", false, false
	for _, x := range args {
		switch x {
		case "--yes", "-y":
			yes = true
		case "--keep-backups":
			keepBackups = true
		default:
			if strings.HasPrefix(x, "-") || o != "" {
				return fmt.Errorf("usage: %s destroy <org> [--yes] [--keep-backups]", Tool)
			}
			o = x
		}
	}
	if err := a.State.Writable("destroy " + o); err != nil {
		return err
	}
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	d := path.Join(a.Orgs.Dir, o)
	var backups []string
	if !keepBackups {
		backups = a.orgBackups(o)
	}

	fmt.Fprintf(a.Stdout, "This permanently removes %s:\n", o)
	state := "stopped"
	if a.running(ctx, o) {
		state = "running: its sessions stop now"
	}
	fmt.Fprintf(a.Stdout, "  - the container claude-%s (%s)\n", o, state)
	fmt.Fprintf(a.Stdout, "  - %s: the workspace, Claude's config and history, keys, secrets, toolchains\n", d)
	for _, b := range backups {
		fmt.Fprintf(a.Stdout, "  - backup %s\n", b)
	}
	if keepBackups {
		fmt.Fprintf(a.Stdout, "Its backups in %s stay (--keep-backups).\n", a.backupsDir())
	} else {
		fmt.Fprintf(a.Stdout, "Backups written elsewhere (backup -o) aren't known to %s: delete them yourself.\n", Tool)
	}
	if !yes {
		f, ok := a.Stdin.(*os.File)
		if !ok || !term.IsTerminal(int(f.Fd())) {
			return errors.New("nothing was removed: confirm in a terminal, or pass --yes")
		}
		fmt.Fprintf(a.Stderr, "Type %s to destroy it: ", o)
		line, err := bufio.NewReader(a.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if strings.TrimSpace(line) != o {
			return errors.New("not confirmed; nothing was removed")
		}
	}

	// The container first: compose down, with its volumes and any stray containers.
	q := *a
	q.Stdout = io.Discard
	if err := q.compose(ctx, o, "down", "--volumes", "--remove-orphans"); err != nil {
		return fmt.Errorf("stopping claude-%s: %w; nothing else was removed", o, err)
	}
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := a.removeOrgDir(ctx, d); err != nil {
		return fmt.Errorf("removing %s: %w", d, err)
	}
	for _, b := range backups {
		if err := a.Host.FS.RemoveAll(b); err != nil {
			return fmt.Errorf("removing %s: %w", b, err)
		}
	}
	a.forgetOrg(o)
	if err := a.recordDestroyed(o); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Destroyed %s", o)
	if len(backups) > 0 {
		fmt.Fprintf(a.Stdout, ", and %d backup(s)", len(backups))
	}
	fmt.Fprintln(a.Stdout, ".")
	return nil
}

// orgBackups are o's backups in the backups dir (<org>-YYYYmmdd-HHMMSS.tar.zst[.gpg|.age]) and the
// copies restore --force set aside (.replaced/<org>-YYYYmmdd-HHMMSS), sorted.
func (a *App) orgBackups(o string) []string {
	var out []string
	for _, c := range []struct {
		dir string
		re  *regexp.Regexp
	}{
		{a.backupsDir(), regexp.MustCompile(`^` + regexp.QuoteMeta(o) + `-[0-9]{8}-[0-9]{6}\.tar\.zst(\.gpg|\.age)?$`)},
		{path.Join(a.backupsDir(), ".replaced"), regexp.MustCompile(`^` + regexp.QuoteMeta(o) + `-[0-9]{8}-[0-9]{6}$`)},
	} {
		entries, err := a.Host.FS.ReadDir(c.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if c.re.MatchString(e.Name()) {
				out = append(out, path.Join(c.dir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// removeOrgDir removes the org's directory. Files the container made as root (sshd's keys, say)
// may not be removable by this user; then the engine removes what's in it, as root, and this
// user removes the rest.
func (a *App) removeOrgDir(ctx context.Context, d string) error {
	if err := a.Host.FS.RemoveAll(d); err == nil {
		return nil
	}
	if err := a.ensureImage(ctx); err != nil {
		return err
	}
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	if err := a.quietRun(ctx, "docker", "run", "--rm", "--network", "none", "-v", d+":/o", "--entrypoint", "find", set.Tag,
		"/o", "-mindepth", "1", "-delete"); err != nil {
		return err
	}
	return a.Host.FS.RemoveAll(d)
}

// forgetOrg drops what berth keeps about o outside its directory: the lease (when this host holds
// it) and its marker, and the default org if it's o. Best effort: the org is already gone.
func (a *App) forgetOrg(o string) {
	_ = a.Host.FS.RemoveAll(a.orgContextDir(o)) // its generated Dockerfile (#106)
	_ = a.Host.FS.Remove(hosts.LeaseMarker(a.State.Home.Path, o))
	if leases, err := hosts.LoadLeases(a.Operator.FS, a.hostPaths()); err == nil {
		if h, ok := leases.Orgs[o]; ok && h == a.hostLabel() {
			delete(leases.Orgs, o)
			_ = hosts.SaveLeases(a.Operator.FS, a.hostPaths(), leases)
		}
	}
	if b, err := a.Operator.FS.ReadFile(a.contextFile()); err == nil {
		def := strings.TrimSpace(string(b))
		if def == o || def == o+"@"+a.hostLabel() {
			_ = a.Operator.FS.Remove(a.contextFile())
		}
	}
}

// recordDestroyed adds o to destroyedFile, replacing an earlier line for it.
func (a *App) recordDestroyed(o string) error {
	if _, err := assets.LockPath(a.Host.FS, a.State.Home.Path); err != nil { // berth's dir, with its .gitignore
		return err
	}
	f := a.destroyedFile()
	b, err := a.Host.FS.ReadFile(f)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var out strings.Builder
	for _, l := range fileLines(b) {
		if name, _, _ := strings.Cut(l, " "); name != o && l != "" {
			out.WriteString(l + "\n")
		}
	}
	out.WriteString(o + " " + time.Now().UTC().Format(time.RFC3339) + "\n")
	return a.Host.FS.WriteFileAtomic(f, []byte(out.String()), 0o600)
}

// destroyedOrgs are the orgs destroy removed from this state root that don't exist again since.
func (a *App) destroyedOrgs() []ops.DestroyedOrg {
	b, err := a.Host.FS.ReadFile(a.destroyedFile())
	if err != nil {
		return nil
	}
	var out []ops.DestroyedOrg
	for _, l := range fileLines(b) {
		name, at, ok := strings.Cut(l, " ")
		if !ok || a.isFile(a.Orgs.EnvPath(name)) {
			continue
		}
		out = append(out, ops.DestroyedOrg{Name: name, Host: a.hostLabel(), At: at})
	}
	return out
}
