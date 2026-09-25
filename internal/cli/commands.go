package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/host/local"
)

// appFor builds the App a command runs against: the resolved state, on the local host.
func appFor(cmd *cobra.Command) *app.App {
	a := app.New(stateFrom(cmd.Context()), local.New(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Getenv)
	a.OpenTTY = local.OpenTTY
	return a
}

// arg is ccenv's "${n:-}": the nth positional argument, or "". Like ccenv, extra arguments are
// ignored (the commands take cobra.ArbitraryArgs).
func arg(args []string, n int) string {
	if n < len(args) {
		return args[n]
	}
	return ""
}

// rest is the arguments from the nth on (nil if there are none).
func rest(args []string, n int) []string {
	if n < len(args) {
		return args[n:]
	}
	return nil
}

func addCommands(root *cobra.Command) {
	// repo and clone parse their own arguments, as ccenv does: the same messages, and flags only
	// where ccenv takes them (audit's --quiet and rm's --delete right after the org).
	repo := reads(&cobra.Command{
		Use: "repo <add|new|publish|ls|rm|adopt|sync|audit|policy> <org> ...", Short: "the repos allowed in an org's /workspace", DisableFlagParsing: true,
		ValidArgsFunction: completeArgs(repoSubsShown, orgArg, nil),
		RunE: func(cmd *cobra.Command, args []string) error {
			return appFor(cmd).Repo(cmd.Context(), arg(args, 0), arg(args, 1), rest(args, 2))
		},
	})
	clone := writes(&cobra.Command{
		Use: "clone <org> <owner/repo|url> [--dir d] [--branch b] [--no-clone]", Short: "register a repo and clone it (repo add)", DisableFlagParsing: true,
		ValidArgsFunction: completeArgs(orgArg),
		RunE: func(cmd *cobra.Command, args []string) error {
			return appFor(cmd).Repo(cmd.Context(), "add", arg(args, 0), rest(args, 1))
		},
	})
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
		// init and env parse their own arguments, as ccenv does: the same messages, and env's
		// --no-restart only as the 4th argument.
		writes(&cobra.Command{
			Use: "init <org> [--name N --email E]", Short: "scaffold a new org (berth's own: MANAGER=berth)", DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error { return appFor(cmd).Init(cmd.Context(), args) },
		}),
		reads(&cobra.Command{
			Use: "env <org> [ls | set KEY | unset KEY] [--no-restart]", Short: "custom env vars for the container", DisableFlagParsing: true,
			ValidArgsFunction: completeArgs(orgArg, []string{"ls", "set", "unset"}),
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Env(cmd.Context(), args) },
		}),
		reads(&cobra.Command{
			Use: "password <org> [show|rotate]", Short: "browser-terminal password", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg, []string{"show", "rotate"}),
			RunE: func(cmd *cobra.Command, args []string) error {
				return appFor(cmd).Password(cmd.Context(), arg(args, 0), arg(args, 1))
			},
		}),
		reads(&cobra.Command{
			Use: "remote <org> [status|logs|restart]", Short: "the Remote Control service", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg, []string{"status", "logs", "restart"}),
			RunE: func(cmd *cobra.Command, args []string) error {
				return appFor(cmd).Remote(cmd.Context(), arg(args, 0), arg(args, 1))
			},
		}),
		// The sign-in commands parse their arguments as ccenv does (--paste/--no-restart anywhere for
		// token; --all/--force only as the second argument).
		writes(&cobra.Command{
			Use: "token <org> [--paste] [--no-restart]", Short: "the 1-year token (claude setup-token in the container)", DisableFlagParsing: true,
			ValidArgsFunction: org1,
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Token(cmd.Context(), args) },
		}),
		one("auth <org>", "sign an org in: token, then the Remote Control login", func(a *app.App, c *cobra.Command, o string) error { return a.Auth(c.Context(), o) }),
		one("login <org>", "the full login that enables claude.ai/code (Remote Control)", func(a *app.App, c *cobra.Command, o string) error { return a.Login(c.Context(), o) }),
		writes(&cobra.Command{
			Use: "logout <org> [--all]", Short: "remove the Remote Control login (--all: the token too)", DisableFlagParsing: true,
			ValidArgsFunction: org1,
			RunE: func(cmd *cobra.Command, args []string) error {
				return appFor(cmd).Logout(cmd.Context(), arg(args, 0), arg(args, 1))
			},
		}),
		writes(&cobra.Command{
			Use: "gh-login <org> [--force]", Short: "sign the gh CLI in inside the container", DisableFlagParsing: true,
			ValidArgsFunction: org1,
			RunE: func(cmd *cobra.Command, args []string) error {
				return appFor(cmd).GhLogin(cmd.Context(), arg(args, 0), arg(args, 1))
			},
		}),
		one("up <org>", "build if needed and (re)create the container", func(a *app.App, c *cobra.Command, o string) error { return a.Up(c.Context(), o) }),
		writes(&cobra.Command{
			Use: "build [docker-build-args...]", Short: "rebuild berth's image (updates Claude Code)", DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error { return appFor(cmd).Build(cmd.Context(), args) },
		}),
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
		// backup parses its own arguments, as ccenv does. It writes (backup files, and containers run)
		// but never changes the org, so an org named explicitly can be either tool's.
		writes(&cobra.Command{
			Use:   "backup <org>...|--all [--plan] [-o file|dir|-] [--passphrase | -r <key> | --no-encrypt] [--keep N]",
			Short: "encrypted backup of orgs (age key, recipients, or a gpg passphrase)", DisableFlagParsing: true,
			ValidArgsFunction: completeBackup,
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Backup(cmd.Context(), args) },
		}),
		writes(&cobra.Command{
			Use: "keygen", Short: "create the age key backups encrypt to (no prompts; good for cron)", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(),
			RunE:              func(cmd *cobra.Command, _ []string) error { return appFor(cmd).Keygen(cmd.Context()) },
		}),
		repo,
		clone,
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
			Use: "fw <org> [show|allow|deny|on|off|edit|reload|presets|test] [entries...]", Short: "the egress allowlist (changes apply live)",
			DisableFlagParsing: true, // entries are positional, as in ccenv
			ValidArgsFunction:  completeFw,
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
