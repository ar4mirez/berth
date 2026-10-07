package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// The tools' inputs. `jsonschema` is each argument's description in the tool's schema.

type noArgs struct{}

type orgArg struct {
	Org string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
}

type accountsArgs struct {
	Orgs []string `json:"orgs,omitempty" jsonschema:"the orgs to report on; every org on this machine when empty"`
}

type logsArgs struct {
	Org   string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Lines int    `json:"lines,omitempty" jsonschema:"how many of the last lines to return (default 100, at most 1000)"`
}

type fwTestArgs struct {
	Org   string   `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Hosts []string `json:"hosts,omitempty" jsonschema:"hosts or URLs to try from inside the org; Anthropic, GitHub and example.com when empty"`
}

type fwEditArgs struct {
	Org     string   `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Entries []string `json:"entries" jsonschema:"allowlist entries: a domain (which covers every name under it), an IPv4 address or range, or an @preset"`
}

func (a fwEditArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "entries": a.Entries}
}

type repoAddArgs struct {
	Org     string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Repo    string `json:"repo" jsonschema:"the repository: owner/repo for GitHub, or a git URL"`
	Dir     string `json:"dir,omitempty" jsonschema:"the folder under /workspace (the repository's name when empty)"`
	Branch  string `json:"branch,omitempty" jsonschema:"the branch to check out (the default branch when empty)"`
	NoClone bool   `json:"no_clone,omitempty" jsonschema:"register only; it is cloned by the next repo sync"`
}

func (a repoAddArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "repo": a.Repo, "dir": a.Dir, "branch": a.Branch, "no_clone": a.NoClone}
}

type repoRemoveArgs struct {
	Org string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Dir string `json:"dir" jsonschema:"the repo's folder under /workspace, as repos_list shows it"`
}

func (a repoRemoveArgs) auditArgs() map[string]any { return map[string]any{"org": a.Org, "dir": a.Dir} }

type backupsArgs struct {
	Org string `json:"org,omitempty" jsonschema:"only this org's backups; every org's when empty"`
}

type backupCreateArgs struct {
	Orgs []string `json:"orgs,omitempty" jsonschema:"the orgs to back up (on this machine)"`
	All  bool     `json:"all,omitempty" jsonschema:"back up every org berth manages on this machine"`
}

func (a backupCreateArgs) auditArgs() map[string]any {
	return map[string]any{"orgs": a.Orgs, "all": a.All}
}

type lifecycleArgs struct {
	Org     string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Confirm string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop the work running in it"`
}

func (a lifecycleArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a lifecycleArgs) auditArgs() map[string]any      { return map[string]any{"org": a.Org} }

// Changed is the result of a tool that changes something: what berth printed doing it.
type Changed struct {
	Org string `json:"org"`
	// Output is what the command line would have printed.
	Output string `json:"output"`
}

// FirewallChanged is the result of firewall_allow and firewall_deny.
type FirewallChanged struct {
	Output string `json:"output"`
	// Firewall is the allowlist after the change.
	Firewall ops.Firewall `json:"firewall"`
}

// BackupsMade is the result of backup_create.
type BackupsMade struct {
	Output string `json:"output"`
	// Backups are the backups there now, newest first.
	Backups ops.Backups `json:"backups"`
}

// flagLike refuses a value that would be read as an option by the command it is handed to.
func flagLike(what, v string) error {
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s can't start with '-': %q", what, v)
	}
	return nil
}

func (s *server) tools() {
	// --- reading -----------------------------------------------------------------------------
	add(s, "hosts_list", "host", "ls", "This machine and every registered host: whether each is reachable, its container engine, and how many orgs it has.",
		func(ctx context.Context, c *call, _ noArgs) (ops.Hosts, error) { return ops.GetHosts(ctx, c.app) })
	add(s, "orgs_list", "ls", "", "Every org, here and on registered hosts: whether it is up, its ports, whether it has a Claude token, and its Remote Control state.",
		func(ctx context.Context, c *call, _ noArgs) (ops.Orgs, error) { return ops.ListOrgs(ctx, c.app), nil })
	add(s, "org_info", "info", "", "An org's connection sheet: its state, address and ports, the claude.ai/code link, and its git public key. Never the browser terminal's password.",
		func(ctx context.Context, c *call, in orgArg) (ops.Info, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Info{}, err
			}
			return ops.GetInfo(ctx, b, o)
		})
	add(s, "org_accounts", "whoami", "", "Which Claude account and GitHub account each org is signed in to.",
		func(ctx context.Context, c *call, in accountsArgs) (ops.Whoami, error) {
			return ops.GetWhoami(ctx, c.app, in.Orgs)
		})
	add(s, "remote_status", "remote", "status", "Remote Control in a running org: on, login-needed, blocked-by-org or restarting, with its link and capacity when on.",
		func(ctx context.Context, c *call, in orgArg) (ops.RemoteStatus, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.RemoteStatus{}, err
			}
			st, err := ops.GetRemoteStatus(ctx, b, o)
			if st.State != "" {
				return st, nil // the state is known even when the capacity line isn't
			}
			return st, err
		})
	add(s, "org_logs", "logs", "", "The last lines of a running org's container log (startup, firewall, Remote Control).",
		func(ctx context.Context, c *call, in logsArgs) (ops.Logs, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Logs{}, err
			}
			return ops.TailLogs(ctx, b, o, in.Lines)
		})
	add(s, "firewall_show", "fw", "show", "An org's egress allowlist as written, and the live firewall status of its container.",
		func(ctx context.Context, c *call, in orgArg) (ops.Firewall, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Firewall{}, err
			}
			return ops.GetFirewall(ctx, b, o)
		})
	add(s, "firewall_presets", "fw", "presets", "The firewall's @presets and the hosts each one allows.",
		func(ctx context.Context, c *call, in orgArg) (ops.Presets, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Presets{}, err
			}
			return ops.GetFirewallPresets(ctx, b, o)
		})
	add(s, "firewall_test", "fw", "test", "Whether a running org can reach each host or URL now, tried from inside its container.",
		func(ctx context.Context, c *call, in fwTestArgs) (ops.FirewallTest, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.FirewallTest{}, err
			}
			for _, h := range in.Hosts {
				if err := flagLike("a host", h); err != nil {
					return ops.FirewallTest{}, err
				}
			}
			return ops.TestFirewall(ctx, b, o, in.Hosts)
		})
	add(s, "repos_list", "repo", "ls", "An org's registered repos, with branch and uncommitted changes when its container reports them, and anything in its workspace that isn't registered.",
		func(ctx context.Context, c *call, in orgArg) (ops.Repos, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Repos{}, err
			}
			return ops.GetRepos(ctx, b, o)
		})
	add(s, "env_list", "env", "ls", "The names of an org's custom environment variables. Values are never returned.",
		func(ctx context.Context, c *call, in orgArg) (ops.Env, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Env{}, err
			}
			return ops.GetEnv(b, o)
		})
	add(s, "packages_list", "pkg", "ls", "The system packages an org's image adds, and that image.",
		func(ctx context.Context, c *call, in orgArg) (ops.Packages, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Packages{}, err
			}
			return ops.GetPackages(ctx, b, o)
		})
	add(s, "schedule_status", "schedule", "status", "The nightly backup schedule on this machine: a systemd timer and its last runs, or a crontab line.",
		func(ctx context.Context, c *call, _ noArgs) (ops.Schedule, error) { return ops.GetSchedule(ctx, c.app) })
	// Listing backups has no command of its own: it reads the backups directory.
	addQuery(s, "backups_list", "The backup files on this machine, newest first: org, when, size and how each is encrypted.",
		func(_ context.Context, c *call, in backupsArgs) (ops.Backups, error) {
			return ops.ListBackups(c.app, in.Org), nil
		})

	// --- writing (--allow-writes) --------------------------------------------------------------
	add(s, "firewall_allow", "fw", "allow", "Add entries to an org's egress allowlist. Applied live in a running org; nothing restarts.",
		func(ctx context.Context, c *call, in fwEditArgs) (FirewallChanged, error) {
			return c.fwEdit(ctx, "allow", in)
		})
	add(s, "firewall_deny", "fw", "deny", "Remove entries from an org's egress allowlist, exactly as written there. Applied live in a running org; nothing restarts.",
		func(ctx context.Context, c *call, in fwEditArgs) (FirewallChanged, error) {
			return c.fwEdit(ctx, "deny", in)
		})
	add(s, "repo_add", "repo", "add", "Register a repository for an org and clone it into /workspace. Only registered repos can be used in an org.",
		func(ctx context.Context, c *call, in repoAddArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Repo == "" {
				return Changed{}, errors.New("repo is required (owner/repo, or a git URL)")
			}
			args := []string{in.Repo}
			for what, v := range map[string]string{"repo": in.Repo, "dir": in.Dir, "branch": in.Branch} {
				if err := flagLike(what, v); err != nil {
					return Changed{}, err
				}
			}
			if in.Dir != "" {
				args = append(args, "--dir", in.Dir)
			}
			if in.Branch != "" {
				args = append(args, "--branch", in.Branch)
			}
			if in.NoClone {
				args = append(args, "--no-clone")
			}
			err = b.Repo(ctx, "add", o, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "repo_remove", "repo", "rm", "Unregister a repo from an org. Its folder is moved to the org's quarantine, not deleted, and `berth repo adopt` brings it back.",
		func(ctx context.Context, c *call, in repoRemoveArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Dir == "" {
				return Changed{}, errors.New("dir is required")
			}
			if err := flagLike("dir", in.Dir); err != nil {
				return Changed{}, err
			}
			err = b.Repo(ctx, "rm", o, []string{in.Dir})
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "backup_create", "backup", "", "Take an encrypted backup of orgs on this machine, into the backups directory. The orgs keep running. Needs a backup key (berth keygen): there is no terminal to ask for a passphrase.",
		func(ctx context.Context, c *call, in backupCreateArgs) (BackupsMade, error) {
			if c.app.BackupNeedsPrompt() {
				return BackupsMade{}, &ops.Error{Kind: ops.KindState, Code: 1, Msg: "there is no backup key, so a backup would ask for a passphrase, and there is no terminal here",
					Hint: "the user can run: berth keygen"}
			}
			var args []string
			if in.All {
				args = append(args, "--all")
			}
			for _, o := range in.Orgs {
				if !ops.OrgName.MatchString(o) {
					return BackupsMade{}, fmt.Errorf("%q isn't an org on this machine (backups are taken where the org is)", o)
				}
				args = append(args, o)
			}
			if len(args) == 0 {
				return BackupsMade{}, errors.New("name the orgs to back up, or set all")
			}
			err := c.app.Backup(ctx, args)
			return BackupsMade{Output: c.output(), Backups: ops.ListBackups(c.app, "")}, err
		})

	// --- restarting (--allow-restarts, and confirm) --------------------------------------------
	add(s, "org_up", "up", "", "Start an org: gets its image if needed, then recreates its container.",
		func(ctx context.Context, c *call, in lifecycleArgs) (Changed, error) {
			return c.lifecycle(ctx, in, func(ctx context.Context, a lifecycleApp, o string) error { return a.Up(ctx, o) })
		})
	add(s, "org_restart", "restart", "", "Restart an org: recreates its container, applying pending changes (a new image, packages, variables).",
		func(ctx context.Context, c *call, in lifecycleArgs) (Changed, error) {
			return c.lifecycle(ctx, in, func(ctx context.Context, a lifecycleApp, o string) error { return a.Restart(ctx, o) })
		})
	add(s, "org_down", "down", "", "Stop an org: stops and removes its container. Its files stay.",
		func(ctx context.Context, c *call, in lifecycleArgs) (Changed, error) {
			return c.lifecycle(ctx, in, func(ctx context.Context, a lifecycleApp, o string) error { return a.Down(ctx, o) })
		})
}

// lifecycleApp is what up, restart and down need of an App.
type lifecycleApp interface {
	Up(ctx context.Context, o string) error
	Restart(ctx context.Context, o string) error
	Down(ctx context.Context, o string) error
}

func (c *call) lifecycle(ctx context.Context, in lifecycleArgs, do func(context.Context, lifecycleApp, string) error) (Changed, error) {
	b, o, err := c.at(ctx, in.Org)
	if err != nil {
		return Changed{}, err
	}
	err = do(ctx, b, o)
	return Changed{Org: in.Org, Output: c.output()}, err
}

func (c *call) fwEdit(ctx context.Context, verb string, in fwEditArgs) (FirewallChanged, error) {
	b, o, err := c.at(ctx, in.Org)
	if err != nil {
		return FirewallChanged{}, err
	}
	if len(in.Entries) == 0 {
		return FirewallChanged{}, errors.New("entries is required")
	}
	if err := b.Fw(ctx, o, append([]string{verb}, in.Entries...)); err != nil {
		return FirewallChanged{Output: c.output()}, err
	}
	fw, err := ops.GetFirewall(ctx, b, o)
	return FirewallChanged{Output: c.output(), Firewall: fw}, err
}
