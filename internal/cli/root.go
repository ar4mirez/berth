// Package cli wires berth's cobra command tree.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/version"
)

// ExitError ends the process with Code and no message. Use it when the output has already
// been printed (e.g. usage on an unknown command) or a passthrough command set the code.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode lets Execute end with Code and no message.
func (e *ExitError) ExitCode() int { return e.Code }

// NewRoot builds the command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "berth",
		Short:         "berth: one Claude Code container per organization",
		Version:       version.String(),
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		// Parity with ccenv: no arguments prints help (exit 0); an unknown command prints it and exits 1.
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			// cobra only adds its own `help` command once subcommands exist.
			if len(args) > 0 && args[0] != "help" {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	// ccenv has its own `completion` (bash); it is ported with the read-only commands.
	root.CompletionOptions.DisableDefaultCmd = true
	// Defined here so cobra skips its default -v shorthand (ccenv has no -v; berth may want it for verbose).
	root.Flags().Bool("version", false, "print the berth version")
	root.SetVersionTemplate("berth {{.Version}}\n")
	addGlobalFlags(root)
	addCommands(root)
	return root
}

// Execute runs berth with args and returns the process exit code.
// Errors print as "berth: <msg>" on stderr and exit 1.
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return execute(NewRoot(), args, stdin, stdout, stderr)
}

func execute(root *cobra.Command, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}
	// An exit code without a message: *ExitError here, *app.Exit, or a passthrough command's own
	// exit (*host.ExitError).
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	_, _ = fmt.Fprintf(stderr, "berth: %v\n", err)
	return 1
}
