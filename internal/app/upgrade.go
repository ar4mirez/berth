package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"runtime"
	"strings"

	"github.com/ar4mirez/berth/internal/assets"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/upgrade"
	"github.com/ar4mirez/berth/internal/version"
)

// Upgrader is what `berth upgrade` needs; tests replace it.
type Upgrader struct {
	Client   *upgrade.Client
	Verifier upgrade.Verifier
}

// Upgrade is `berth upgrade [--version vX.Y.Z] | --rollback` (#42): install a verified release next
// to the current one (~/.local/opt/berth/<version>/berth) and move the berth link to it, or move
// the link back. It never restarts a container: a new image reaches each org at its next restart.
func (a *App) Upgrade(ctx context.Context, args []string) error {
	want, rollback := "", false
	for i := 0; i < len(args); i++ {
		switch x := args[i]; x {
		case "--version":
			if i+1 >= len(args) {
				return errors.New("--version needs a value (vX.Y.Z)")
			}
			i++
			want = nth(args, i)
			if !strings.HasPrefix(want, "v") {
				want = "v" + want
			}
		case "--rollback":
			rollback = true
		default:
			return fmt.Errorf("usage: %s upgrade [--version vX.Y.Z] | --rollback", Tool)
		}
	}
	if err := a.State.Writable("upgrade berth"); err != nil {
		return err
	}
	if a.Self == "" {
		return fmt.Errorf("can't tell where the %s binary is", Tool)
	}
	opt := a.optDir()
	prevFile := path.Join(opt, ".previous")
	if rollback {
		b, err := a.Host.FS.ReadFile(prevFile)
		prev := strings.TrimSpace(string(b))
		if err != nil || prev == "" || !a.isFile(prev) {
			return fmt.Errorf("no previous version recorded in %s", prevFile)
		}
		if err := a.linkBinary(ctx, prev); err != nil {
			return err
		}
		if err := a.Host.FS.WriteFile(prevFile, []byte(a.Self+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Rolled back to %s. Nothing was restarted.\n", prev)
		return nil
	}
	if a.Upgrader == nil || a.Upgrader.Verifier == nil {
		return errors.New("upgrade isn't available in this build")
	}
	rel, err := a.Upgrader.Client.Get(ctx, want)
	if err != nil {
		return err
	}
	cur := "v" + strings.TrimPrefix(version.Version, "v")
	if rel.Tag == cur {
		fmt.Fprintf(a.Stdout, "%s is already at %s.\n", Tool, rel.Tag)
		return nil
	}
	fmt.Fprintf(a.Stdout, "Fetching %s (%s/%s), verifying its signature and checksum…\n", rel.Tag, runtime.GOOS, runtime.GOARCH)
	bin, err := upgrade.Fetch(ctx, a.Upgrader.Client, a.Upgrader.Verifier, rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("%w; nothing was changed", err)
	}
	dir := path.Join(opt, strings.TrimPrefix(rel.Tag, "v"))
	dst := path.Join(dir, Tool)
	if err := a.Host.FS.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := a.Host.FS.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := a.Host.FS.Chmod(tmp, 0o755); err != nil {
		return err
	}
	// It must run, and be the version it claims, before anything points at it.
	out, err := a.capture(ctx, false, tmp, "--version")
	if err != nil || !strings.Contains(out, " "+strings.TrimPrefix(rel.Tag, "v")+" ") {
		_ = a.Host.FS.Remove(tmp)
		return fmt.Errorf("the new binary doesn't run as %s (%q); nothing was changed", rel.Tag, out)
	}
	if err := a.Host.FS.Rename(tmp, dst); err != nil {
		return err
	}
	if err := a.Host.FS.WriteFile(prevFile, []byte(a.Self+"\n"), 0o644); err != nil {
		return err
	}
	if err := a.linkBinary(ctx, dst); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Upgraded to %s (%s). The previous version stays; undo with: %s upgrade --rollback\n", rel.Tag, dst, Tool)
	a.imageNote(ctx, dst)
	return nil
}

// optDir is where versions live: the parent of the current binary's version directory when it's
// installed that way (…/opt/berth/<version>/berth), else ~/.local/opt/berth.
func (a *App) optDir() string {
	if d := path.Dir(path.Dir(a.Self)); path.Base(path.Dir(d)) == "opt" && path.Base(d) == Tool {
		return d
	}
	return a.Getenv("HOME") + "/.local/opt/" + Tool
}

// linkBinary points the berth link at bin, by running that binary's own `install` (so the shell
// completion matches it), into the directory berth was run from when that's a link.
func (a *App) linkBinary(ctx context.Context, bin string) error {
	args := []string{bin, "install"}
	if a.Invoked != "" && a.Invoked != a.Self {
		if fi, err := a.Host.FS.Stat(a.Invoked); err == nil && !fi.IsDir() {
			args = append(args, path.Dir(a.Invoked))
		}
	}
	return a.Host.Exec.Run(ctx, host.Cmd{Args: args, Stdout: a.Stdout, Stderr: a.Stderr})
}

// imageNote says whether the new version changes the container image, which reaches each org only
// at its next restart (docs/image-update.md).
func (a *App) imageNote(ctx context.Context, bin string) {
	set, err := assets.Embedded(a.State.Home.Path)
	if err != nil {
		return
	}
	next, err := a.capture(ctx, true, bin, "image-tag")
	switch {
	case err != nil || next == "":
		fmt.Fprintf(a.Stdout, "Your orgs keep running on their current image; if this version changes it, each org picks it up at its next restart (docs/image-update.md).\n")
	case next == set.Tag:
		fmt.Fprintf(a.Stdout, "The container image is unchanged (%s).\n", next)
	default:
		fmt.Fprintf(a.Stdout, "The container image changes (%s -> %s). Nothing was restarted: each org picks it up at its next restart; get it ready first with: %s pull (or %s build). See docs/image-update.md.\n", set.Tag, next, Tool, Tool)
	}
}

// ImageTag is the hidden `berth image-tag`: the image tag this berth uses, for `upgrade`'s note.
func (a *App) ImageTag() error {
	set, err := assets.Embedded(a.State.Home.Path) // computed, never written: this only reads
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, set.Tag)
	return nil
}
