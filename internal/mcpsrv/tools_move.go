package mcpsrv

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// The operations that replace or move an org (#167): a backup restored, and an org moved to a
// registered host. Each is confirmed, and neither is a tool an MCP server offers.

type restoreArgs struct {
	File        string `json:"file" jsonschema:"the backup to restore: a file's name in the backups directory, as backups_list shows it"`
	As          string `json:"as,omitempty" jsonschema:"restore it under this org name (the backup's own when empty)"`
	Force       bool   `json:"force,omitempty" jsonschema:"replace an org of that name that is there now: it is stopped, and its files are replaced by the backup's"`
	NoStart     bool   `json:"no_start,omitempty" jsonschema:"don't start the restored org"`
	NoRehydrate bool   `json:"no_rehydrate,omitempty" jsonschema:"don't reinstall what backups leave out (repos, toolchains, dependencies) once it runs"`
	Confirm     string `json:"confirm,omitempty" jsonschema:"the file's name again, exactly as in file: the confirmation that the user agreed to restore it"`
}

func (a restoreArgs) confirmation() (string, string) { return a.File, a.Confirm }
func (a restoreArgs) auditArgs() map[string]any {
	return map[string]any{"file": a.File, "as": a.As, "force": a.Force, "no_start": a.NoStart, "no_rehydrate": a.NoRehydrate}
}

type migrateArgs struct {
	Org      string `json:"org" jsonschema:"the org to move, as in acme, or acme@box1 for one on a registered host"`
	Host     string `json:"host" jsonschema:"the registered host to move it to, as hosts_list shows it (local for this machine)"`
	As       string `json:"as,omitempty" jsonschema:"its name on that host (the same when empty)"`
	NoSwitch bool   `json:"no_switch,omitempty" jsonschema:"copy it there and stop: the org keeps running where it is, and nothing is switched"`
	Confirm  string `json:"confirm,omitempty" jsonschema:"the org's name again, exactly as in org: the confirmation that the user agreed to stop it where it is"`
}

func (a migrateArgs) confirmation() (string, string) { return a.Org, a.Confirm }
func (a migrateArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "host": a.Host, "as": a.As, "no_switch": a.NoSwitch}
}

func init() {
	noMCP["backup_restore"], noMCP["org_migrate"] = true, true
}

func (s *server) moveTools() {
	add(s, "backup_restore", "restore", "", "Restore an org from a backup in this machine's backups directory, and start it. It is refused if an org of that name is there, unless force, which replaces it.",
		func(ctx context.Context, c *call, in restoreArgs) (Done, error) {
			// A name in the backups directory, and nothing else: not a path on the server.
			if in.File == "" || in.File != path.Base(in.File) || strings.HasPrefix(in.File, ".") || strings.HasPrefix(in.File, "-") {
				return Done{}, errors.New("file is a backup's name, as backups_list shows it: not a path")
			}
			known := false
			for _, b := range ops.ListBackups(c.app, "").Backups {
				known = known || b.File == in.File
			}
			if !known {
				return Done{}, &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: "no backup named " + in.File + " in " + c.app.BackupsDir()}
			}
			args := []string{path.Join(c.app.BackupsDir(), in.File)}
			if in.As != "" {
				if !ops.OrgName.MatchString(in.As) {
					return Done{}, errors.New("as is an org's name: lowercase letters, digits and dashes")
				}
				args = append(args, "--as", in.As)
			}
			for flag, on := range map[string]bool{"--force": in.Force, "--no-start": in.NoStart, "--no-rehydrate": in.NoRehydrate} {
				if on {
					args = append(args, flag)
				}
			}
			err := c.app.Restore(ctx, args)
			return Done{Output: c.output()}, err
		})
	add(s, "org_migrate", "migrate", "", "Move an org to a registered host: it is copied there and checked while it keeps running, then stopped where it is and started there. With no_switch it is only copied.",
		func(ctx context.Context, c *call, in migrateArgs) (Changed, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return Changed{}, err
			}
			if err := hostName(in.Host); err != nil {
				return Changed{}, err
			}
			// A registered host, or this machine: not an address for the server to ssh to.
			if in.Host != "local" && !contains(c.app.HostNames(), in.Host) {
				return Changed{}, &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: "unknown host '" + in.Host + "'", Hint: "see: berth host ls"}
			}
			args := []string{o, in.Host}
			if in.As != "" {
				if !ops.OrgName.MatchString(in.As) {
					return Changed{}, errors.New("as is an org's name: lowercase letters, digits and dashes")
				}
				args = append(args, "--as", in.As)
			}
			if in.NoSwitch {
				args = append(args, "--no-switch")
			} else {
				args = append(args, "--yes") // confirmed above: the org's name again
			}
			err = b.Migrate(ctx, args)
			return Changed{Org: in.Org, Output: c.output()}, err
		})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
