// Package mcpsrv is `berth mcp`: a Model Context Protocol server, so an agent manages orgs through
// typed tools instead of the command line (#61).
//
// Every tool is an operation from the catalog (internal/ops), and the catalog decides what it may
// do: a tool that reads always runs; one that writes needs the server started with --allow-writes;
// one that restarts a container needs --allow-restarts and the org's name again as a confirmation.
// Secret values are never returned, and every write asked for is in the audit log.
package mcpsrv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/ops"
)

// Options is how the server was started.
type Options struct {
	// AllowWrites lets tools change things; AllowRestarts lets them recreate or stop containers
	// too (and implies AllowWrites).
	AllowWrites, AllowRestarts bool
	// Version is berth's.
	Version string
	// NewApp makes the App one call runs against, with its output going to stdout and stderr:
	// never to the process's own, which carry the protocol.
	NewApp func(stdout, stderr io.Writer) *app.App
}

// Spec is one tool as the catalog sees it. Specs lists them all, for the docs and the tests.
type Spec struct {
	Name string
	// Cmd and Sub are the operation in ops.Catalog that decides what the tool may do.
	Cmd, Sub string
	Op       ops.Op
}

type server struct {
	opts  Options
	mcp   *mcp.Server
	specs []Spec
}

// New builds the server. It panics for a tool whose operation isn't in the catalog: a tool can't
// exist without declaring whether it writes or restarts.
func New(opts Options) (*mcp.Server, []Spec) {
	if opts.AllowRestarts {
		opts.AllowWrites = true
	}
	s := &server{opts: opts}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "berth", Title: "berth", Version: opts.Version}, &mcp.ServerOptions{
		Instructions: instructions(opts),
	})
	s.tools()
	s.resources()
	return s.mcp, s.specs
}

func instructions(o Options) string {
	mode := "This server is read-only: tools that change anything are refused. The operator can start it with --allow-writes."
	switch {
	case o.AllowRestarts:
		mode = "This server may change orgs, and restart, start or stop their containers. A restart stops the work running in that org: " +
			"ask the user before calling org_up, org_restart or org_down, and pass the org's name as `confirm`."
	case o.AllowWrites:
		mode = "This server may change orgs (firewall, repos, backups) but not restart, start or stop their containers: " +
			"the operator would have to start it with --allow-restarts."
	}
	return "berth runs one isolated Claude Code container per organization (\"org\"). An org is named like `acme`, or `acme@box1` " +
		"on a registered host. " + mode + " Secret values are never returned: variables are listed by name only. " +
		"The berth://docs/… resources are berth's documentation."
}

// call is one tool call: its App, and what that App printed.
type call struct {
	s      *server
	app    *app.App
	stdout bytes.Buffer
	stderr bytes.Buffer
	mu     sync.Mutex
	done   []func()
}

// at resolves an org argument (acme, or acme@box1) to the App for its host and the bare name.
func (c *call) at(ctx context.Context, arg string) (*app.App, string, error) {
	if strings.TrimSpace(arg) == "" {
		return nil, "", fmt.Errorf("org is required (as in acme, or acme@box1)")
	}
	b, o, done, err := c.app.At(ctx, arg)
	if err != nil {
		return nil, "", err
	}
	c.mu.Lock()
	c.done = append(c.done, done)
	c.mu.Unlock()
	return b, o, nil
}

// output is what the operation printed, as the command line would have shown it.
func (c *call) output() string {
	out := strings.TrimRight(c.stdout.String(), "\n")
	if e := strings.TrimRight(c.stderr.String(), "\n"); e != "" {
		if out != "" {
			out += "\n"
		}
		out += e
	}
	return out
}

// confirmed is implemented by the input of a tool that restarts a container.
type confirmed interface{ confirmation() (org, confirm string) }

// audited is implemented by the input of a tool that writes: its arguments, for the audit log.
type audited interface{ auditArgs() map[string]any }

func boolp(b bool) *bool { return &b }

// add registers a tool for the catalog operation cmd/sub.
func add[In, Out any](s *server, name, cmd, sub, description string, h func(ctx context.Context, c *call, in In) (Out, error)) {
	op, ok := ops.Lookup(cmd, sub)
	if !ok || op.Access == ops.BySub {
		panic("mcpsrv: tool " + name + " has no operation " + cmd + " " + sub + " in ops.Catalog")
	}
	register(s, Spec{Name: name, Cmd: cmd, Sub: sub, Op: op}, description, h)
}

// addQuery registers a reading tool that has no command of its own in the catalog: it only reads.
func addQuery[In, Out any](s *server, name, description string, h func(ctx context.Context, c *call, in In) (Out, error)) {
	register(s, Spec{Name: name, Op: ops.Op{Access: ops.Read, JSON: true}}, description, h)
}

func register[In, Out any](s *server, spec Spec, description string, h func(ctx context.Context, c *call, in In) (Out, error)) {
	name, op := spec.Name, spec.Op
	s.specs = append(s.specs, spec)
	tool := &mcp.Tool{Name: name, Description: ops.Respell(description), Annotations: &mcp.ToolAnnotations{
		ReadOnlyHint:    op.Access == ops.Read,
		DestructiveHint: boolp(op.Restart != ops.Never || destructive[name]),
		IdempotentHint:  op.Access == ops.Read,
		OpenWorldHint:   boolp(false),
	}}
	switch {
	case op.Restart != ops.Never:
		tool.Description += " Restarts or stops the org's container, which stops the work running in it: needs the server started with --allow-restarts, and `confirm` set to the org's name."
	case op.Access == ops.Write:
		tool.Description += " Changes the org: needs the server started with --allow-writes."
	}
	mcp.AddTool(s.mcp, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		c := &call{s: s}
		c.app = s.opts.NewApp(&c.stdout, &c.stderr)
		defer func() {
			for _, d := range c.done {
				d()
			}
		}()
		record := func(outcome string, err error) {
			if op.Access != ops.Write {
				return
			}
			e := app.AuditEntry{Via: "mcp", Tool: name, Outcome: outcome, Args: map[string]any{}}
			if a, ok := any(in).(audited); ok {
				e.Args = a.auditArgs()
			}
			if err != nil {
				e.Error = err.Error()
			}
			_ = c.app.Audit(ctx, e)
		}
		if err := s.allowed(name, op, in); err != nil {
			record("refused", err)
			return nil, zero, err
		}
		out, err := h(ctx, c, in)
		if err != nil {
			// The message as the command line prints it, with its hint, and what was printed before it.
			e := ops.AsError(err)
			msg := e.Error()
			if e.Quiet && c.output() != "" {
				msg = c.output()
			} else if o := c.output(); o != "" {
				msg = o + "\n" + msg
			}
			err = fmt.Errorf("%s", ops.Respell(msg))
			record("failed", err)
			return nil, zero, err
		}
		record("ok", nil)
		return nil, out, nil
	})
}

// destructive are the writing tools that remove something, beyond the ones that restart.
var destructive = map[string]bool{"repo_remove": true, "firewall_deny": true}

// allowed is the gate: what the catalog says the operation does, against how the server was started.
func (s *server) allowed(name string, op ops.Op, in any) error {
	if op.Access == ops.Read {
		return nil
	}
	if op.Restart != ops.Never {
		if !s.opts.AllowRestarts {
			return fmt.Errorf("%s restarts or stops a container (%s), and this server wasn't started with --allow-restarts: refused. "+
				"Tell the user; they can run it themselves, or restart the server with: berth mcp --allow-restarts", name, op.Note)
		}
		c, ok := in.(confirmed)
		if !ok {
			return fmt.Errorf("%s: no confirmation argument", name)
		}
		if org, confirm := c.confirmation(); confirm == "" || confirm != org {
			return fmt.Errorf("%s stops the work running in %s (%s). To go ahead, ask the user, then call it again with confirm set to %q: refused",
				name, org, op.Note, org)
		}
		return nil
	}
	if !s.opts.AllowWrites {
		return fmt.Errorf("%s changes things, and this server is read-only: refused. "+
			"Tell the user; they can run it themselves, or restart the server with: berth mcp --allow-writes", name)
	}
	return nil
}
