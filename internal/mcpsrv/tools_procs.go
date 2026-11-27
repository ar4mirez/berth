package mcpsrv

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/ar4mirez/berth/internal/ops"
)

// Supervised processes (#216): a named long-lived process in a running org's container, as its
// user, supervised there by image/berth-proc. Its variables are secrets by berth's rules: they
// are passed on berth-proc's stdin, never in an event, the audit log or a document, which lists
// them by name.

type procStartArgs struct {
	Org  string   `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name string   `json:"name" jsonschema:"the process's name: lowercase letters, digits and dashes, up to 32"`
	Argv []string `json:"argv" jsonschema:"the command and its arguments, as a list (never a shell string): argv[0] an absolute path in the container, or a name on its user's PATH"`
	Cwd  string   `json:"cwd,omitempty" jsonschema:"where it starts: an absolute path in the container (/workspace by default)"`
	// Env is secret: its values are never shown back, logged or in an event.
	Env     map[string]string `json:"env,omitempty" secret:"true" jsonschema:"its variables, {NAME: value}, names like API_URL, on top of the org's own; listed back by name only"`
	Restart string            `json:"restart,omitempty" jsonschema:"never, or on-failure (the default): started again after a failure, waiting 1s, then twice as long each time, up to 60s"`
	Grace   int               `json:"stop_grace_seconds,omitempty" jsonschema:"1 to 300, 30 by default: how long a stop waits after SIGTERM before it kills the process's whole group"`
	// Placement is a placement token for the process (#216), kept fresh while it runs.
	Placement bool `json:"placement,omitempty" jsonschema:"true for a placement token: a proof signed by berth of the org's container it runs in, at the path BERTH_PLACEMENT_FILE names, kept fresh while it runs; refused when no berth serve (not --read-only) would keep it fresh"`
}

func (a procStartArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name, "argv": a.Argv, "env_names": ops.EnvNames(slices.Collect(maps.Keys(a.Env))...), "cwd": a.Cwd, "restart": a.Restart, "stop_grace_seconds": a.Grace, "placement": a.Placement}
}

type procNameArgs struct {
	Org  string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name string `json:"name" jsonschema:"the process's name, as processes_list shows it"`
}

func (a procNameArgs) auditArgs() map[string]any { return map[string]any{"org": a.Org, "name": a.Name} }

func init() {
	// Starting or stopping a program in an org is for the API's caller, as a session in it is: no
	// MCP server offers it.
	noMCP["process_start"], noMCP["process_stop"] = true, true
}

func (s *server) procTools() {
	add(s, "placement_keys", "placement", "keys", "The public keys (Ed25519) that verify the placement tokens berth gives supervised processes: the one signing, and older ones until their last token has expired.",
		func(ctx context.Context, c *call, _ noArgs) (ops.PlacementKeys, error) {
			return c.app.PlacementKeys(ctx)
		})
	add(s, "processes_list", "proc", "ls", "An org's supervised processes: each one's state (starting, running, exited, backoff), pid, start time, exit code and restarts. A stopped org has none.",
		func(ctx context.Context, c *call, in orgArg) (ops.Processes, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Processes{}, err
			}
			out, err := ops.GetProcesses(ctx, b, o)
			out.Org = in.Org
			return out, err
		})
	add(s, "process_show", "proc", "show", "One of an org's supervised processes: its state, pid, start time, exit code and restarts.",
		func(ctx context.Context, c *call, in procNameArgs) (ops.Process, error) {
			return c.procVerb(ctx, "show", in)
		})
	add(s, "process_start", "proc", "start", "Start a named long-lived process in a running org's container, as its user (never root), inside its firewall. A supervisor there keeps it running (restart on-failure, or never); it ends with the container. Its variables are listed by name only.",
		func(ctx context.Context, c *call, in procStartArgs) (ops.Process, error) {
			c.private()
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Process{}, err
			}
			if in.Name == "" {
				return ops.Process{}, errors.New("name is required")
			}
			p, err := b.ProcStart(ctx, o, in.Name, ops.ProcSpec{Argv: in.Argv, Cwd: in.Cwd, Env: in.Env, Restart: in.Restart, Grace: in.Grace, Placement: in.Placement})
			if err != nil {
				c.forget()
				for _, v := range in.Env {
					err = scrub(err, v)
				}
				return ops.Process{}, err
			}
			p.Org = in.Org
			return p, nil
		})
	add(s, "process_stop", "proc", "stop", "Stop one of an org's supervised processes: SIGTERM, its grace, then SIGKILL of its whole process group; then it is forgotten. Answers when it has exited.",
		func(ctx context.Context, c *call, in procNameArgs) (ops.Process, error) {
			return c.procVerb(ctx, "stop", in)
		})
}

// procVerb is show or stop, as the command line runs it, with the document it prints.
func (c *call) procVerb(ctx context.Context, verb string, in procNameArgs) (ops.Process, error) {
	b, o, err := c.at(ctx, in.Org)
	if err != nil {
		return ops.Process{}, err
	}
	if in.Name == "" {
		return ops.Process{}, errors.New("name is required")
	}
	if err := flagLike("name", in.Name); err != nil {
		return ops.Process{}, err
	}
	p, err := b.ProcAct(ctx, o, verb, in.Name)
	p.Org = in.Org
	return p, err
}
