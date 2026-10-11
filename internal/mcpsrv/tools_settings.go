package mcpsrv

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// The rest of an org's settings and the machine's own, as tools (#167): repos beyond add and
// remove, a variable taken away, the backup schedule, Remote Control's log and login, and the image.

type repoPolicyArgs struct {
	Org     string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Mode    string `json:"mode" jsonschema:"enforce (unregistered folders go to quarantine), warn (they are reported), or off"`
	Confirm string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop the work running in it"`
}

func (a repoPolicyArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a repoPolicyArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "mode": a.Mode}
}

type repoAdoptArgs struct {
	Org  string   `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Dirs []string `json:"dirs,omitempty" jsonschema:"folders under /workspace, or in the org's quarantine, to register as repos"`
	All  bool     `json:"all,omitempty" jsonschema:"every folder that isn't registered"`
}

func (a repoAdoptArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "dirs": a.Dirs, "all": a.All}
}

type repoNewArgs struct {
	Org         string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Repo        string `json:"repo" jsonschema:"owner/repo for a new repository on GitHub, or a name with local"`
	Local       bool   `json:"local,omitempty" jsonschema:"no GitHub repository yet: a local one, to publish later"`
	Visibility  string `json:"visibility,omitempty" jsonschema:"private (the default), public or internal"`
	Description string `json:"description,omitempty" jsonschema:"the repository's description on GitHub"`
	Readme      bool   `json:"readme,omitempty" jsonschema:"start it with a README"`
	Dir         string `json:"dir,omitempty" jsonschema:"the folder under /workspace (the repository's name when empty)"`
}

func (a repoNewArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "repo": a.Repo, "local": a.Local, "visibility": a.Visibility, "dir": a.Dir}
}

type envUnsetArgs struct {
	Org       string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name      string `json:"name" jsonschema:"the variable's name, as env_list shows it"`
	NoRestart bool   `json:"no_restart,omitempty" jsonschema:"leave a running org as it is: the variable is gone at its next restart"`
	Confirm   string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop the work running in it"`
}

func (a envUnsetArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a envUnsetArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name, "no_restart": a.NoRestart}
}

type logoutArgs struct {
	Org     string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	All     bool   `json:"all,omitempty" jsonschema:"also remove the org's Claude token, which restarts a running org"`
	Confirm string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to sign it out"`
}

func (a logoutArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a logoutArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "all": a.All}
}

type scheduleOnArgs struct {
	At   string `json:"at,omitempty" jsonschema:"when, each night, as HH:MM in the machine's time (03:00 when empty)"`
	Keep int    `json:"keep,omitempty" jsonschema:"how many backups of each org to keep (14 when empty)"`
}

func (a scheduleOnArgs) auditArgs() map[string]any { return map[string]any{"at": a.At, "keep": a.Keep} }

// nothing is the argument of a tool that changes this machine and takes none.
type nothing struct{}

func (nothing) auditArgs() map[string]any { return map[string]any{} }

// ScheduleChanged is the result of the schedule tools.
type ScheduleChanged struct {
	Output string `json:"output"`
	// Schedule is the schedule after the change.
	Schedule ops.Schedule `json:"schedule"`
}

// Done is the result of a tool that changes this machine, not an org: what berth printed doing it.
type Done struct {
	Output string `json:"output"`
}

func (s *server) settingsTools() {
	// --- reading -----------------------------------------------------------------------------
	add(s, "repo_audit", "repo", "audit", "An org's workspace checked against its registered repos: the policy, what isn't registered, and what is in quarantine.",
		func(ctx context.Context, c *call, in orgArg) (ops.RepoAuditDoc, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.RepoAuditDoc{}, err
			}
			return ops.GetRepoAudit(b, o)
		})
	add(s, "remote_logs", "remote", "logs", "The last lines of Remote Control's log in a running org: why it is off, blocked, or waiting for a login.",
		func(ctx context.Context, c *call, in orgArg) (ops.Logs, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Logs{}, err
			}
			if err := b.Remote(ctx, o, "logs"); err != nil {
				return ops.Logs{}, err
			}
			out := ops.Logs{Org: o, Lines: []string{}}
			if text := strings.TrimRight(c.stdout.String(), "\n"); text != "" {
				out.Lines = strings.Split(text, "\n")
			}
			return out, nil
		})

	// --- writing -----------------------------------------------------------------------------
	add(s, "repo_adopt", "repo", "adopt", "Register folders that are already in an org's /workspace, or bring them back from its quarantine.",
		func(ctx context.Context, c *call, in repoAdoptArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			var args []string
			switch {
			case in.All && len(in.Dirs) > 0:
				return Changed{}, errors.New("name the folders, or set all: not both")
			case in.All:
				args = []string{"--all"}
			case len(in.Dirs) == 0:
				return Changed{}, errors.New("dirs is required (or set all)")
			}
			for _, d := range in.Dirs {
				if err := flagLike("a folder", d); err != nil {
					return Changed{}, err
				}
				args = append(args, d)
			}
			err = b.Repo(ctx, "adopt", o, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "repo_new", "repo", "new", "Create a new repository for an org and register it: on GitHub (it needs the org signed in to gh), or a local one to publish later.",
		func(ctx context.Context, c *call, in repoNewArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Repo == "" {
				return Changed{}, errors.New("repo is required (owner/repo, or a name with local)")
			}
			for what, v := range map[string]string{"repo": in.Repo, "dir": in.Dir, "description": in.Description} {
				if err := flagLike(what, v); err != nil {
					return Changed{}, err
				}
			}
			args := []string{in.Repo}
			switch in.Visibility {
			case "":
			case "private", "public", "internal":
				args = append(args, "--"+in.Visibility)
			default:
				return Changed{}, errors.New("visibility is private, public or internal")
			}
			if in.Local {
				args = append(args, "--local")
			}
			if in.Readme {
				args = append(args, "--readme")
			}
			if in.Description != "" {
				args = append(args, "--description", in.Description)
			}
			if in.Dir != "" {
				args = append(args, "--dir", in.Dir)
			}
			err = b.Repo(ctx, "new", o, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "packages_build", "pkg", "build", "Build an org's image with its system packages now, so its next restart doesn't wait for it. The org keeps running.",
		func(ctx context.Context, c *call, in orgWrite) (PackagesChanged, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return PackagesChanged{}, err
			}
			if err := b.Pkg(ctx, o, []string{"build"}); err != nil {
				return PackagesChanged{Output: c.output()}, err
			}
			p, err := ops.GetPackages(ctx, b, o)
			return PackagesChanged{Output: c.output(), Packages: p}, err
		})
	add(s, "schedule_on", "schedule", "", "Back up every org on this machine each night: a systemd user timer, or a crontab line where there is no systemd.",
		func(ctx context.Context, c *call, in scheduleOnArgs) (ScheduleChanged, error) {
			var args []string
			if in.At != "" {
				if err := flagLike("at", in.At); err != nil {
					return ScheduleChanged{}, err
				}
				args = append(args, "--at", in.At)
			}
			if in.Keep < 0 {
				return ScheduleChanged{}, errors.New("keep is how many backups to keep: a positive number")
			}
			if in.Keep > 0 {
				args = append(args, "--keep", strconv.Itoa(in.Keep))
			}
			return c.schedule(ctx, args)
		})
	add(s, "schedule_off", "schedule", "off", "Stop the nightly backups on this machine. The backups there are stay.",
		func(ctx context.Context, c *call, _ nothing) (ScheduleChanged, error) {
			return c.schedule(ctx, []string{"off"})
		})
	add(s, "schedule_run", "schedule", "run", "Run the nightly backup now, as the schedule would.",
		func(ctx context.Context, c *call, _ nothing) (ScheduleChanged, error) {
			return c.schedule(ctx, []string{"run"})
		})
	add(s, "image_pull", "pull", "", "Get berth's released image on this machine now, so the orgs' next restarts don't wait for it. Nothing restarts.",
		func(ctx context.Context, c *call, _ nothing) (Done, error) {
			err := c.app.Pull(ctx)
			return Done{Output: c.output()}, err
		})

	// --- restarting (and confirm) -----------------------------------------------------------------
	add(s, "repo_policy_set", "repo", "policy/<mode>", "Set what happens to folders in an org's /workspace that aren't registered repos. A running org is restarted to apply it.",
		func(ctx context.Context, c *call, in repoPolicyArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Mode != "enforce" && in.Mode != "warn" && in.Mode != "off" {
				return Changed{}, errors.New("mode is enforce, warn or off")
			}
			err = b.Repo(ctx, "policy", o, []string{in.Mode})
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "env_unset", "env", "unset", "Remove one of an org's custom variables. A running org is restarted to apply it, unless no_restart.",
		func(ctx context.Context, c *call, in envUnsetArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if in.Name == "" {
				return Changed{}, errors.New("name is required")
			}
			if err := flagLike("name", in.Name); err != nil {
				return Changed{}, err
			}
			args := []string{o, "unset", in.Name}
			if in.NoRestart {
				args = append(args, "--no-restart")
			}
			err = b.Env(ctx, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
	add(s, "org_logout", "logout", "", "Sign an org out of Remote Control. With all, its Claude token is removed too, which restarts a running org. Signing in again needs a terminal.",
		func(ctx context.Context, c *call, in logoutArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			flag := ""
			if in.All {
				flag = "--all"
			}
			err = b.Logout(ctx, o, flag)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
}

func (c *call) schedule(ctx context.Context, args []string) (ScheduleChanged, error) {
	if err := c.app.Schedule(ctx, args); err != nil {
		return ScheduleChanged{Output: c.output()}, err
	}
	sch, err := ops.GetSchedule(ctx, c.app)
	return ScheduleChanged{Output: c.output(), Schedule: sch}, err
}
