package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/hosts"
)

// Moving an org between hosts over the registry (#50): `berth migrate <org>[@host] <host>`.
//
//  1. Rehearsal: the org is streamed (the plain zstd tar of the backup contract, over berth's SSH
//     connections) into a stopped copy on the target, and checked, while it keeps running.
//  2. With the operator's go-ahead (a restart, announced): the org is stopped, streamed again (so
//     nothing written since the rehearsal is lost), checked file by file, its lease moves, and it
//     starts on the target, then rehydrates.
//  3. The old copy stays, stopped, until the operator removes it.
//
// A failure after the stop starts the org where it was again, with its lease. Before the stop,
// nothing on the source changes.

// migrateFailAt is a test hook: the integration test sets BERTH_TEST_MIGRATE_FAIL=<step> to check
// that a failure at that step leaves the org running where it was. Nothing else sets it.
const migrateFailEnv = "BERTH_TEST_MIGRATE_FAIL"

func (a *App) migrateFail(step string) error {
	if a.Getenv(migrateFailEnv) == step {
		return fmt.Errorf("%s=%s: failing on purpose", migrateFailEnv, step)
	}
	return nil
}

// migrateToHost is Migrate when the target is a registered host (or local).
func (a *App) migrateToHost(ctx context.Context, o, target, as string, yes, noSwitch bool) (err error) {
	if err := a.State.Writable("migrate an org"); err != nil {
		return err
	}
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	src, from := a, a.hostLabel()
	if target == from {
		return fmt.Errorf("%s is already on %s", o, target)
	}
	dst, done, err := a.appOn(ctx, target)
	if err != nil {
		return err
	}
	defer done()
	dst.Stdin, dst.Stdout, dst.Stderr = a.Stdin, a.Stdout, a.Stderr
	name := as
	if name == "" {
		name = o
	}
	if !orgName.MatchString(name) {
		return fmt.Errorf("invalid org name '%s'", name)
	}
	// The target may already have a stopped copy from an earlier rehearsal: it is replaced. Any other
	// org by that name is refused.
	replace := false
	if dst.exists(path.Join(dst.Orgs.Dir, name)) {
		if m := dst.manifestOrg(name); m != o || dst.running(ctx, name) {
			return fmt.Errorf("%s already has an org named %s; pick another name with --as", target, name)
		}
		replace = true
	}
	if err := dst.ensureImage(ctx); err != nil {
		return fmt.Errorf("berth's image on %s: %w", target, err)
	}

	// 1. Rehearsal, while the org keeps running.
	fmt.Fprintf(a.Stdout, "== Rehearsal: copying %s from %s to %s as %s (it keeps running here)\n", o, from, target, name)
	began := time.Now()
	if err := a.copyOrg(ctx, src, o, dst, name, replace); err != nil {
		return fmt.Errorf("the rehearsal copy failed: %w. %s still runs on %s; nothing changed there", err, o, from)
	}
	took := time.Since(began).Round(time.Second)
	if err := a.verifyCopy(ctx, src, o, dst, name, false); err != nil {
		return fmt.Errorf("the rehearsal copy doesn't match: %w. %s still runs on %s; the stopped copy on %s is left for a look", err, o, from, target)
	}
	fmt.Fprintf(a.Stdout, "Rehearsal done in %s: the copy on %s matches.\n", took, target)

	// 2. The switch: the operator's go-ahead.
	running := src.running(ctx, o)
	switch {
	case noSwitch:
		fmt.Fprintf(a.Stdout, "Stopped here (--no-switch). To switch: %s migrate %s %s --yes\n", Tool, o+"@"+from, target)
		return nil
	case !yes:
		ok, err := a.confirmSwitch(o, from, target, took, running)
		if err != nil || !ok {
			fmt.Fprintf(a.Stdout, "Not switched: %s still runs on %s. To switch later: %s migrate %s %s --yes\n", o, from, Tool, o+"@"+from, target)
			return err
		}
	}

	fmt.Fprintf(a.Stdout, "== Switching %s to %s: stopping it on %s (its work there stops)\n", o, target, from)
	if running {
		if err := src.Down(ctx, o); err != nil {
			return fmt.Errorf("stopping %s on %s: %w; nothing was switched", o, from, err)
		}
	}
	// From here on, a failure starts the org where it was again.
	defer func() {
		if err == nil || !running {
			return
		}
		fmt.Fprintf(a.Stdout, "== Something failed: starting %s on %s again\n", o, from)
		src.TakeLease = true
		if rerr := src.Up(context.WithoutCancel(ctx), o); rerr != nil {
			err = fmt.Errorf("%w; AND restarting it on %s failed: %w. Start it by hand: %s up %s@%s --take-lease", err, from, rerr, Tool, o, from)
			return
		}
		err = fmt.Errorf("%w. %s runs on %s again", err, o, from)
	}()
	fmt.Fprintf(a.Stdout, "== Final copy (%s is stopped, so nothing written since the rehearsal is lost)\n", o)
	if err := a.migrateFail("final"); err != nil {
		return err
	}
	if err := a.copyOrg(ctx, src, o, dst, name, true); err != nil {
		return fmt.Errorf("the final copy failed: %w", err)
	}
	if err := a.verifyCopy(ctx, src, o, dst, name, true); err != nil {
		return fmt.Errorf("the final copy doesn't match: %w", err)
	}
	if err := a.migrateFail("start"); err != nil {
		return err
	}
	dst.TakeLease = true
	if err := dst.Up(ctx, name); err != nil {
		return fmt.Errorf("starting %s on %s: %w", name, target, err)
	}
	if err := dst.Rehydrate(ctx, name); err != nil {
		fmt.Fprintf(a.Stderr, "warning: rehydrating %s on %s: %v (run it again: %s rehydrate %s@%s)\n", name, target, err, Tool, name, target)
	}
	fmt.Fprintf(a.Stdout, "Migrated: %s runs on %s as %s, and holds the lease there.\n", o, target, name)
	fmt.Fprintf(a.Stdout, "The old copy stays, stopped, on %s (%s). Remove it once you're sure.\n", from, path.Join(src.Orgs.Dir, o))
	return nil
}

// confirmSwitch asks for the go-ahead on the operator's terminal.
func (a *App) confirmSwitch(o, from, target string, took time.Duration, running bool) (bool, error) {
	tty, err := a.openTTY()
	if err != nil {
		return false, nil // no terminal: never switch without --yes
	}
	defer func() { _ = tty.Close() }()
	stop := fmt.Sprintf("stops %s on %s (its work there stops), ", o, from)
	if !running {
		stop = ""
	}
	fmt.Fprintf(a.Stderr, "Switch now? This %scopies it again (the rehearsal took %s) and starts it on %s. [y/N] ", stop, took, target)
	ans, _ := bufio.NewReader(tty).ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))
	return ans == "y" || ans == "yes", nil
}

// copyOrg streams o from src into a stopped copy named name on dst (restore - --no-start).
func (a *App) copyOrg(ctx context.Context, src *App, o string, dst *App, name string, replace bool) error {
	if replace {
		if err := dst.removeCopy(ctx, name); err != nil {
			return fmt.Errorf("removing the earlier copy: %w", err)
		}
	}
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		sec := &secrets{a: src}
		mount := path.Join(src.Orgs.Dir, o) + ":/src:ro"
		err := src.archive(ctx, sec, o, "", mount, nil, pw, "create")
		_ = pw.CloseWithError(err)
		errc <- err
	}()
	q := *dst
	q.Stdin = pr
	// restore's "Start with: berth up <org>" would name the wrong host here; migrate starts it.
	q.Stdout = &dropLines{w: dst.Stdout, prefix: "Start with: "}
	rerr := q.Restore(ctx, []string{"-", "--as", name, "--no-start", "--no-rehydrate"})
	_ = pr.Close()
	serr := <-errc
	if serr != nil {
		return fmt.Errorf("reading %s: %w", o, serr)
	}
	return rerr
}

// dropLines passes output through, but for lines starting with prefix.
type dropLines struct {
	w      io.Writer
	prefix string
	buf    []byte
}

func (d *dropLines) Write(p []byte) (int, error) {
	d.buf = append(d.buf, p...)
	for {
		i := strings.IndexByte(string(d.buf), '\n')
		if i < 0 {
			return len(p), nil
		}
		line := d.buf[:i+1]
		if !strings.HasPrefix(string(line), d.prefix) {
			if _, err := d.w.Write(line); err != nil {
				return len(p), err
			}
		}
		d.buf = d.buf[i+1:]
	}
}

// removeCopy removes a stopped copy this migrate made (its sshd keys are root's, so as root).
func (a *App) removeCopy(ctx context.Context, name string) error {
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	return a.quietRun(ctx, "docker", "run", "--rm", "-v", a.Orgs.Dir+":/orgs", "--entrypoint", "rm", set.Tag, "-rf", "/orgs/"+name)
}

// manifestOrg is the org named in a copy's .ccenv-manifest.json ("" if there is none).
func (a *App) manifestOrg(name string) string {
	b, err := a.Host.FS.ReadFile(path.Join(a.Orgs.Dir, name, ".ccenv-manifest.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Org string `json:"org"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.Org
}

// restoreAdjusts are the org.env keys restore changes to fit the new host.
var restoreAdjusts = map[string]bool{"SSH_PORT": true, "TTYD_PORT": true, "BIND_ADDR": true, "MANAGER": true}

// verifyCopy checks dst's copy against src's org: org.env (but what restore adjusts), every file
// under config/, and (strict: the source is stopped) every file's path and size but what the
// backup skips. Without strict, file differences are reported but don't fail: the org was running.
func (a *App) verifyCopy(ctx context.Context, src *App, o string, dst *App, name string, strict bool) error {
	envOf := func(b *App, org string) map[string]string {
		m := map[string]string{}
		raw, _ := b.Host.FS.ReadFile(b.Orgs.EnvPath(org))
		for _, l := range fileLines(raw) {
			if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(k, "#") && !restoreAdjusts[k] {
				m[k] = v
			}
		}
		return m
	}
	se, de := envOf(src, o), envOf(dst, name)
	for k, v := range se {
		if de[k] != v {
			return fmt.Errorf("org.env: %s differs", k)
		}
	}
	for k := range de {
		if _, ok := se[k]; !ok {
			return fmt.Errorf("org.env: %s is only in the copy", k)
		}
	}
	sl, err := src.fileList(ctx, o)
	if err != nil {
		return fmt.Errorf("listing %s: %w", o, err)
	}
	dl, err := dst.fileList(ctx, name)
	if err != nil {
		return fmt.Errorf("listing the copy: %w", err)
	}
	skipped := dst.manifestSkipped(name)
	var diffs []string
	for p, size := range sl {
		if isSkipped(p, skipped) {
			continue
		}
		if ds, ok := dl[p]; !ok {
			diffs = append(diffs, "missing in the copy: "+p)
		} else if ds != size {
			diffs = append(diffs, fmt.Sprintf("size differs: %s (%s here, %s in the copy)", p, size, ds))
		}
	}
	for p := range dl {
		if _, ok := sl[p]; !ok && !isSkipped(p, skipped) {
			diffs = append(diffs, "only in the copy: "+p)
		}
	}
	sort.Strings(diffs)
	config := 0
	for _, d := range diffs {
		if strings.Contains(d, " config/") {
			config++
		}
	}
	switch {
	case len(diffs) == 0:
		fmt.Fprintf(a.Stdout, "Checked: org.env and %d files match.\n", len(dl))
		return nil
	case strict || config > 0:
		return fmt.Errorf("%d difference(s), e.g. %s", len(diffs), strings.Join(diffs[:min(3, len(diffs))], "; "))
	default:
		fmt.Fprintf(a.Stdout, "Checked: org.env and config match; %d file(s) changed while it ran (the final copy, with the org stopped, takes them), e.g. %s\n",
			len(diffs), strings.Join(diffs[:min(3, len(diffs))], "; "))
		return nil
	}
}

// fileList is every file and symlink under the org's dir, with its size, as root (sshd's keys are
// root's), in berth's image with no network. org.env and the copy's own manifest are left out:
// org.env is compared by key, and the manifest is the copy's.
func (a *App) fileList(ctx context.Context, org string) (map[string]string, error) {
	set, _, err := a.composeAssets()
	if err != nil {
		return nil, err
	}
	out, err := a.capture(ctx, true, "docker", "run", "--rm", "--network", "none", "-v", path.Join(a.Orgs.Dir, org)+":/o:ro",
		"--entrypoint", "sh", set.Tag, "-c",
		`cd /o && find . \( -type f -o -type l \) ! -path ./org.env ! -path ./.ccenv-manifest.json -printf '%P\t%s\n'`)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if p, s, ok := strings.Cut(l, "\t"); ok {
			m[p] = s
		}
	}
	return m, nil
}

// manifestSkipped is the paths the backup skipped (regenerable data), from the copy's manifest.
func (a *App) manifestSkipped(name string) []string {
	b, err := a.Host.FS.ReadFile(path.Join(a.Orgs.Dir, name, ".ccenv-manifest.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Skipped []string `json:"skipped"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	for i, s := range m.Skipped {
		m.Skipped[i] = strings.TrimPrefix(s, "./")
	}
	return m.Skipped
}

func isSkipped(p string, skipped []string) bool {
	for _, s := range skipped {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// isRegisteredTarget reports whether migrate's target names a registered host (or local), as
// opposed to a [user@]host for ssh.
func (a *App) isRegisteredTarget(target string) bool {
	if target == hosts.Local {
		return true
	}
	if hosts.CheckName(target) != nil {
		return false
	}
	_, err := a.base().registered(target)
	return err == nil
}
