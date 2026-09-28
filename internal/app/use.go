package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// The default org (#55): `berth use acme@box1` saves it, and org commands take it when their org
// argument is missing. Only when it's missing (see OrgMissing): a mistyped org is never replaced.

func (a *App) contextFile() string { return path.Join(a.hostPaths().Dir, "context") }

// ContextOrg is the saved default org ("" for none).
func (a *App) ContextOrg() string {
	b, err := a.Operator.FS.ReadFile(a.contextFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var orgAddress = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(@[a-z0-9][a-z0-9-]*)?$`)

// OrgMissing reports whether x, where a command expects its org, isn't one: the end of the line, a
// flag, one of the command's own verbs, or anything that can't be an org name (owner/repo, a
// sentence for run). A plausible org name that doesn't exist is not missing: it is an error.
func OrgMissing(x string, present bool, verbs []string) bool {
	if !present || strings.HasPrefix(x, "-") || !orgAddress.MatchString(x) {
		return true
	}
	for _, v := range verbs {
		if x == v {
			return true
		}
	}
	return false
}

// Use is `berth use [<org>[@host]] | --clear`.
func (a *App) Use(ctx context.Context, args []string) error {
	switch {
	case len(args) == 0:
		if c := a.ContextOrg(); c != "" {
			fmt.Fprintf(a.Stdout, "%s (org commands take it when you leave the org out)\n", c)
		} else {
			fmt.Fprintf(a.Stdout, "No default org. Set one: %s use <org>[@host]\n", Tool)
		}
		return nil
	case len(args) > 1:
		return fmt.Errorf("usage: %s use [<org>[@host]] | --clear", Tool)
	}
	if err := a.State.Writable("change the default org"); err != nil {
		return err
	}
	if args[0] == "--clear" {
		if err := a.Operator.FS.Remove(a.contextFile()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		fmt.Fprintln(a.Stdout, "No default org now.")
		return nil
	}
	if !orgAddress.MatchString(args[0]) {
		return fmt.Errorf("invalid org '%s' (<org> or <org>@<host>)", args[0])
	}
	b, o, done, err := a.At(ctx, args[0])
	if err != nil {
		return err
	}
	defer done()
	if err := b.needOrg(o); err != nil {
		return err
	}
	if err := a.Operator.FS.MkdirAll(a.hostPaths().Dir, 0o700); err != nil {
		return err
	}
	if err := a.Operator.FS.WriteFileAtomic(a.contextFile(), []byte(args[0]+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Default org: %s. Org commands take it when you leave the org out (berth up, berth fw show, …).\n", args[0])
	return nil
}
