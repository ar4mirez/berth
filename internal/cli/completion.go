package cli

import (
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
)

// completionOrgs lists orgs from the state root, the way the command would resolve it (--home,
// $BERTH_HOME, config.yaml, default). cobra's __complete doesn't run the root's pre-run hook.
func completionOrgs(cmd *cobra.Command) []string {
	home, _ := cmd.Flags().GetString("home")
	in, err := config.FromOS(home)
	if err != nil {
		return nil
	}
	h, err := config.Resolve(in)
	if err != nil {
		return nil
	}
	st := config.State{Home: h, ReadOnly: true} // completion never writes
	return app.New(st, local.New(), nil, nil, nil, os.Getenv).OrgNames()
}

// completeArgs builds a ValidArgsFunction from one candidate list per position; "org" means the
// org names, and a nil list means nothing to suggest.
func completeArgs(positions ...[]string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) >= len(positions) || positions[len(args)] == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		words := positions[len(args)]
		if len(words) == 1 && words[0] == "org" {
			words = completionOrgs(cmd)
		}
		return words, cobra.ShellCompDirectiveNoFileComp
	}
}

var orgArg = []string{"org"}

// completeOrgs suggests every org not already on the line (whoami [org...]).
func completeOrgs(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var out []string
	for _, o := range completionOrgs(cmd) {
		if !slices.Contains(args, o) {
			out = append(out, o)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completionCmd is `berth completion [bash|zsh|fish|powershell]` (ccenv's is bash only, and found
// orgs through `readlink $(command -v ccenv)/../orgs`; berth completes from the state root).
func completionCmd(root *cobra.Command) *cobra.Command {
	return reads(&cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "print a shell completion script (default bash)",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch arg(args, 0) {
			case "", "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			}
			return fmt.Errorf("unsupported shell %q (bash, zsh, fish or powershell)", args[0])
		},
	})
}
