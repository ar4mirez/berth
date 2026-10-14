package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/ops"
)

// The machine's own operations, and the last of an org's, as tools (#167): the registry of hosts,
// berth's image and berth itself, and what is left of repos, variables and a restored org.

type hostArg struct {
	Host string `json:"host" jsonschema:"a registered host's name, as hosts_list shows it"`
}

type hostWrite struct {
	Host string `json:"host" jsonschema:"a registered host's name, as hosts_list shows it"`
}

func (a hostWrite) auditArgs() map[string]any { return map[string]any{"host": a.Host} }

type hostAddArgs struct {
	Host             string   `json:"host" jsonschema:"the name to give it: lowercase letters, digits and dashes"`
	Address          string   `json:"address" jsonschema:"where it is, as ssh takes it: [user@]host[:port]"`
	Home             string   `json:"home,omitempty" jsonschema:"the state root on that host (berth's default there when empty)"`
	Identities       []string `json:"identities,omitempty" jsonschema:"private key files on this machine to try first; berth then installs a key of its own"`
	Fingerprint      string   `json:"fingerprint,omitempty" jsonschema:"the host's SSH key fingerprint to expect (SHA256:…)"`
	AcceptNewHostKey bool     `json:"accept_new_host_key,omitempty" jsonschema:"trust the key the host presents now, when no fingerprint is given"`
	Engine           string   `json:"engine,omitempty" jsonschema:"its container engine: docker (the default) or podman"`
	Bind             string   `json:"bind,omitempty" jsonschema:"where its orgs' ports listen (berth's default when empty)"`
	NoGuard          bool     `json:"no_guard,omitempty" jsonschema:"don't install the host guard, which keeps org containers away from the host itself"`
	Jump             string   `json:"jump,omitempty" jsonschema:"a jump host (bastion) to reach it through, as ssh -J takes it: [user@]host[:port]"`
	JumpFingerprint  string   `json:"jump_fingerprint,omitempty" jsonschema:"the jump host's SSH key fingerprint to expect (SHA256:…)"`
}

func (a hostAddArgs) auditArgs() map[string]any {
	return map[string]any{"host": a.Host, "address": a.Address, "home": a.Home, "engine": a.Engine, "bind": a.Bind, "no_guard": a.NoGuard, "jump": a.Jump}
}

type hostRemoveArgs struct {
	Host  string `json:"host" jsonschema:"a registered host's name, as hosts_list shows it"`
	Force bool   `json:"force,omitempty" jsonschema:"forget it even if it still has orgs, or can't be reached to check; its orgs are never touched"`
}

func (a hostRemoveArgs) auditArgs() map[string]any {
	return map[string]any{"host": a.Host, "force": a.Force}
}

type repoPublishArgs struct {
	Org        string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Dir        string `json:"dir" jsonschema:"the local repo's folder under /workspace, as repos_list shows it"`
	Repo       string `json:"repo" jsonschema:"owner/repo to create on GitHub and push it to"`
	Visibility string `json:"visibility,omitempty" jsonschema:"private (the default), public or internal"`
}

func (a repoPublishArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "dir": a.Dir, "repo": a.Repo, "visibility": a.Visibility}
}

type envMigrateArgs struct {
	Org      string `json:"org" jsonschema:"an org on this machine, as in acme (not acme@box1: its files are moved where they are)"`
	NoBackup bool   `json:"no_backup,omitempty" jsonschema:"don't take a backup of the org first"`
}

func (a envMigrateArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "no_backup": a.NoBackup}
}

type runArgs struct {
	Org    string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Prompt string `json:"prompt" jsonschema:"what to ask Claude in the org's /workspace; it answers and ends"`
}

// The prompt is the user's own words, and may hold anything: the audit log has that one was run.
func (a runArgs) auditArgs() map[string]any { return map[string]any{"org": a.Org} }

type upgradeArgs struct {
	Version  string `json:"version,omitempty" jsonschema:"the release to install, as vX.Y.Z (the newest when empty)"`
	Rollback bool   `json:"rollback,omitempty" jsonschema:"go back to the version before the last upgrade"`
}

func (a upgradeArgs) auditArgs() map[string]any {
	return map[string]any{"version": a.Version, "rollback": a.Rollback}
}

func init() {
	// Hosts are the operator's: the registry on their machine, and berth's key on another. Running
	// a prompt has an agent drive another, and an upgrade replaces what is serving. None is a tool
	// to hand an agent, whatever the server was started with.
	for _, name := range []string{"host_add", "host_remove", "host_rotate_access", "host_guard_status", "host_guard_on", "host_guard_off", "org_run", "berth_upgrade"} {
		noMCP[name] = true
	}
}

func (s *server) machineTools() {
	// --- reading -----------------------------------------------------------------------------
	add(s, "host_guard_status", "host", "guard/status", "The host guard on a registered host: whether it is installed, its container's state, and its rules. It keeps org containers away from the host itself.",
		func(ctx context.Context, c *call, in hostArg) (ops.HostGuard, error) {
			if err := hostName(in.Host); err != nil {
				return ops.HostGuard{}, err
			}
			// The guard's status is the command's own document: read as --output json prints it.
			c.app.Output = app.OutputJSON
			if err := c.app.HostGuard(ctx, []string{in.Host, "status"}); err != nil {
				return ops.HostGuard{}, err
			}
			var out ops.HostGuard
			return out, json.Unmarshal(c.stdout.Bytes(), &out)
		})

	// --- writing -----------------------------------------------------------------------------
	add(s, "host_add", "host", "add", "Register another machine to run orgs on, over SSH: berth checks it, installs a key of its own there, and by default the host guard.",
		func(ctx context.Context, c *call, in hostAddArgs) (Done, error) {
			if in.Host == "" || in.Address == "" {
				return Done{}, errors.New("host and address are required")
			}
			args := []string{in.Host, in.Address}
			for _, f := range [][2]string{{"--home", in.Home}, {"--fingerprint", in.Fingerprint}, {"--engine", in.Engine}, {"--bind", in.Bind}, {"--jump", in.Jump}, {"--jump-fingerprint", in.JumpFingerprint}} {
				if f[1] != "" {
					args = append(args, f[0], f[1])
				}
			}
			for _, id := range in.Identities {
				args = append(args, "--identity", id)
			}
			for _, v := range append([]string{in.Host, in.Address, in.Home, in.Fingerprint, in.Engine, in.Bind, in.Jump, in.JumpFingerprint}, in.Identities...) {
				if err := flagLike("a value", v); err != nil {
					return Done{}, err
				}
			}
			if in.AcceptNewHostKey {
				args = append(args, "--accept-new-host-key")
			}
			if in.NoGuard {
				args = append(args, "--no-guard")
			}
			err := c.app.HostAdd(ctx, args)
			return Done{Output: c.output()}, err
		})
	add(s, "host_remove", "host", "rm", "Forget a registered host. It is refused while the host has orgs, unless force. Its orgs, and the machine, are never touched.",
		func(ctx context.Context, c *call, in hostRemoveArgs) (Done, error) {
			if err := hostName(in.Host); err != nil {
				return Done{}, err
			}
			args := []string{in.Host}
			if in.Force {
				args = append(args, "--force")
			}
			err := c.app.HostRm(ctx, args)
			return Done{Output: c.output()}, err
		})
	add(s, "host_rotate_access", "host", "rotate-access", "Replace berth's key on a registered host. The new key must log in before the old one is removed, so access is never lost.",
		func(ctx context.Context, c *call, in hostWrite) (Done, error) {
			if err := hostName(in.Host); err != nil {
				return Done{}, err
			}
			err := c.app.HostRotateAccess(ctx, []string{in.Host})
			return Done{Output: c.output()}, err
		})
	for _, verb := range []string{"on", "off"} {
		what := map[string]string{
			"on":  "Install the host guard on a registered host: its org containers can no longer reach the host itself. Applied live; nothing restarts.",
			"off": "Remove the host guard from a registered host: its org containers can reach the host itself again. Nothing restarts.",
		}[verb]
		add(s, "host_guard_"+verb, "host", "guard/"+verb, what,
			func(ctx context.Context, c *call, in hostWrite) (Done, error) {
				if err := hostName(in.Host); err != nil {
					return Done{}, err
				}
				err := c.app.HostGuard(ctx, []string{in.Host, verb})
				return Done{Output: c.output()}, err
			})
	}
	add(s, "org_rehydrate", "rehydrate", "", "Reinstall in a running org what backups leave out: its repos, toolchains and project dependencies. For an org that was restored or moved.",
		func(ctx context.Context, c *call, in orgWrite) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			err = b.Rehydrate(ctx, o)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "image_build", "build", "", "Build berth's image on this machine from its own files. Orgs keep running on theirs until their next restart.",
		func(ctx context.Context, c *call, _ nothing) (Done, error) {
			err := c.app.Build(ctx, nil)
			return Done{Output: c.output()}, err
		})
	add(s, "repo_publish", "repo", "publish", "Create a GitHub repository for one of an org's local repos, and push it there. It needs the org signed in to gh.",
		func(ctx context.Context, c *call, in repoPublishArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Dir == "" || in.Repo == "" {
				return Changed{}, errors.New("dir and repo are required")
			}
			for what, v := range map[string]string{"dir": in.Dir, "repo": in.Repo} {
				if err := flagLike(what, v); err != nil {
					return Changed{}, err
				}
			}
			args := []string{in.Dir, in.Repo}
			switch in.Visibility {
			case "":
			case "private", "public", "internal":
				args = append(args, "--"+in.Visibility)
			default:
				return Changed{}, errors.New("visibility is private, public or internal")
			}
			err = b.Repo(ctx, "publish", o, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "env_migrate", "secrets", "migrate", "Move an org's secrets and custom variables out of org.env into files of their own, after a backup. Nothing restarts: the container takes them from the files at its next restart.",
		func(ctx context.Context, c *call, in envMigrateArgs) (Changed, error) {
			if !ops.OrgName.MatchString(in.Org) {
				return Changed{}, errors.New("org is an org on this machine")
			}
			args := []string{in.Org}
			if in.NoBackup {
				args = append(args, "--no-backup")
			} else if c.app.BackupNeedsPrompt() {
				return Changed{}, &ops.Error{Kind: ops.KindState, Code: 1, Msg: "there is no backup key, so the backup it takes first would ask for a passphrase, and there is no terminal here",
					Hint: "the user can run: berth keygen"}
			}
			err := c.app.SecretsMigrate(ctx, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "org_run", "run", "", "Ask Claude something in a running org's /workspace, without a session: it answers and ends. It acts as the org, with what the org can reach.",
		func(ctx context.Context, c *call, in runArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Prompt == "" {
				return Changed{}, errors.New("prompt is required")
			}
			err = b.Run(ctx, o, []string{in.Prompt})
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "berth_upgrade", "upgrade", "", "Install a verified release of berth on this machine and move to it, or go back to the one before. Nothing restarts: a server that is running keeps its version until it is started again.",
		func(ctx context.Context, c *call, in upgradeArgs) (Done, error) {
			var args []string
			switch {
			case in.Rollback && in.Version != "":
				return Done{}, errors.New("a version, or rollback: not both")
			case in.Rollback:
				args = []string{"--rollback"}
			case in.Version != "":
				if err := flagLike("version", in.Version); err != nil {
					return Done{}, err
				}
				args = []string{"--version", in.Version}
			}
			err := c.app.Upgrade(ctx, args)
			return Done{Output: c.output()}, err
		})
}

// hostName refuses a host's name that is empty, or that a command would read as an option.
func hostName(name string) error {
	if name == "" {
		return errors.New("host is required (a registered host's name)")
	}
	return flagLike("host", name)
}
