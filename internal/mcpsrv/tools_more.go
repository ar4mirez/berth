package mcpsrv

import (
	"context"
	"errors"

	"github.com/ar4mirez/berth/internal/ops"
)

// More of berth's operations as tools (#167): what the dashboards do beyond the first set. Each is
// a command's own code with its arguments typed, under the same gate.

// orgWrite is the argument of a tool that changes one org and takes nothing else.
type orgWrite struct {
	Org string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
}

func (a orgWrite) auditArgs() map[string]any { return map[string]any{"org": a.Org} }

type pkgEditArgs struct {
	Org     string   `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Entries []string `json:"entries" jsonschema:"system packages by their Debian name, or @presets (package_presets lists them)"`
	NoBuild bool     `json:"no_build,omitempty" jsonschema:"save the list only; the org's image is built at its next start"`
}

func (a pkgEditArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "entries": a.Entries, "no_build": a.NoBuild}
}

type orgCreateArgs struct {
	Org   string `json:"org" jsonschema:"the new org's name: lowercase letters, digits and dashes; acme@box1 creates it on a registered host"`
	Name  string `json:"name,omitempty" jsonschema:"the name its git commits carry (the host's own git user.name when empty)"`
	Email string `json:"email,omitempty" jsonschema:"the email its git commits carry (the host's own git user.email when empty)"`
}

func (a orgCreateArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name, "email": a.Email}
}

type orgDestroyArgs struct {
	Org         string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Confirm     string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to remove it, with its history and secrets"`
	KeepBackups bool   `json:"keep_backups,omitempty" jsonschema:"leave its backups in the backups directory"`
}

func (a orgDestroyArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a orgDestroyArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "keep_backups": a.KeepBackups}
}

// PackagesChanged is the result of packages_add and packages_remove.
type PackagesChanged struct {
	Output string `json:"output"`
	// Packages is the list after the change.
	Packages ops.Packages `json:"packages"`
}

func (s *server) moreTools() {
	// --- reading -----------------------------------------------------------------------------
	add(s, "package_presets", "pkg", "presets", "The @presets for system packages and the packages each one stands for.",
		func(context.Context, *call, noArgs) (ops.PackagePresetsDoc, error) {
			return ops.GetPackagePresets(), nil
		})

	// --- writing -----------------------------------------------------------------------------
	add(s, "repo_sync", "repo", "sync", "Clone an org's registered repos that aren't in its /workspace yet. Needs the org running.",
		func(ctx context.Context, c *call, in orgWrite) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			err = b.Repo(ctx, "sync", o, nil)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	for _, verb := range []string{"on", "off", "reload"} {
		what := map[string]string{
			"on":     "Turn an org's egress firewall on: only what its allowlist names can be reached. Applied live; nothing restarts.",
			"off":    "Turn an org's egress firewall off: the org can reach anything until it is turned on again. Applied live; nothing restarts.",
			"reload": "Apply an org's allowlist again in its running container, resolving its names anew: for a host whose addresses changed. Nothing restarts.",
		}[verb]
		add(s, "firewall_"+verb, "fw", verb, what,
			func(ctx context.Context, c *call, in orgWrite) (FirewallChanged, error) {
				b, o, err := c.at(ctx, in.Org)
				if err != nil {
					return FirewallChanged{}, err
				}
				if err := b.Fw(ctx, o, []string{verb}); err != nil {
					return FirewallChanged{Output: c.output()}, err
				}
				fw, err := ops.GetFirewall(ctx, b, o)
				return FirewallChanged{Output: c.output(), Firewall: fw}, err
			})
	}
	add(s, "packages_add", "pkg", "add", "Add system packages to an org's image, and build it. The org keeps running on its current image: the packages are there after its next restart.",
		func(ctx context.Context, c *call, in pkgEditArgs) (PackagesChanged, error) {
			return c.pkgEdit(ctx, "add", in)
		})
	add(s, "packages_remove", "pkg", "rm", "Remove entries from the system packages an org's image adds, exactly as written there. It applies at the org's next restart.",
		func(ctx context.Context, c *call, in pkgEditArgs) (PackagesChanged, error) {
			return c.pkgEdit(ctx, "rm", in)
		})
	add(s, "remote_restart", "remote", "restart", "Restart Remote Control inside a running org: the service, not the container. Sessions in claude.ai/code reconnect.",
		func(ctx context.Context, c *call, in orgWrite) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			err = b.Remote(ctx, o, "restart")
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "org_create", "init", "", "Create an org: its directory, keys and settings. It isn't started, and it has no Claude account yet: org_up starts it, and signing in needs a terminal.",
		func(ctx context.Context, c *call, in orgCreateArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			args := []string{o}
			for _, f := range [][2]string{{"--name", in.Name}, {"--email", in.Email}} {
				if err := flagLike(f[0][2:], f[1]); err != nil {
					return Changed{}, err
				}
				if f[1] != "" {
					args = append(args, f[0], f[1])
				}
			}
			err = b.Init(ctx, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})

	// --- what can't be undone (the admin scope, and confirm) ------------------------------------
	add(s, "org_destroy", "destroy", "", "Remove an org for good: its container, its workspace, Claude's config and history, its keys and secrets, and its backups here unless keep_backups.",
		func(ctx context.Context, c *call, in orgDestroyArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			args := []string{o, "--yes"} // confirmed above: the org's name again, as a person types it
			if in.KeepBackups {
				args = append(args, "--keep-backups")
			}
			err = b.Destroy(ctx, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
}

func (c *call) pkgEdit(ctx context.Context, verb string, in pkgEditArgs) (PackagesChanged, error) {
	b, o, err := c.at(ctx, in.Org)
	if err != nil {
		return PackagesChanged{}, err
	}
	if len(in.Entries) == 0 {
		return PackagesChanged{}, errors.New("entries is required")
	}
	args := []string{verb}
	for _, e := range in.Entries {
		if err := flagLike("an entry", e); err != nil {
			return PackagesChanged{}, err
		}
		args = append(args, e)
	}
	if in.NoBuild {
		args = append(args, "--no-build")
	}
	if err := b.Pkg(ctx, o, args); err != nil {
		return PackagesChanged{Output: c.output()}, err
	}
	p, err := ops.GetPackages(ctx, b, o)
	return PackagesChanged{Output: c.output(), Packages: p}, err
}
