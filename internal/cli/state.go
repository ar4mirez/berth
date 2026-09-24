package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/config"
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

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd == root { // help / version / unknown command: nothing to resolve
			return nil
		}
		access := cmd.Annotations[accessKey]
		switch access {
		case accessRead, accessWrite:
		default:
			return fmt.Errorf("internal error: %q declares no access (use reads() or writes())", cmd.CommandPath())
		}
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
