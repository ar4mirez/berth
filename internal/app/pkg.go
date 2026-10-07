package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/assets"
	"github.com/ar4mirez/berth/internal/ops"
)

// System packages in an org's image (#106, internal/ops/packages.go; berth-only). The list is
// orgs/<org>/config/packages.txt. berth builds berth/claude-env-<org>:<base>-<list> from a generated
// Dockerfile (FROM berth's image, then apt-get install), and compose runs the org on it.

// pkgWrites are the pkg subcommands that change the list or build the image.
var pkgWrites = map[string]bool{"add": true, "rm": true, "remove": true, "build": true}

// Pkg is `berth pkg <org> [ls | add <package|@preset>... | rm <entry>... | presets | build]`.
func (a *App) Pkg(ctx context.Context, o string, args []string) error {
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	if pkgWrites[sub] {
		if err := a.writable(o, "change "+o+"'s packages"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	build := true
	var names []string
	for _, x := range args {
		switch {
		case x == "--no-build" && (sub == "add" || sub == "rm" || sub == "remove"):
			build = false
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		default:
			names = append(names, x)
		}
	}
	usage := fmt.Errorf("usage: %s pkg <org> [ls | add <package|@preset>... | rm <entry>... | presets | build] [--no-build]", Tool)
	switch sub {
	case "ls", "list":
		if len(names) > 0 {
			return usage
		}
		return a.pkgLs(ctx, o)
	case "presets":
		pre := ops.GetPackagePresets()
		if a.Output == OutputJSON {
			return a.writeJSON(pre)
		}
		for _, p := range pre.Presets {
			sayf(a.Stdout, "%s\n  %s\n", p.Name, strings.Join(p.Packages, " "))
		}
		return nil
	case "build":
		if len(names) > 0 {
			return usage
		}
		return a.pkgBuild(ctx, o, true)
	case "add", "rm", "remove":
		if len(names) == 0 {
			return usage
		}
		return a.pkgEdit(ctx, o, sub == "add", names, build)
	}
	return usage
}

func (a *App) pkgLs(ctx context.Context, o string) error {
	p, err := ops.GetPackages(ctx, a, o)
	if err != nil {
		return err
	}
	// The image as compose would run it: BERTH_IMAGE_DIR changes berth's own.
	if set, own, err := a.composeAssets(); err == nil && own && set.Tag != p.BaseImage {
		p.BaseImage, p.Image = set.Tag, ops.OrgImage(set.Tag, o, p.Packages)
		p.Built = a.quietRun(ctx, "docker", "image", "inspect", p.Image) == nil
	}
	if a.Output == OutputJSON {
		return a.writeJSON(p)
	}
	if len(p.Entries) == 0 {
		sayf(a.Stdout, "(no extra packages; add some: %s pkg %s add <package|@preset>)\n", Tool, o)
		return nil
	}
	fmt.Fprintln(a.Stdout, p.File)
	for _, e := range p.Entries {
		fmt.Fprintln(a.Stdout, "  "+e)
	}
	for _, e := range p.Invalid {
		sayf(a.Stderr, "%s: '%s' in %s isn't a package name or a known preset: it is left out\n", Tool, e, p.File)
	}
	state := "built"
	if !p.Built {
		state = "not built yet: " + Tool + " pkg " + o + " build, or the org's next start builds it"
	}
	sayf(a.Stdout, "%d packages, in %s (%s)\n", len(p.Packages), p.Image, state)
	return nil
}

// pkgEdit adds or removes entries, then builds the org's image, so a package that doesn't exist
// fails now and not at the org's next restart. A failed build puts the list back as it was.
func (a *App) pkgEdit(ctx context.Context, o string, add bool, names []string, build bool) error {
	if add {
		for _, n := range names {
			if err := ops.CheckPackage(n); err != nil {
				return err
			}
		}
	}
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	f := ops.PackagesFile(a, o)
	before, err := a.Host.FS.ReadFile(f)
	missing := err != nil
	content := before
	if missing {
		content = []byte(ops.PackagesHeader(o))
	}
	has := func(e string) bool {
		for _, l := range fileLines(content) {
			if strings.TrimSpace(l) == e {
				return true
			}
		}
		return false
	}
	changed := false
	for _, n := range names {
		switch {
		case add && has(n):
			fmt.Fprintln(a.Stdout, "already listed: "+n)
		case add:
			if len(content) > 0 && content[len(content)-1] != '\n' {
				content = append(content, '\n')
			}
			content = append(content, n+"\n"...)
			fmt.Fprintln(a.Stdout, "added: "+n)
			changed = true
		case !has(n):
			fmt.Fprintln(a.Stdout, "not in the list: "+n)
		default:
			var kept []byte
			for _, l := range fileLines(content) {
				if strings.TrimSpace(l) != n {
					kept = append(kept, l+"\n"...)
				}
			}
			content = kept
			fmt.Fprintln(a.Stdout, "removed: "+n)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := a.Host.FS.WriteFile(f, content, 0o644); err != nil {
		return err
	}
	unlock()
	if !build {
		sayf(a.Stdout, "Saved. %s's image is built at its next start, or now with: %s pkg %s build\n", o, Tool, o)
		return nil
	}
	if err := a.pkgBuild(ctx, o, false); err != nil {
		// Put the list back: the org must not be left with one its image can't be built from.
		relock, lerr := a.lock(ctx)
		if lerr != nil {
			return errors.Join(err, lerr)
		}
		defer relock()
		if missing {
			_ = a.Host.FS.Remove(f)
		} else if werr := a.Host.FS.WriteFile(f, before, 0o644); werr != nil {
			return errors.Join(err, werr)
		}
		// The build's own output says why; this says what it means, with the build's exit code.
		return &ops.Error{Kind: ops.KindCommand, Code: ops.AsError(err).Code, Err: err,
			Msg: "the image didn't build, so " + o + "'s packages are as they were", Hint: "the build's output above says why"}
	}
	return nil
}

// pkgBuild builds the org's image now and says how it reaches the org. Nothing is restarted.
func (a *App) pkgBuild(ctx context.Context, o string, force bool) error {
	set, own, err := a.composeAssets()
	if err != nil {
		return err
	}
	if !own {
		return errors.New("extra packages need berth's own image (this state root runs a ccenv checkout's compose.yml)")
	}
	img, err := a.orgImage(ctx, o, set, true, force)
	if err != nil {
		return err
	}
	how := Tool + " up " + o
	if a.running(ctx, o) {
		how = Tool + " restart " + o + " (that stops the work running in it)"
	}
	if img.tag == set.Tag {
		sayf(a.Stdout, "%s has no extra packages: it runs on %s. Nothing was restarted; it applies with: %s\n", o, set.Tag, how)
		return nil
	}
	sayf(a.Stdout, "%s is ready (%d packages on %s). Nothing was restarted; it applies with: %s\n", img.tag, img.packages, set.Tag, how)
	return nil
}

// orgImg is the image an org runs on and the build context compose is given for it.
type orgImg struct {
	tag, dir string
	packages int
}

// orgContextDir is where an org's generated Dockerfile is: next to berth's own assets.
func (a *App) orgContextDir(o string) string {
	return path.Join(a.State.Home.Path, assets.Dir+"-orgs", o)
}

// orgImage is the image org o runs on: berth's (set) when it lists no packages, else its own,
// built on top. With ensure, the Dockerfile is written and the image built if it isn't there (or
// rebuilt, with force): first berth's own image, pulled or built, since the org's starts from it.
// Without ensure nothing is written or run: the names only.
func (a *App) orgImage(ctx context.Context, o string, set assets.Set, ensure, force bool) (orgImg, error) {
	pkgs, bad := ops.ExpandPackages(ops.PackageEntries(a, o))
	if ensure {
		for _, e := range bad {
			sayf(a.Stderr, "%s: '%s' in %s isn't a package name or a known preset: it is left out\n", Tool, e, ops.PackagesFile(a, o))
		}
	}
	img := orgImg{tag: ops.OrgImage(set.Tag, o, pkgs), dir: set.ImageDir, packages: len(pkgs)}
	if len(pkgs) == 0 {
		return img, nil
	}
	img.dir = a.orgContextDir(o)
	if !ensure {
		return img, nil
	}
	if err := a.Host.FS.MkdirAll(img.dir, 0o755); err != nil {
		return img, err
	}
	if err := a.Host.FS.WriteFile(path.Join(img.dir, "Dockerfile"), []byte(ops.OrgDockerfile(set.Tag, o, pkgs)), 0o644); err != nil {
		return img, err
	}
	if !force && a.quietRun(ctx, "docker", "image", "inspect", img.tag) == nil {
		return img, nil
	}
	// The base: there already, or the released image, or a local build as `berth build` does.
	if a.quietRun(ctx, "docker", "image", "inspect", set.Tag) != nil && !a.imageFromRelease(ctx) {
		a.Progress.Step("build", "building "+set.Tag)
		if err := a.Build(ctx, nil); err != nil {
			return img, err
		}
	}
	a.Progress.Step("packages", fmt.Sprintf("building %s (%d packages)", img.tag, len(pkgs)))
	sayf(a.Stderr, "%s: building %s's image with its %d packages\n", Tool, o, len(pkgs))
	if err := a.passthrough(ctx, false, "docker", "build", "-t", img.tag, img.dir); err != nil {
		return img, err
	}
	return img, nil
}
