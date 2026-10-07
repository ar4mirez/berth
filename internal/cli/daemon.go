package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/api"
	"github.com/ar4mirez/berth/internal/apiclient"
	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/mcpsrv"
	"github.com/ar4mirez/berth/internal/ops"
)

// `berth --via-daemon <command>` (#62) sends the command to a running `berth serve` over its
// socket, which runs it there (POST /v1/cli) and sends back what it prints and its exit code. The
// commands that can go are the operations the API has endpoints for: the same surface, as text.

var daemonOps = sync.OnceValue(func() map[string]bool {
	out := map[string]bool{}
	for _, s := range api.Specs() {
		if s.Cmd != "" {
			out[s.Cmd+" "+s.Sub] = true
			out[s.Cmd] = true
		}
	}
	return out
})

// daemonCan reports whether the operation (its name and subcommand in ops.Catalog) is one the API
// serves. A command's default subcommand (fw acme, for fw acme show) goes when it only reads.
func daemonCan(name, sub string) bool {
	if daemonOps()[name+" "+sub] {
		return true
	}
	op, ok := ops.Lookup(name, sub)
	return ok && sub == "" && op.Access == ops.Read && op.JSON && daemonOps()[name]
}

// throughDaemon makes every command, under --via-daemon, run on the server instead of here.
func throughDaemon(c *cobra.Command) {
	for _, s := range c.Commands() {
		throughDaemon(s)
		if s.RunE == nil || s.Annotations[goneKey] != "" {
			continue
		}
		run := s.RunE
		s.RunE = func(cmd *cobra.Command, args []string) error {
			if via, _ := cmd.Root().PersistentFlags().GetBool("via-daemon"); !via || helpAsked(cmd, args) {
				return run(cmd, args)
			}
			name, _, fargs := opOf(cmd, args)
			client := apiclient.Unix(api.SocketPath(os.Getenv))
			code, err := client.CLI(cmd.Context(), append([]string{name}, fargs...), outputFrom(cmd.Context()), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		}
	}
}

// runFor is the API's RunCLI: a command in ccenv's flat form, run in this process for a caller,
// on the server's state root. The caller's scope is checked against the operation as the catalog
// has it, and a write is in the audit log.
func runFor(st config.State) func(ctx context.Context, by mcpsrv.Caller, args []string, output string, stdout, stderr io.Writer) int {
	return func(ctx context.Context, by mcpsrv.Caller, args []string, output string, stdout, stderr io.Writer) int {
		refuse := func(msg string) int {
			_, _ = io.WriteString(stderr, "berth: "+ops.Respell(msg)+"\n")
			return 1
		}
		root := newTree(true)
		c, rest, err := root.Find(args)
		if err != nil || c == root || !c.Runnable() || strings.HasPrefix(args[0], "-") {
			return refuse("no command " + strings.Join(args, " "))
		}
		name, sub, _ := opOf(c, rest)
		op, ok := ops.Lookup(name, sub)
		if !ok || !daemonCan(name, sub) {
			return refuse("the API has no endpoint for " + strings.TrimSpace(name+" "+sub))
		}
		audit := func(outcome string) {
			if op.Access == ops.Write {
				a := appMaker(st)(io.Discard, io.Discard)
				_ = a.Audit(ctx, app.AuditEntry{Via: "api", Tool: "cli " + strings.TrimSpace(name+" "+sub), Outcome: outcome, Args: map[string]any{"args": args[1:]}})
			}
		}
		switch {
		case op.Restart != ops.Never && !by.Restarts:
			audit("refused")
			return refuse(name + " restarts or stops a container, and this caller may not: refused")
		case op.Access == ops.Write && !by.Writes:
			audit("refused")
			return refuse(strings.TrimSpace(name+" "+sub) + " changes things, and this caller may only read: refused")
		}
		full := []string{"--home", st.Home.Path}
		if st.ReadOnly {
			full = append(full, "--read-only")
		}
		if output == app.OutputJSON {
			full = append(full, "--output", "json")
		}
		code := executeContext(ctx, root, append(full, args...), strings.NewReader(""), stdout, stderr)
		if code == 0 {
			audit("ok")
		} else {
			audit("failed")
		}
		return code
	}
}
