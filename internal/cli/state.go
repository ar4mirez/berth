package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
)

// Every runnable command declares its access with reads() or writes(). The --read-only guard
// refuses writers before they run, and TestEveryCommandDeclaresAccess fails the build if a
// command declares neither, so a new mutating command can't slip past the guard by omission.
const accessKey = "berth.access"

const (
	accessRead  = "read"
	accessWrite = "write"
)

func reads(c *cobra.Command) *cobra.Command  { return withAccess(c, accessRead) }
func writes(c *cobra.Command) *cobra.Command { return withAccess(c, accessWrite) }

func withAccess(c *cobra.Command, a string) *cobra.Command {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[accessKey] = a
	return c
}

type stateKey struct{}

type outputKey struct{}

// progress holds a long operation's events for one invocation (--output json on up, backup, logs,
// …): the pre-run hook names the operation, appFor starts it, and execute ends it.
type progress struct {
	op, org string
	p       *ops.Progress
}

type progressKey struct{}

func progressFrom(ctx context.Context) *progress {
	h, _ := ctx.Value(progressKey{}).(*progress)
	return h
}

var eventOrgName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(@[a-z0-9][a-z0-9-]*)?$`)

// eventOrg is the org an operation's events are about: the org argument of the commands that take
// one first ("" for the rest, and when it is left out for the default org).
func eventOrg(cmd string, args []string) string {
	switch cmd {
	case "up", "restart", "logs", "remote":
		if len(args) > 0 && eventOrgName.MatchString(args[0]) {
			return args[0]
		}
	}
	return ""
}

// outputFrom is the --output the root's pre-run hook accepted for this command.
func outputFrom(ctx context.Context) string {
	if o, ok := ctx.Value(outputKey{}).(string); ok {
		return o
	}
	return app.OutputText
}

// subOf is the subcommand ops.Lookup needs, from the arguments as each command takes them.
func subOf(cmd string, args []string) string {
	at := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch cmd {
	case "fw", "env", "password", "remote", "pkg": // <cmd> <org> <sub>
		return at(1)
	case "repo": // repo <sub> <org> [mode]
		if at(0) == "policy" {
			if at(2) != "" {
				return "policy/<mode>"
			}
			return "policy/"
		}
		return at(0)
	case "service": // service <install|uninstall|status|logs>
		return at(0)
	case "use": // use [<org>[@host] | --clear]
		switch at(0) {
		case "", "--clear":
			return at(0)
		}
		return "<org>"
	case "schedule": // the last action word wins, as in ccenv
		sub := ""
		for _, a := range args {
			switch a {
			case "status", "run", "now", "off", "--off":
				sub = a
			}
		}
		return sub
	}
	return ""
}

// stateFrom returns the State that the root's pre-run hook resolved for this command.
func stateFrom(ctx context.Context) config.State {
	s, _ := ctx.Value(stateKey{}).(config.State)
	return s
}

// addGlobalFlags registers --home and --read-only and the pre-run hook that applies them.
// Subcommands must not define their own PersistentPreRun(E): cobra runs only the nearest one.
func addGlobalFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	home := pf.String("home", "", "state root (default: $BERTH_HOME, then home: in config.yaml, then ~/.local/share/berth)")
	readOnly := pf.Bool("read-only", false, "refuse any command that would change state")
	output := pf.String("output", app.OutputText, "output format for commands that return data: text or json (docs/json.md)")
	pf.Bool("via-daemon", false, "run the command through a running `berth serve`, over its socket (docs/api.md)")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// help, --version, an unknown command, and cobra's own __complete: nothing to resolve or guard.
		if cmd == root || (cmd.Parent() == root && (cmd.Name() == "help" || strings.HasPrefix(cmd.Name(), "__complete"))) {
			return nil
		}
		// Help for a command that parses its own arguments: nothing to resolve or guard either.
		if helpAsked(cmd, args) || cmd.Annotations[goneKey] != "" {
			return nil
		}
		name, sub, fargs := opOf(cmd, args)
		if via, _ := pf.GetBool("via-daemon"); via && !daemonCan(name, sub) {
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("%q can't go through the daemon: it needs a terminal, or the API has no endpoint for it",
				strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")), Hint: "run it without --via-daemon"}
		}
		access := cmd.Annotations[accessKey]
		switch access {
		case accessRead, accessWrite:
		default:
			return fmt.Errorf("internal error: %q declares no access (use reads() or writes())", cmd.CommandPath())
		}
		switch *output {
		case app.OutputText:
		case app.OutputJSON:
			op, ok := ops.Lookup(name, sub)
			switch {
			case ok && op.JSON:
			case ok && op.Events:
				// A long operation: its progress, one event per line (appFor starts it).
				if h := progressFrom(cmd.Context()); h != nil {
					// The operation as the catalog names it: "up", "remote logs".
					h.op, h.org = strings.TrimSpace(name+" "+sub), eventOrg(name, fargs)
				}
			default:
				why := "it changes things, and returns no data"
				if op.NoJSON != "" {
					why = "it is " + op.NoJSON
				}
				return &ops.Error{Kind: ops.KindUsage, Code: 1,
					Msg: fmt.Sprintf("--output json isn't available for %q: %s", strings.TrimPrefix(cmd.CommandPath(), root.Name()+" "), why)}
			}
		default:
			bad := *output
			*output = app.OutputText // so the error itself prints as text
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("--output must be text or json, not %q", bad)}
		}
		cmd.SetContext(context.WithValue(cmd.Context(), outputKey{}, *output))
		st := config.State{ReadOnly: *readOnly}
		if access == accessWrite {
			if err := st.Writable("run " + strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")); err != nil {
				return err
			}
		}
		in, err := config.FromOS(*home)
		if err != nil {
			return err
		}
		if st.Home, err = config.Resolve(in); err != nil {
			return err
		}
		if st.Engine, err = config.ResolveEngine(in); err != nil {
			return err
		}
		if err := host.CheckEngine(st.Engine); err != nil {
			return err
		}
		if st.Bind, err = config.ResolveBind(in); err != nil {
			return err
		}
		if err := app.CheckBind(st.Bind); err != nil {
			return err
		}
		cmd.SetContext(context.WithValue(cmd.Context(), stateKey{}, st))
		return nil
	}
}
