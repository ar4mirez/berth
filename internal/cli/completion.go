package cli

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
)

// completionOrgs lists orgs from the state root, the way the command would resolve it (--home,
// $BERTH_HOME, config.yaml, default). cobra's __complete doesn't run the root's pre-run hook.
func completionOrgs(cmd *cobra.Command) []string {
	a := completionApp(cmd)
	if a == nil {
		return nil
	}
	return a.OrgNames()
}

func completionApp(cmd *cobra.Command) *app.App {
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
	return app.New(st, local.New(), nil, nil, nil, os.Getenv)
}

// completionOrgsAt is completionOrgs for commands that take org@host (#45): once the word has an
// "@", the registered hosts after it (this reads berth's registry, and never connects to a host).
func completionOrgsAt(cmd *cobra.Command, toComplete string) []string {
	o, _, found := strings.Cut(toComplete, "@")
	if !found {
		return completionOrgs(cmd)
	}
	a := completionApp(cmd)
	if a == nil {
		return nil
	}
	out := []string{o + "@local"}
	for _, h := range a.HostNames() {
		out = append(out, o+"@"+h)
	}
	return out
}

// completeArgs builds a ValidArgsFunction from one candidate list per position; "org" means the
// org names, and a nil list means nothing to suggest.
func completeArgs(positions ...[]string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) >= len(positions) || positions[len(args)] == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		words := positions[len(args)]
		if len(words) == 1 && words[0] == "org" {
			words = completionOrgsAt(cmd, toComplete)
		}
		return words, cobra.ShellCompDirectiveNoFileComp
	}
}

var orgArg = []string{"org"}

// repoSubsShown are the repo subcommands completion offers, as ccenv's.
var repoSubsShown = []string{"add", "new", "publish", "ls", "rm", "adopt", "sync", "audit", "policy"}

// completeOrgs suggests every org not already on the line (whoami [org...]).
func completeOrgs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var out []string
	for _, o := range completionOrgsAt(cmd, toComplete) {
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

// bashCompletion is berth's bash completion script, registered for name too when it's an alias
// (a link named ccenv, say).
func bashCompletion(root *cobra.Command, name string) ([]byte, error) {
	var b bytes.Buffer
	if err := root.GenBashCompletionV2(&b, true); err != nil {
		return nil, err
	}
	if name != root.Name() {
		fmt.Fprintf(&b, "\n# %s is an alias of %s.\ncomplete -o default -F __start_%s %s\n", name, root.Name(), root.Name(), name)
	}
	return b.Bytes(), nil
}

// completeBackup: orgs and --all, as ccenv's completion.
func completeBackup(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return append(completionOrgs(cmd), "--all"), cobra.ShellCompDirectiveNoFileComp
}

// completeFw: the org, then the subcommand, then (for allow) the presets, as ccenv's completion.
func completeFw(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch {
	case len(args) == 0:
		return completionOrgsAt(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
	case len(args) == 1:
		return []string{"show", "allow", "deny", "on", "off", "edit", "reload", "presets", "test"}, cobra.ShellCompDirectiveNoFileComp
	case args[1] == "allow" || args[1] == "add":
		return app.FwPresets, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}
