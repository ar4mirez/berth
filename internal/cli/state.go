package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/config"
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
	case "fw", "env", "password", "remote": // <cmd> <org> <sub>
		return at(1)
	case "repo": // repo <sub> <org> [mode]
		if at(0) == "policy" {
			if at(2) != "" {
				return "policy/<mode>"
			}
			return "policy/"
		}
		return at(0)
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

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// help, --version, an unknown command, and cobra's own __complete: nothing to resolve or guard.
		if cmd == root || (cmd.Parent() == root && (cmd.Name() == "help" || strings.HasPrefix(cmd.Name(), "__complete"))) {
			return nil
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
			if op, ok := ops.Lookup(cmd.Name(), subOf(cmd.Name(), args)); !ok || !op.JSON {
				return fmt.Errorf("--output json isn't available for %q yet (#54)", strings.TrimPrefix(cmd.CommandPath(), root.Name()+" "))
			}
		default:
			return fmt.Errorf("--output must be text or json, not %q", *output)
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
		cmd.SetContext(context.WithValue(cmd.Context(), stateKey{}, st))
		return nil
	}
}
