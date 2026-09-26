package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/host/local"
)

// appFor builds the App a command runs against: the resolved state, on the local host.
func appFor(cmd *cobra.Command) *app.App {
	a := app.New(stateFrom(cmd.Context()), local.New(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Getenv)
	a.OpenTTY = local.OpenTTY
	a.Self, _ = os.Executable()
	a.Invoked = invokedPath()
	a.Output = outputFrom(cmd.Context())
	return a
}

// invokedPath is the path berth was run as, without resolving symlinks: os.Args[0] as given if it
// has a slash (made absolute), else where PATH finds it. "" if that fails.
func invokedPath() string {
	p := os.Args[0]
	if !strings.Contains(p, "/") {
		found, err := exec.LookPath(p)
		if err != nil {
			return ""
		}
		p = found
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return abs
}

// secretsCmd is `berth secrets migrate <org> [--no-backup]` (#37).
func secretsCmd() *cobra.Command {
	c := &cobra.Command{Use: "secrets", Short: "where an org keeps its tokens and custom variables"}
	c.AddCommand(writes(&cobra.Command{
		Use:   "migrate <org> [--no-backup]",
		Short: "move an org's tokens and custom variables out of org.env into files (backup first; restarts nothing)",
		Long: "Moves the org's tokens (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY, GH_TOKEN) and its custom variables\n" +
			"from org.env into one file each (0600) under the org's config/secrets/env, so docker inspect can't show\n" +
			"them. A backup is taken first. Nothing restarts: the org's next restart takes the values from the files.",
		DisableFlagParsing: true,
		ValidArgsFunction:  completeArgs(orgArg),
		RunE:               localOnly("secrets migrate", nthArg(0), func(cmd *cobra.Command, args []string) error { return appFor(cmd).SecretsMigrate(cmd.Context(), args) }),
	}))
	return c
}

// hostCmd is `berth host add|ls|rm|rotate-access` (#44).
func hostCmd() *cobra.Command {
	c := &cobra.Command{Use: "host", Short: "the hosts berth manages: this machine and others over SSH"}
	c.AddCommand(
		writes(&cobra.Command{
			Use:   "add <name> <[user@]host[:port]> [--home <dir>] [--identity <key file>] [--fingerprint SHA256:…] [--accept-new-host-key]",
			Short: "register a host: pin its SSH key, check Docker, and give berth its own key there",
			Long: "Connects with your own SSH access (ssh-agent, or --identity), pins the host's key in berth's known_hosts\n" +
				"(asking you to confirm its fingerprint, or taking --fingerprint), checks Docker and compose, then adds a key\n" +
				"of berth's own to the host's authorized_keys and records the host. --home is the state root there\n" +
				"(default ~/.local/share/berth). Nothing on the host is started or restarted.",
			DisableFlagParsing: true,
			RunE:               func(cmd *cobra.Command, args []string) error { return appFor(cmd).HostAdd(cmd.Context(), args) },
		}),
		reads(&cobra.Command{
			Use:   "ls",
			Short: "each host: reachable, its Docker version and its number of orgs",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return appFor(cmd).HostLs(cmd.Context(), args) },
		}),
		writes(&cobra.Command{
			Use:                "rm <name> [--force]",
			Short:              "forget a host and revoke berth's key there (refused while it has orgs, unless --force)",
			DisableFlagParsing: true,
			RunE:               func(cmd *cobra.Command, args []string) error { return appFor(cmd).HostRm(cmd.Context(), args) },
		}),
		writes(&cobra.Command{
			Use:                "rotate-access <name>",
			Short:              "replace berth's key on a host (the old one goes only once the new one works)",
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return appFor(cmd).HostRotateAccess(cmd.Context(), args)
			},
		}),
	)
	return c
}

// onOrg runs fn with the App for the org argument pick chooses: org@host acts on that registered
// host (#45), with the bare org in its place; a bare org, or org@local, stays on this machine.
// pick returns -1 when there's no org argument.
func onOrg(pick func([]string) int, fn func(a *app.App, cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a := appFor(cmd)
		i := pick(args)
		if i < 0 || i >= len(args) {
			return fn(a, cmd, args)
		}
		b, o, done, err := a.At(cmd.Context(), args[i])
		if err != nil {
			return err
		}
		defer done()
		args = slices.Clone(args)
		args[i] = o
		return fn(b, cmd, args)
	}
}

// onOrgs is onOrg for commands naming several orgs (whoami): they must all be on one host.
func onOrgs(fn func(a *app.App, cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		host, bare := "", make([]string, len(args))
		for i, x := range args {
			o, h := app.SplitAddress(x)
			if h == "local" {
				h = ""
			}
			if i > 0 && h != host {
				return fmt.Errorf("%s: name orgs on one host at a time", cmd.Name())
			}
			host, bare[i] = h, o
		}
		if host == "" {
			return fn(appFor(cmd), cmd, bare)
		}
		a, _, done, err := appFor(cmd).At(cmd.Context(), "@"+host)
		if err != nil {
			return err
		}
		defer done()
		return fn(a, cmd, bare)
	}
}

// localOnly refuses org@host addresses (at the positions pick chooses) for commands that can't act
// on another host yet: they read the operator's backup key or write the operator's files.
func localOnly(name string, pick func([]string) int, run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		check := args
		if i := pick(args); i >= 0 {
			check = []string{arg(args, i)}
		}
		if err := app.RefuseRemote(name, check...); err != nil {
			return err
		}
		return run(cmd, args)
	}
}

// nthArg picks the nth argument; allArgs picks none, meaning every argument (localOnly).
func nthArg(n int) func([]string) int { return func([]string) int { return n } }

func allArgs([]string) int { return -1 }

// tokenOrg picks token's org: its last argument that isn't one of its flags, as Token reads it.
func tokenOrg(args []string) int {
	i := -1
	for j, x := range args {
		if x != "--paste" && x != "--no-restart" {
			i = j
		}
	}
	return i
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
		RunE: onOrg(nthArg(1), func(a *app.App, cmd *cobra.Command, args []string) error {
			return a.Repo(cmd.Context(), arg(args, 0), arg(args, 1), rest(args, 2))
		}),
	})
	clone := writes(&cobra.Command{
		Use: "clone <org> <owner/repo|url> [--dir d] [--branch b] [--no-clone]", Short: "register a repo and clone it (repo add)", DisableFlagParsing: true,
		ValidArgsFunction: completeArgs(orgArg),
		RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
			return a.Repo(cmd.Context(), "add", arg(args, 0), rest(args, 1))
		}),
	})
	org1 := completeArgs(orgArg)
	one := func(use, short string, run func(*app.App, *cobra.Command, string) error) *cobra.Command {
		return writes(&cobra.Command{
			Use: use, Short: short, Args: cobra.ArbitraryArgs, ValidArgsFunction: org1,
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error { return run(a, cmd, arg(args, 0)) }),
		})
	}
	// claude and run pass their arguments straight to claude, flags included, so cobra doesn't parse
	// them. Global flags go before the command (berth --read-only claude acme …); the root's
	// TraverseChildren makes sure those are parsed rather than handed to claude.
	passthrough := func(use, short string, run func(*app.App, *cobra.Command, string, []string) error) *cobra.Command {
		return writes(&cobra.Command{
			Use: use, Short: short, DisableFlagParsing: true, ValidArgsFunction: org1,
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				var rest []string
				if len(args) > 1 {
					rest = args[1:]
				}
				return run(a, cmd, arg(args, 0), rest)
			}),
		})
	}
	root.AddCommand(
		// init and env parse their own arguments, as ccenv does: the same messages, and env's
		// --no-restart only as the 4th argument.
		writes(&cobra.Command{
			Use: "init <org> [--name N --email E]", Short: "scaffold a new org (berth's own: MANAGER=berth)", DisableFlagParsing: true,
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error { return a.Init(cmd.Context(), args) }),
		}),
		reads(&cobra.Command{
			Use: "env <org> [ls | set KEY | unset KEY] [--no-restart]", Short: "custom env vars for the container", DisableFlagParsing: true,
			ValidArgsFunction: completeArgs(orgArg, []string{"ls", "set", "unset"}),
			RunE:              onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error { return a.Env(cmd.Context(), args) }),
		}),
		reads(&cobra.Command{
			Use: "password <org> [show|rotate]", Short: "browser-terminal password", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg, []string{"show", "rotate"}),
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				return a.Password(cmd.Context(), arg(args, 0), arg(args, 1))
			}),
		}),
		reads(&cobra.Command{
			Use: "remote <org> [status|logs|restart]", Short: "the Remote Control service", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg, []string{"status", "logs", "restart"}),
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				return a.Remote(cmd.Context(), arg(args, 0), arg(args, 1))
			}),
		}),
		// The sign-in commands parse their arguments as ccenv does (--paste/--no-restart anywhere for
		// token; --all/--force only as the second argument).
		writes(&cobra.Command{
			Use: "token <org> [--paste] [--no-restart]", Short: "the 1-year token (claude setup-token in the container)", DisableFlagParsing: true,
			ValidArgsFunction: org1,
			RunE:              onOrg(tokenOrg, func(a *app.App, cmd *cobra.Command, args []string) error { return a.Token(cmd.Context(), args) }),
		}),
		one("auth <org>", "sign an org in: token, then the Remote Control login", func(a *app.App, c *cobra.Command, o string) error { return a.Auth(c.Context(), o) }),
		one("login <org>", "the full login that enables claude.ai/code (Remote Control)", func(a *app.App, c *cobra.Command, o string) error { return a.Login(c.Context(), o) }),
		writes(&cobra.Command{
			Use: "logout <org> [--all]", Short: "remove the Remote Control login (--all: the token too)", DisableFlagParsing: true,
			ValidArgsFunction: org1,
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				return a.Logout(cmd.Context(), arg(args, 0), arg(args, 1))
			}),
		}),
		writes(&cobra.Command{
			Use: "gh-login <org> [--force]", Short: "sign the gh CLI in inside the container", DisableFlagParsing: true,
			ValidArgsFunction: org1,
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				return a.GhLogin(cmd.Context(), arg(args, 0), arg(args, 1))
			}),
		}),
		one("up <org>", "build if needed and (re)create the container", func(a *app.App, c *cobra.Command, o string) error { return a.Up(c.Context(), o) }),
		writes(&cobra.Command{
			Use:   "upgrade [--version vX.Y.Z] | --rollback",
			Short: "install a verified release and switch to it (the previous one stays; restarts nothing)", DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error { return appFor(cmd).Upgrade(cmd.Context(), args) },
		}),
		reads(&cobra.Command{
			Use: "image-tag", Short: "the image tag this berth uses", Hidden: true, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return appFor(cmd).ImageTag() },
		}),
		writes(&cobra.Command{
			Use: "pull", Short: "get the released image now, so restarts don't wait for it (restarts nothing)", Args: cobra.NoArgs,
			ValidArgsFunction: completeArgs(),
			RunE:              func(cmd *cobra.Command, _ []string) error { return appFor(cmd).Pull(cmd.Context()) },
		}),
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
			RunE:              localOnly("backup", allArgs, func(cmd *cobra.Command, args []string) error { return appFor(cmd).Backup(cmd.Context(), args) }),
		}),
		writes(&cobra.Command{
			Use: "keygen", Short: "create the age key backups encrypt to (no prompts; good for cron)", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(),
			RunE:              func(cmd *cobra.Command, _ []string) error { return appFor(cmd).Keygen(cmd.Context()) },
		}),
		writes(&cobra.Command{
			Use:   "install [bin-dir] [--alias NAME]",
			Short: "link berth onto PATH (default ~/.local/bin) with bash completion; --alias ccenv at cutover", DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return appFor(cmd).Install(cmd.Context(), args, func(name string) ([]byte, error) { return bashCompletion(root, name) })
			},
		}),
		reads(&cobra.Command{
			Use:   "parity-check [--legacy PATH] [org...]",
			Short: "compare every read command under ccenv and berth --read-only on this state root (pre-cutover)", DisableFlagParsing: true,
			ValidArgsFunction: completeOrgs,
			RunE:              localOnly("parity-check", allArgs, func(cmd *cobra.Command, args []string) error { return appFor(cmd).ParityCheck(cmd.Context(), args) }),
		}),
		writes(&cobra.Command{
			Use: "takeover <org>", Short: "cutover: make a ccenv org berth's (MANAGER=berth); restarts nothing", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg),
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				return a.Takeover(cmd.Context(), arg(args, 0))
			}),
		}),
		writes(&cobra.Command{
			Use: "handback <org>", Short: "undo takeover: MANAGER=ccenv again; restarts nothing", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg),
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				return a.Handback(cmd.Context(), arg(args, 0))
			}),
		}),
		secretsCmd(),
		hostCmd(),
		// restore and migrate parse their own arguments, as ccenv does.
		writes(&cobra.Command{
			Use:   "restore <file|-> [--as name] [--identity|-i key] [--force] [--no-start] [--no-rehydrate]",
			Short: "restore an org from a backup (the restored org is berth's)", DisableFlagParsing: true,
			RunE: localOnly("restore", allArgs, func(cmd *cobra.Command, args []string) error { return appFor(cmd).Restore(cmd.Context(), args) }),
		}),
		one("rehydrate <org>", "reinstall what backups skip: repos, mise toolchains, dependencies", func(a *app.App, c *cobra.Command, o string) error {
			return a.Rehydrate(c.Context(), o)
		}),
		writes(&cobra.Command{
			Use:   "migrate <org> <[user@]host> [--as name] [--remote-dir dir]",
			Short: "stream an org to berth on another host over ssh", DisableFlagParsing: true,
			ValidArgsFunction: completeArgs(orgArg),
			RunE:              localOnly("migrate", nthArg(0), func(cmd *cobra.Command, args []string) error { return appFor(cmd).Migrate(cmd.Context(), args) }),
		}),
		// schedule parses its own arguments, as ccenv does; status reads, the rest write.
		reads(&cobra.Command{
			Use:   "schedule [--at HH:MM] [--keep N] [-o dir] | status | run | off",
			Short: "nightly backup --all (systemd user timer berth-backup, or cron)", DisableFlagParsing: true,
			ValidArgsFunction: completeArgs([]string{"status", "run", "off", "--at", "--keep", "-o"}),
			RunE:              func(cmd *cobra.Command, args []string) error { return appFor(cmd).Schedule(cmd.Context(), args) },
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
			RunE:              onOrgs(func(a *app.App, cmd *cobra.Command, args []string) error { return a.Whoami(cmd.Context(), args) }),
		}),
		reads(&cobra.Command{
			Use: "logs <org>", Short: "follow the container's logs", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg),
			RunE:              onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error { return a.Logs(cmd.Context(), arg(args, 0)) }),
		}),
		reads(&cobra.Command{
			Use: "fw <org> [show|allow|deny|on|off|edit|reload|presets|test] [entries...]", Short: "the egress allowlist (changes apply live)",
			DisableFlagParsing: true, // entries are positional, as in ccenv
			ValidArgsFunction:  completeFw,
			RunE: onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return a.Fw(cmd.Context(), "", nil)
				}
				return a.Fw(cmd.Context(), args[0], args[1:])
			}),
		}),
		reads(&cobra.Command{
			Use: "info <org>", Short: "every way to connect", Args: cobra.ArbitraryArgs,
			ValidArgsFunction: completeArgs(orgArg),
			RunE:              onOrg(nthArg(0), func(a *app.App, cmd *cobra.Command, args []string) error { return a.Info(cmd.Context(), arg(args, 0)) }),
		}),
	)
}
