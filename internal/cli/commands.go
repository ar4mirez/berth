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
