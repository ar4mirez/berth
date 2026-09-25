package app

import (
	"bytes"
	"context"
	"fmt"
	"path"

	"github.com/ar4mirez/berth/internal/org"
)

// Takeover is `berth takeover <org>`: the cutover step that makes a ccenv org berth's
// (MANAGER=berth), and nothing else. The container isn't touched: it keeps running, on its current
// image, until its next restart under berth (docs/cutover.md). It refuses when the state root's own
// ccenv doesn't refuse berth's orgs, since both tools could then manage the org.
func (a *App) Takeover(_ context.Context, o string) error {
	if err := a.State.Writable("take over " + o); err != nil {
		return err
	}
	if err := a.needOrg(o); err != nil {
		return err
	}
	if a.env(o, "MANAGER") == "berth" {
		fmt.Fprintf(a.Stdout, "%s is already berth's (MANAGER=berth); nothing to do.\n", o)
		return nil
	}
	if legacy := path.Join(a.State.Home.Path, "ccenv"); a.isFile(legacy) {
		b, err := a.Host.FS.ReadFile(legacy)
		if err != nil {
			return err
		}
		if !bytes.Contains(b, []byte("berth_owned")) {
			return fmt.Errorf("%s doesn't refuse berth's orgs (no berth_owned guard): port that patch first, or both tools could manage %s", legacy, o)
		}
	}
	if err := a.setManager(o, "berth"); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s is now berth's (MANAGER=berth). Nothing was restarted: the container keeps running as it is.\n", o)
	fmt.Fprintf(a.Stdout, "Its next %s restart %s moves it to berth's image (a short restart: plan it). Undo: %s handback %s\n", Tool, o, Tool, o)
	return nil
}

// Handback is `berth handback <org>`: the rollback of takeover, MANAGER=ccenv. Nothing restarts;
// ccenv manages the org again from its next command.
func (a *App) Handback(_ context.Context, o string) error {
	if err := a.State.Writable("hand " + o + " back to ccenv"); err != nil {
		return err
	}
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	if err := a.setManager(o, "ccenv"); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s is ccenv's again (MANAGER=ccenv). Nothing was restarted.\n", o)
	fmt.Fprintf(a.Stdout, "If berth had restarted it on berth's image, its next `ccenv restart %s` moves it back to claude-env.\n", o)
	return nil
}

// setManager rewrites org.env in place (same inode and mode): MANAGER replaced where it is, or added
// as the first line, as init writes it.
func (a *App) setManager(o, m string) error {
	p := a.Orgs.EnvPath(o)
	b, err := a.Host.FS.ReadFile(p)
	if err != nil {
		return err
	}
	if _, ok := org.Lookup(b, "MANAGER"); ok {
		return a.Orgs.Set(o, "MANAGER", m)
	}
	return a.Host.FS.WriteFile(p, append([]byte("MANAGER="+m+"\n"), b...), 0o600)
}
