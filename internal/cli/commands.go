package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/host/local"
)

// appFor builds the App a command runs against: the resolved state, on the local host.
func appFor(cmd *cobra.Command) *app.App {
	return app.New(stateFrom(cmd.Context()), local.New(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Getenv)
}

// arg is ccenv's "${n:-}": the nth positional argument, or "". Like ccenv, extra arguments are
// ignored (the commands take cobra.ArbitraryArgs).
func arg(args []string, n int) string {
	if n < len(args) {
		return args[n]
	}
	return ""
}

func addCommands(root *cobra.Command) {
	repo := reads(&cobra.Command{
		Use: "repo <ls|audit> <org>", Short: "registered repos and anything unregistered in /workspace", Args: cobra.ArbitraryArgs,
		ValidArgsFunction: completeArgs([]string{"ls", "audit"}, orgArg),
		RunE: func(cmd *cobra.Command, args []string) error {
			var rest []string
			if quiet, _ := cmd.Flags().GetBool("quiet"); quiet {
				rest = []string{"--quiet"}
			}
			return appFor(cmd).Repo(cmd.Context(), arg(args, 0), arg(args, 1), rest)
		},
	})
	repo.Flags().Bool("quiet", false, "repo audit: say nothing when the workspace is clean")
	org1 := completeArgs(orgArg)
	one := func(use, short string, run func(*app.App, *cobra.Command, string) error) *cobra.Command {
		return writes(&cobra.Command{
			Use: use, Short: short, Args: cobra.ArbitraryArgs, ValidArgsFunction: org1,
			RunE: func(cmd *cobra.Command, args []string) error { return run(appFor(cmd), cmd, arg(args, 0)) },
		})
	}
	// claude and run pass their arguments straight to claude, flags included, so cobra doesn't parse
	// them. Global flags go before the command (berth --read-only claude acme …); the root's
	// TraverseChildren makes sure those are parsed rather than handed to claude.
	passthrough := func(use, short string, run func(*app.App, *cobra.Command, string, []string) error) *cobra.Command {
		return writes(&cobra.Command{
			Use: use, Short: short, DisableFlagParsing: true, ValidArgsFunction: org1,
			RunE: func(cmd *cobra.Command, args []string) error {
				var rest []string
				if len(args) > 1 {
					rest = args[1:]
				}
				return run(appFor(cmd), cmd, arg(args, 0), rest)
			},
		})
	}
	root.AddCommand(
		one("down <org>", "stop the container", func(a *app.App, c *cobra.Command, o string) error { return a.Down(c.Context(), o) }),
		one("restart <org>", "recreate the container", func(a *app.App, c *cobra.Command, o string) error { return a.Restart(c.Context(), o) }),
		one("attach <org>", "attach to the shared tmux session", func(a *app.App, c *cobra.Command, o string) error { return a.Attach(c.Context(), o) }),
		one("shell <org>", "bash inside the container", func(a *app.App, c *cobra.Command, o string) error { return a.Shell(c.Context(), o) }),
		passthrough("claude <org> [args...]", "interactive claude in /workspace", func(a *app.App, c *cobra.Command, o string, rest []string) error {
			return a.Claude(c.Context(), o, rest)
		}),
		passthrough("run <org> \"<prompt>\" [args...]", "headless claude -p", func(a *app.App, c *cobra.Command, o string, rest []string) error {
			return a.Run(c.Context(), o, rest)
		}),
	)
	root.AddCommand(
		repo,
		completionCmd(root),
		reads(&cobra.Command{
			Use: "ls", Short: "all orgs and their state", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(),
			RunE:              func(cmd *cobra.Command, _ []string) error { return appFor(cmd).Ls(cmd.Context()) },
		}),
		reads(&cobra.Command{
			Use: "whoami [org...]", Short: "which Claude account each org is signed in with", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeOrgs,
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Whoami(cmd.Context(), args) },
		}),
		reads(&cobra.Command{
			Use: "logs <org>", Short: "follow the container's logs", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg),
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Logs(cmd.Context(), arg(args, 0)) },
		}),
		reads(&cobra.Command{
			Use: "fw <org> [show|presets]", Short: "show the egress allowlist, or the presets", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg, []string{"show", "presets"}),
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return appFor(cmd).Fw(cmd.Context(), "", nil)
				}
				return appFor(cmd).Fw(cmd.Context(), args[0], args[1:])
			},
		}),
		reads(&cobra.Command{
			Use: "info <org>", Short: "every way to connect", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg),
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Info(cmd.Context(), arg(args, 0)) },
		}),
	)
}
