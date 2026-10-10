package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/docgen"
	"github.com/ar4mirez/berth/internal/ops"
)

// berth's command line (#140): `berth <group> <verb> [<org>] …`, one spelling per command.
//
// ccenv's flat commands (addCommands) stay in the code as the operations they are. Each command
// here is one of them under its new name, or calls one with its arguments put in ccenv's order, so
// the catalog (internal/ops), the --read-only guard and the parity suite keep describing the same
// operations.
//
// Run as `ccenv` (system install --alias ccenv), or with BERTH_SPELLINGS=ccenv, berth has ccenv's
// flat command line instead (organize): the parity suite runs it that way.

// spellingsEnv selects ccenv's command line: BERTH_SPELLINGS=ccenv.
const spellingsEnv = "BERTH_SPELLINGS"

func ccenvSpellings() bool {
	return os.Getenv(spellingsEnv) == "ccenv" || filepath.Base(os.Args[0]) == "ccenv"
}

const (
	// opKey is the flat command a native command runs: the operation's name in ops.Catalog.
	opKey = "berth/op"
	// subKey is that operation's subcommand, when the native command fixes it (fw allow).
	subKey = "berth/sub"
	// goneKey marks a removed spelling, with the one that replaced it.
	goneKey = "berth/gone"
)

// flatArgs maps a native command to its arguments in the flat command's order, for the pre-run
// hook, which looks the operation up before the command runs.
var flatArgs sync.Map // *cobra.Command -> func([]string) []string

// opOf is the operation a command runs: its name and subcommand in ops.Catalog, and its arguments
// as that operation takes them.
func opOf(cmd *cobra.Command, args []string) (name, sub string, fargs []string) {
	name, fargs = cmd.Name(), args
	if n := cmd.Annotations[opKey]; n != "" {
		name = n
	} else if p := cmd.Parent(); p != nil && p.HasParent() {
		// A group's own verb (host ls): the operation is the group's, and the verb its subcommand.
		sub = cmd.Name()
		if op, ok := ops.Lookup(p.Name(), sub); ok && op.Access == ops.BySub {
			// Its own subcommand is its last argument when that is one (host guard box1 status).
			last := ""
			if len(args) > 1 {
				last = args[len(args)-1]
			}
			if _, known := op.Subs[last]; !known {
				last = ""
			}
			sub += "/" + last
		}
		return p.Name(), sub, append([]string{cmd.Name()}, args...)
	}
	if f, ok := flatArgs.Load(cmd); ok {
		fargs = f.(func([]string) []string)(args)
	}
	if s, ok := cmd.Annotations[subKey]; ok {
		return name, s, fargs
	}
	return name, subOf(name, fargs), fargs
}

// argMap puts a verb's arguments in its flat command's order.
type argMap struct {
	to func([]string) []string
	// orgMissing reports whether the org was left out (nil: the flat command works that out).
	orgMissing func([]string) bool
}

// prefixed is a verb the flat command already takes first: repo add <org> … .
func prefixed(words ...string) argMap {
	return argMap{to: func(args []string) []string { return append(slices.Clone(words), args...) }}
}

// orgVerb is a verb the flat command takes after the org: `fw allow acme x` is `fw acme allow x`.
// fixed is how many arguments that aren't flags follow the org (-1: any number). With the org left
// out, the verb goes first, and the flat command puts the default org before it.
func orgVerb(verb string, fixed int) argMap {
	missing := func(args []string) bool {
		if fixed >= 0 {
			n := 0
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					n++
				}
			}
			return n <= fixed
		}
		return len(args) == 0 || app.OrgMissing(args[0], true, nil)
	}
	return argMap{orgMissing: missing, to: func(args []string) []string {
		if missing(args) {
			return append([]string{verb}, args...)
		}
		return append([]string{args[0], verb}, args[1:]...)
	}}
}

type tree struct {
	root *cobra.Command
	flat map[string]*cobra.Command
	used map[string]bool
}

func (t *tree) take(name string) *cobra.Command {
	c := t.flat[name]
	if c == nil {
		panic("cli: no command " + name)
	}
	t.used[name] = true
	return c
}

func annotate(c *cobra.Command, k, v string) {
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[k] = v
}

// path is a command's words after "berth".
func path(parent *cobra.Command, name string) string {
	return strings.TrimSpace(strings.TrimPrefix(parent.CommandPath(), "berth") + " " + name)
}

// group is a noun: it only gathers verbs.
func (t *tree) group(parent *cobra.Command, name, short string, example ...string) *cobra.Command {
	g := &cobra.Command{Use: name, Short: short, Example: "  " + strings.Join(example, "\n  ")}
	if parent == t.root {
		g.GroupID = "commands"
	}
	parent.AddCommand(g)
	return g
}

// move puts the flat command `name` under parent as `as`: the same command, renamed.
func (t *tree) move(parent *cobra.Command, name, as string) *cobra.Command {
	c := t.take(name)
	_, rest, _ := strings.Cut(c.Use, " ")
	c.Use = strings.TrimSpace(as + " " + rest)
	c.Hidden = false
	c.Example = "  " + strings.Join(respell(examples[name], name, path(parent, as)), "\n  ")
	annotate(c, opKey, name)
	parent.AddCommand(c)
	return c
}

// dup is a second place for a command: the same operation, arguments and access.
func dup(c *cobra.Command, use, example string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: c.Short, Long: c.Long, Example: example, Args: c.Args, ValidArgs: c.ValidArgs,
		DisableFlagParsing: c.DisableFlagParsing, ValidArgsFunction: c.ValidArgsFunction, RunE: c.RunE,
		Annotations: maps.Clone(c.Annotations),
	}
}

// verb is a subcommand that calls the flat command `name` with its arguments rearranged by m.
func (t *tree) verb(parent *cobra.Command, name, sub, use, short string, m argMap, complete cobra.CompletionFunc, example ...string) *cobra.Command {
	flat := t.take(name)
	run := flat.RunE
	word, _, _ := strings.Cut(use, " ")
	access := accessRead
	if op, ok := ops.Lookup(name, sub); ok && op.Access == ops.Write {
		access = accessWrite
	}
	c := &cobra.Command{
		Use: use, Short: short, DisableFlagParsing: true, ValidArgsFunction: complete,
		Example:     "  " + strings.Join(example, "\n  "),
		Annotations: map[string]string{accessKey: access, opKey: name, subKey: sub},
	}
	if flat.Annotations[ownArgsKey] != "" {
		annotate(c, ownArgsKey, "true")
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if m.orgMissing != nil && m.orgMissing(args) && appFor(cmd).ContextOrg() == "" {
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf(
				"%s: which org? (%s; or set a default org: berth org use <org>)", path(parent, word), strings.TrimSuffix(cmd.UseLine(), " [flags]"))}
		}
		return run(cmd, m.to(args))
	}
	flatArgs.Store(c, m.to)
	parent.AddCommand(c)
	return c
}

// orgFirst lets a group take ccenv's order too (berth fw acme allow x): the instructions inside
// images built before #140 use it. With no arguments it is the group's help.
func (t *tree) orgFirst(g *cobra.Command, name string) {
	flat := t.take(name)
	run := flat.RunE
	g.DisableFlagParsing, g.Args, g.ValidArgsFunction = true, cobra.ArbitraryArgs, nil
	g.Annotations = map[string]string{accessKey: flat.Annotations[accessKey], opKey: name}
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return run(cmd, args)
	}
}

// gone is a removed spelling: it names the one that replaced it, and runs nothing.
func (t *tree) gone(name, now string) {
	t.used[name] = true
	t.root.AddCommand(&cobra.Command{
		Use: name, Hidden: true, DisableFlagParsing: true, Args: cobra.ArbitraryArgs,
		Annotations: map[string]string{goneKey: now},
		RunE: func(*cobra.Command, []string) error {
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("'%s' is now 'berth %s' (berth %s --help)", name, now, now)}
		},
	})
}

// kept leaves a flat command working at the top level, hidden: something other than a person runs
// it (docs/prd/140-cli-layout.md has each one's reason).
func (t *tree) kept(name string) *cobra.Command {
	c := t.take(name)
	c.Hidden = true
	setExample(c, name)
	t.root.AddCommand(c)
	return c
}

var org1 = completeArgs(orgArg)

// completeVerb completes the org, then one list per following position.
func completeVerb(positions ...[]string) cobra.CompletionFunc {
	return completeArgs(append([][]string{orgArg}, positions...)...)
}

// layout builds berth's command line from the flat commands on root.
func layout(root *cobra.Command) {
	t := &tree{root: root, flat: map[string]*cobra.Command{}, used: map[string]bool{}}
	all := slices.Clone(root.Commands())
	for _, c := range all {
		t.flat[c.Name()] = c
	}
	root.RemoveCommand(all...)
	root.AddGroup(&cobra.Group{ID: "everyday", Title: "Everyday:"}, &cobra.Group{ID: "commands", Title: "Commands:"})
	root.SetHelpCommandGroupID("commands")
	root.Long = root.Short + ".\n\nEvery command lives in one group: `berth <group> --help` lists its commands, and every command has\n" +
		"its own --help. The everyday ones also work on their own: berth up acme is berth org up acme.\n" +
		"Leave the org out to use the default one (berth org use)."

	// org
	org := t.group(root, "org", "orgs: create, start, stop, connect, move", "berth org ls", "berth org create acme", "berth org up acme")
	for _, v := range [][2]string{
		{"ls", "ls"}, {"init", "create"}, {"info", "info"}, {"up", "up"}, {"down", "down"}, {"restart", "restart"},
		{"destroy", "destroy"}, {"attach", "attach"}, {"shell", "shell"}, {"claude", "claude"}, {"run", "run"}, {"exec", "exec"},
		{"logs", "logs"}, {"connect", "connect"}, {"rehydrate", "rehydrate"}, {"migrate", "migrate"}, {"use", "use"},
		{"takeover", "takeover"}, {"handback", "handback"},
	} {
		t.move(org, v[0], v[1])
	}
	pw := t.group(org, "password", "the browser terminal's password", "berth org password show acme", "berth org password rotate acme")
	t.verb(pw, "password", "show", "show <org>", "print the browser terminal's password", orgVerb("show", 0), org1, "berth org password show acme")
	t.verb(pw, "password", "rotate", "rotate <org>", "set a new password (restarts a running org)", orgVerb("rotate", 0), org1, "berth org password rotate acme")

	// The everyday ones, also on their own.
	for _, name := range []string{"ls", "up", "down", "restart", "shell", "claude", "attach", "logs", "info"} {
		c := t.flat[name]
		s := dup(c, c.Use, "  "+strings.Join(examples[name], "\n  "))
		s.GroupID = "everyday"
		root.AddCommand(s)
	}

	// repo: ccenv's order already.
	repo := t.group(root, "repo", "the repos allowed in an org's /workspace", "berth repo add acme acme/widgets", "berth repo ls acme")
	rv := func(sub, use, short string, pos [][]string, ex ...string) {
		t.verb(repo, "repo", sub, sub+" "+use, short, prefixed(sub), completeVerb(pos...), ex...)
	}
	rv("add", "<org> <owner/repo|git-url> [--dir name] [--branch b] [--no-clone]", "register a repo and clone it", nil,
		"berth repo add acme acme/widgets", "berth repo add acme acme/widgets --branch main --dir widgets")
	rv("new", "<org> <owner/repo> [--private|--public|--internal] [-d desc] [--template o/r] [--readme] [--gitignore Node] [--license mit] [--dir d]",
		"create a repo on GitHub, register it and clone it", nil, "berth repo new acme acme/gadgets --private --readme")
	rv("publish", "<org> <dir> <owner/repo> [--private|--public|--internal]", "put a local folder on GitHub and register it", nil,
		"berth repo publish acme prototype acme/prototype --private")
	rv("ls", "<org>", "the registered repos, their state, and what isn't registered", nil, "berth repo ls acme", "berth --output json repo ls acme")
	rv("rm", "<org> <dir> [--delete]", "unregister a repo (its folder goes to quarantine; --delete removes it)", nil,
		"berth repo rm acme widgets", "berth repo rm acme widgets --delete")
	rv("adopt", "<org> <dir>...|--all", "register folders already in /workspace", nil, "berth repo adopt acme widgets", "berth repo adopt acme --all")
	rv("sync", "<org>", "clone what is registered and missing", nil, "berth repo sync acme")
	rv("audit", "<org> [--quiet]", "check /workspace against the registry", nil, "berth repo audit acme")
	policy := t.verb(repo, "repo", "policy", "policy <org> [enforce|warn|off]", "show or set what happens to unregistered repos (setting restarts a running org)",
		prefixed("policy"), completeVerb([]string{"enforce", "warn", "off"}), "berth repo policy acme", "berth repo policy acme enforce")
	delete(policy.Annotations, subKey) // its access depends on its mode: subOf works it out

	// fw
	fw := t.group(root, "fw", "the egress allowlist (changes apply live)", "berth fw show acme", "berth fw allow acme pypi.org @python")
	t.orgFirst(fw, "fw")
	fv := func(sub, use, short string, fixed int, complete cobra.CompletionFunc, ex ...string) {
		t.verb(fw, "fw", sub, strings.TrimSpace(sub+" "+use), short, orgVerb(sub, fixed), complete, ex...)
	}
	presets := func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return append(completionOrgsAt(cmd, toComplete), app.FwPresets...), cobra.ShellCompDirectiveNoFileComp
		}
		return app.FwPresets, cobra.ShellCompDirectiveNoFileComp
	}
	fv("show", "<org>", "the allowlist, and whether the firewall is on", 0, org1, "berth fw show acme", "berth --output json fw show acme")
	fv("allow", "<org> <domain|ip|cidr|@preset>...", "add entries to the allowlist", -1, presets, "berth fw allow acme pypi.org", "berth fw allow acme @python")
	fv("deny", "<org> <entry>...", "remove entries from the allowlist", -1, org1, "berth fw deny acme pypi.org")
	fv("on", "<org>", "turn the firewall on", 0, org1, "berth fw on acme")
	fv("off", "<org>", "turn the firewall off: the org can reach anything", 0, org1, "berth fw off acme")
	fv("edit", "<org>", "edit the allowlist in $EDITOR, then apply it", 0, org1, "berth fw edit acme")
	fv("reload", "<org>", "resolve the allowlist again (a host's addresses changed)", 0, org1, "berth fw reload acme")
	fv("presets", "<org>", "the presets allow takes (@python, @node, …)", 0, org1, "berth fw presets acme")
	fv("test", "<org> [host...]", "check from inside the org which hosts it can reach", -1, org1, "berth fw test acme", "berth fw test acme pypi.org")

	// env
	env := t.group(root, "env", "custom variables (API keys) for an org's sessions", "berth env ls acme", "berth env set acme OPENROUTER_API_KEY")
	env.Long = "Custom variables (API keys) for an org's sessions. set reads the value hidden, or from stdin.\n\n" +
		"accept takes secrets typed inside the org instead: in the org's terminal, `berth-secret-drop KEY`\n" +
		"reads the value hidden and leaves a one-time drop; `berth env accept <org>` then stores what is\n" +
		"waiting (--list shows the names, accept <org> KEY takes one). The value is never typed on the host."
	t.orgFirst(env, "env")
	ev := func(sub, use, short string, fixed int, ex ...string) {
		t.verb(env, "env", sub, strings.TrimSpace(sub+" "+use), short, orgVerb(sub, fixed), org1, ex...)
	}
	ev("ls", "<org>", "the names of an org's variables (never their values)", 0, "berth env ls acme")
	ev("set", "<org> KEY [--no-restart]", "set a variable: the value is read hidden, or from stdin (restarts a running org)", 1,
		"berth env set acme OPENROUTER_API_KEY", "berth env set acme OPENROUTER_API_KEY --no-restart < key.txt")
	ev("unset", "<org> KEY [--no-restart]", "remove a variable (restarts a running org)", 1, "berth env unset acme OPENROUTER_API_KEY")
	ev("accept", "<org> [KEY...] [--list] [--no-restart]", "store the secrets typed inside the org with berth-secret-drop", -1,
		"berth env accept acme", "berth env accept acme --list", "berth env accept acme OPENROUTER_API_KEY")
	sec := t.take("secrets")
	mig := sec.Commands()[0]
	sec.RemoveCommand(mig)
	annotate(mig, opKey, "secrets")
	annotate(mig, subKey, "migrate")
	mig.Example = "  berth env migrate acme\n  berth env migrate acme --no-backup"
	env.AddCommand(mig)

	// pkg
	pkg := t.group(root, "pkg", "system packages in an org's image (applied at its next restart)", "berth pkg add acme libpq-dev", "berth pkg ls acme")
	pkg.Long = t.flat["pkg"].Long
	t.orgFirst(pkg, "pkg")
	pv := func(sub, use, short string, fixed int, complete cobra.CompletionFunc, ex ...string) {
		t.verb(pkg, "pkg", sub, strings.TrimSpace(sub+" "+use), short, orgVerb(sub, fixed), complete, ex...)
	}
	pkgPresets := func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return append(completionOrgsAt(cmd, toComplete), ops.PresetNames()...), cobra.ShellCompDirectiveNoFileComp
		}
		return ops.PresetNames(), cobra.ShellCompDirectiveNoFileComp
	}
	pv("ls", "<org>", "the packages an org's image adds", 0, org1, "berth pkg ls acme")
	pv("add", "<org> <package|@preset>... [--no-build]", "add packages and build the org's image", -1, pkgPresets,
		"berth pkg add acme libpq-dev", "berth pkg add acme @playwright-chromium", "berth restart acme   # to run on the new image")
	pv("rm", "<org> <entry>... [--no-build]", "remove packages and build the org's image", -1, org1, "berth pkg rm acme libpq-dev")
	pv("presets", "<org>", "the presets add takes", 0, org1, "berth pkg presets acme")
	pv("build", "<org>", "build the org's image now", 0, org1, "berth pkg build acme")

	// account
	acct := t.group(root, "account", "sign-in: Claude, Remote Control, GitHub", "berth account signin acme", "berth account whoami")
	for _, v := range [][2]string{{"auth", "signin"}, {"token", "token"}, {"login", "login"}, {"logout", "logout"}, {"gh-login", "gh"}, {"whoami", "whoami"}} {
		t.move(acct, v[0], v[1])
	}
	rc := t.group(acct, "remote", "the Remote Control service in an org", "berth account remote status acme", "berth account remote restart acme")
	t.verb(rc, "remote", "status", "status <org>", "whether Remote Control is running", orgVerb("status", 0), org1, "berth account remote status acme")
	t.verb(rc, "remote", "logs", "logs <org>", "follow the service's log", orgVerb("logs", 0), org1, "berth account remote logs acme")
	t.verb(rc, "remote", "restart", "restart <org>", "restart the service (the container keeps running)", orgVerb("restart", 0), org1, "berth account remote restart acme")

	// backup
	bk := t.group(root, "backup", "backups: create, restore, schedule, the key", "berth backup create acme", "berth backup create --all --keep 14",
		"berth backup restore backups/acme-20260101-030000.tar.zst.age")
	flatBackup := t.take("backup")
	create := dup(flatBackup, "create "+strings.TrimPrefix(flatBackup.Use, "backup "), "  "+strings.Join(respell(examples["backup"], "backup", "backup create"), "\n  "))
	annotate(create, opKey, "backup")
	bk.AddCommand(create)
	// Installed timers run `berth backup --all …` every night: it stays, hidden from help.
	bk.DisableFlagParsing, bk.Args = true, cobra.ArbitraryArgs
	bk.Annotations = maps.Clone(flatBackup.Annotations)
	annotate(bk, opKey, "backup")
	runBackup := flatBackup.RunE
	bk.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return runBackup(cmd, args)
	}
	restore := t.kept("restore") // migrate runs `berth restore -` over ssh, on another version's berth too
	r := dup(restore, restore.Use, "  "+strings.Join(respell(examples["restore"], "restore", "backup restore"), "\n  "))
	annotate(r, opKey, "restore")
	bk.AddCommand(r)
	t.move(bk, "keygen", "keygen")
	t.kept("schedule") // backup schedule on --host runs it on the host
	sch := t.group(bk, "schedule", "nightly backups of every org (a systemd user timer, or cron)", "berth backup schedule on --at 03:30 --keep 14", "berth backup schedule status")
	t.verb(sch, "schedule", "", "on [--at HH:MM] [--keep N] [-o dir] [--host <name>]", "schedule a nightly backup of every org",
		argMap{to: func(args []string) []string { return args }}, completeArgs([]string{"--at", "--keep", "-o", "--host"}),
		"berth backup schedule on", "berth backup schedule on --at 03:30 --keep 14", "berth backup schedule on --host box1")
	for _, v := range [][2]string{{"status", "whether backups are scheduled, and when the last one ran"}, {"run", "run the scheduled backup now"}, {"off", "remove the schedule"}} {
		t.verb(sch, "schedule", v[0], v[0]+" [--host <name>]", v[1], prefixed(v[0]), completeArgs([]string{"--host"}), "berth backup schedule "+v[0])
	}

	// host: already a group.
	host := t.take("host")
	host.GroupID = "commands"
	root.AddCommand(host)
	setExample(host, "host")
	for _, s := range host.Commands() {
		setExample(s, "host "+s.Name())
	}

	// system
	sys := t.group(root, "system", "berth itself: install, upgrade, its image, shell completion", "berth system upgrade", "berth system install")
	for _, name := range []string{"install", "completion"} { // run by upgrade and by shell start-up files: they stay, hidden
		c := t.kept(name)
		s := dup(c, c.Use, "  "+strings.Join(respell(examples[name], name, "system "+name), "\n  "))
		annotate(s, opKey, name)
		sys.AddCommand(s)
	}
	for _, name := range []string{"upgrade", "pull", "build", "parity-check"} {
		t.move(sys, name, name)
	}
	svc := t.group(sys, "service", "berth serve as a service of yours: at login, without starting it by hand", "berth system service install", "berth system service status")
	svc.Long = t.flat["service"].Long
	for _, v := range [][3]string{
		{"install", "install [--listen ADDR]", "install and start it (a systemd user unit, or a launchd agent)"},
		{"uninstall", "uninstall", "stop it and remove it"},
		{"status", "status", "whether it is installed and running"},
		{"logs", "logs [-f]", "its last log lines (-f: follow)"},
	} {
		ex := "berth system service " + v[0]
		t.verb(svc, "service", v[0], v[1], v[2], prefixed(v[0]), completeArgs(), ex)
	}
	t.kept("image-tag")

	for _, name := range []string{"tui", "ui", "mcp", "serve"} {
		c := t.take(name)
		c.GroupID = "commands"
		setExample(c, name)
		root.AddCommand(c)
	}

	// One spelling per command: ccenv's others name their replacement.
	for _, g := range [][2]string{
		{"init", "org create"}, {"destroy", "org destroy"}, {"run", "org run"}, {"exec", "org exec"}, {"connect", "org connect"},
		{"rehydrate", "org rehydrate"}, {"migrate", "org migrate"}, {"use", "org use"}, {"takeover", "org takeover"}, {"handback", "org handback"},
		{"password", "org password"}, {"remote", "account remote"},
		{"auth", "account signin"}, {"token", "account token"}, {"login", "account login"}, {"logout", "account logout"},
		{"gh-login", "account gh"}, {"whoami", "account whoami"},
		{"clone", "repo add"}, {"secrets", "env migrate"}, {"keygen", "backup keygen"},
		{"upgrade", "system upgrade"}, {"pull", "system pull"}, {"build", "system build"}, {"parity-check", "system parity-check"},
	} {
		t.gone(g[0], g[1])
	}
	for name := range t.flat {
		if !t.used[name] {
			panic("cli: the flat command " + name + " has no place in the tree")
		}
	}
	respellHelp(root)
	linkDocs(root, nil)
	root.Long += "\n\nDocs: " + docgen.Site + "/"
}

// respellHelp: the commands a help text names, as this command line spells them.
func respellHelp(c *cobra.Command) {
	for _, s := range c.Commands() {
		s.Short, s.Long = ops.Respell(s.Short), ops.Respell(s.Long)
		respellHelp(s)
	}
}
