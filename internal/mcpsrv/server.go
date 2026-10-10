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
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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
	// Via names the transport in the audit log and decides how a refusal is worded: "mcp" (the
	// default) is `berth mcp` on stdio, whose flags the operator can change; "mcp-http" is the
	// API's /mcp, where a token's scope decides.
	Via string
	// NewApp makes the App one call runs against, with its output going to stdout and stderr:
	// never to the process's own, which carry the protocol.
	NewApp func(stdout, stderr io.Writer) *app.App
}

// Caller is who asks for a tool, and what they may do. The MCP server's is how it was started; the
// API's (internal/api) is the socket's owner, or a token's scope.
type Caller struct {
	// Via names the interface in the audit log: "mcp", "api".
	Via string
	// Writes lets tools change things; Restarts lets them recreate or stop containers too.
	Writes, Restarts bool
	// Admin lets them do what can't be undone, and what handles a secret (ops.Op.Admin): the
	// socket's owner, or a token with the admin scope. No MCP server has it.
	Admin bool
	// Events, when set, gets the operation's progress as it runs (ops.Event).
	Events func(ops.Event)
}

// Spec is one tool as the catalog sees it. Specs lists them all, for the docs, the tests, and the
// API, which serves the same tools over HTTP.
type Spec struct {
	Name string
	// Cmd and Sub are the operation in ops.Catalog that decides what the tool may do.
	Cmd, Sub string
	Op       ops.Op
	// NoMCP is true for a tool the API serves and no MCP server offers (#167): what an agent
	// shouldn't be handed, whatever the server was started with. Every admin operation is one.
	NoMCP bool
	// Description is the tool's, with what it needs to run.
	Description string
	// In and Out are its arguments and its result.
	In, Out reflect.Type
	// Invoke runs it for a caller, with its arguments as JSON: the same gate, audit and errors as
	// through MCP. An argument the tool doesn't have is refused.
	Invoke func(ctx context.Context, by Caller, args json.RawMessage) (any, error)
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
	s.moreTools()
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
	tool := &mcp.Tool{Name: name, Description: ops.Respell(description), Annotations: &mcp.ToolAnnotations{
		ReadOnlyHint:    op.Access == ops.Read,
		DestructiveHint: boolp(op.Restart != ops.Never || destructive[name]),
		IdempotentHint:  op.Access == ops.Read,
		OpenWorldHint:   boolp(false),
	}}
	switch {
	case op.Admin:
		tool.Description += " It can't be undone, or it handles a secret: needs a caller with the admin scope, and no MCP server offers it."
	case op.Restart != ops.Never:
		tool.Description += " Restarts or stops the org's container, which stops the work running in it: needs the server started with --allow-restarts, and `confirm` set to the org's name."
	case op.Access == ops.Write:
		tool.Description += " Changes the org: needs the server started with --allow-writes."
	}
	core := func(ctx context.Context, by Caller, in In) (res Out, rerr error) {
		var zero Out
		c := &call{s: s}
		c.app = s.opts.NewApp(&c.stdout, &c.stderr)
		if by.Events != nil {
			// What the operation prints becomes events too, as it does for --output json.
			p := ops.NewProgress(by.Events, strings.TrimSpace(spec.Cmd+" "+spec.Sub), "")
			c.app.Progress = p
			c.app.Stdout, c.app.Stderr = io.MultiWriter(&c.stdout, p.Writer("stdout")), io.MultiWriter(&c.stderr, p.Writer("stderr"))
			defer func() { p.Done(rerr) }() // the last event: done, or failed with the error
		}
		defer func() {
			for _, d := range c.done {
				d()
			}
		}()
		record := func(outcome string, err error) {
			if op.Access != ops.Write {
				return
			}
			e := app.AuditEntry{Via: by.Via, Tool: name, Outcome: outcome, Args: map[string]any{}}
			if a, ok := any(in).(audited); ok {
				e.Args = a.auditArgs()
			}
			if err != nil {
				e.Error = err.Error()
			}
			_ = c.app.Audit(ctx, e)
		}
		if err := allowed(name, op, in, by); err != nil {
			record("refused", err)
			return zero, err
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
			err = &ops.Error{Kind: e.Kind, Code: e.Code, Msg: ops.Respell(msg), Quiet: e.Quiet}
			record("failed", err)
			return zero, err
		}
		record("ok", nil)
		return out, nil
	}
	spec.NoMCP = noMCP[name] || op.Admin
	spec.Description, spec.In, spec.Out = tool.Description, reflect.TypeFor[In](), reflect.TypeFor[Out]()
	spec.Invoke = func(ctx context.Context, by Caller, args json.RawMessage) (any, error) {
		var in In
		if len(bytes.TrimSpace(args)) > 0 {
			dec := json.NewDecoder(bytes.NewReader(args))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&in); err != nil {
				return nil, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "the arguments don't fit " + name + ": " + err.Error()}
			}
		}
		return core(ctx, by, in)
	}
	s.specs = append(s.specs, spec)
	if spec.NoMCP {
		return
	}
	mcp.AddTool(s.mcp, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		via := s.opts.Via
		if via == "" {
			via = "mcp"
		}
		out, err := core(ctx, Caller{Via: via, Writes: s.opts.AllowWrites, Restarts: s.opts.AllowRestarts}, in)
		if err != nil {
			err = fmt.Errorf("%s", err.Error()) // the text is the whole of a tool error
		}
		return nil, out, err
	})
}

// noMCP are the tools the API serves and no MCP server offers, beyond the admin ones (#167).
var noMCP = map[string]bool{}

// destructive are the writing tools that remove something, beyond the ones that restart.
var destructive = map[string]bool{"repo_remove": true, "firewall_deny": true}

// allowed is the gate: what the catalog says the operation does, against how the server was started.
func allowed(name string, op ops.Op, in any, by Caller) error {
	if op.Access == ops.Read {
		return nil
	}
	if op.Admin && !by.Admin {
		return refused("%s can't be undone, or handles a secret, and this caller may not: it needs the admin scope: refused", name)
	}
	if op.Restart != ops.Never {
		if !by.Restarts {
			if by.Via != "mcp" {
				return refused("%s restarts or stops a container (%s), and this caller may not: refused", name, op.Note)
			}
			return fmt.Errorf("%s restarts or stops a container (%s), and this server wasn't started with --allow-restarts: refused. "+
				"Tell the user; they can run it themselves, or restart the server with: berth mcp --allow-restarts", name, op.Note)
		}
		c, ok := in.(confirmed)
		if !ok {
			return fmt.Errorf("%s: no confirmation argument", name)
		}
		if org, confirm := c.confirmation(); confirm == "" || confirm != org {
			return refused("%s stops the work running in %s (%s). To go ahead, ask the user, then call it again with confirm set to %q: refused",
				name, org, op.Note, org)
		}
		return nil
	}
	if !by.Writes {
		if by.Via != "mcp" {
			return refused("%s changes things, and this caller may only read: refused", name)
		}
		return fmt.Errorf("%s changes things, and this server is read-only: refused. "+
			"Tell the user; they can run it themselves, or restart the server with: berth mcp --allow-writes", name)
	}
	return nil
}

// refused is a caller asking for more than it may do.
func refused(format string, args ...any) error {
	return &ops.Error{Kind: ops.KindRefused, Code: 1, Msg: fmt.Sprintf(format, args...)}
}
