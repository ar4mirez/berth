package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/docgen"
)

// The command layout (#55). berth's top-level commands are ccenv's, and stay exactly as they are:
// the parity suite, `install --alias ccenv`, and the instructions inside the image (berth fw <org>
// allow, berth repo add) all use them. On top of that:
//   - help shows them in sections (cobra groups);
//   - noun groups gather related commands under one name: org, account, system. Each verb there is
//     a copy of the top-level command (same run function, arguments and access), so both spellings
//     behave the same;
//   - the top-level commands a group now covers are hidden from help, but keep working.

// sections are help's groups, in order.
var sections = []*cobra.Group{
	{ID: "orgs", Title: "Orgs (everyday commands; `berth org` has them all):"},
	{ID: "inside", Title: "Inside an org:"},
	{ID: "account", Title: "Sign-in:"},
	{ID: "backups", Title: "Backups:"},
	{ID: "hosts", Title: "Hosts:"},
	{ID: "system", Title: "berth itself:"},
}

// placement is each top-level command's section; hidden ones live in a noun group now.
var placement = map[string]string{
	"org": "orgs", "ls": "orgs", "up": "orgs", "down": "orgs", "destroy": "orgs", "restart": "orgs", "attach": "orgs",
	"shell": "orgs", "claude": "orgs", "run": "orgs", "logs": "orgs", "info": "orgs", "connect": "orgs",
	"repo": "inside", "clone": "inside", "fw": "inside", "env": "inside", "secrets": "inside",
	"account": "account",
	"backup":  "backups", "restore": "backups", "schedule": "backups", "keygen": "backups",
	"host": "hosts", "use": "hosts",
	"system": "system",
}

// nounGroups: group -> verb -> the top-level command it copies. A top-level command listed here
// and not in placement is hidden from help (it still works).
var nounGroups = []struct {
	name, short string
	verbs       [][2]string
}{
	{"org", "orgs: create, start, stop, connect, move", [][2]string{
		{"ls", "ls"}, {"create", "init"}, {"info", "info"}, {"up", "up"}, {"down", "down"}, {"restart", "restart"},
		{"attach", "attach"}, {"shell", "shell"}, {"logs", "logs"}, {"claude", "claude"}, {"run", "run"},
		{"whoami", "whoami"}, {"rehydrate", "rehydrate"}, {"migrate", "migrate"}, {"password", "password"},
		{"remote", "remote"}, {"takeover", "takeover"}, {"handback", "handback"}, {"connect", "connect"},
		{"destroy", "destroy"},
	}},
	{"account", "sign an org in and out: Claude, Remote Control, GitHub", [][2]string{
		{"signin", "auth"}, {"token", "token"}, {"login", "login"}, {"logout", "logout"}, {"gh", "gh-login"}, {"whoami", "whoami"},
	}},
	{"system", "berth itself: install, upgrade, its image, shell completion", [][2]string{
		{"install", "install"}, {"upgrade", "upgrade"}, {"pull", "pull"}, {"build", "build"},
		{"completion", "completion"}, {"parity-check", "parity-check"},
	}},
}

// examples are each top-level command's, as typed; a group's copy gets them respelled.
var examples = map[string][]string{
	"ls":                 {"berth ls", "berth --output json ls"},
	"init":               {`berth init acme --name "Ada Lovelace" --email ada@example.com`, "berth init acme@box1"},
	"info":               {"berth info acme"},
	"connect":            {"berth connect acme@box1", "berth connect acme"},
	"up":                 {"berth up acme", "berth up acme@box1 --take-lease"},
	"down":               {"berth down acme"},
	"destroy":            {"berth destroy acme", "berth destroy acme --yes --keep-backups"},
	"restart":            {"berth restart acme"},
	"attach":             {"berth attach acme"},
	"shell":              {"berth shell acme"},
	"logs":               {"berth logs acme"},
	"claude":             {"berth claude acme", "berth claude acme --resume"},
	"run":                {`berth run acme "summarize the open PRs"`},
	"whoami":             {"berth whoami", "berth whoami acme globex"},
	"rehydrate":          {"berth rehydrate acme"},
	"migrate":            {"berth migrate acme box1", "berth migrate acme@box1 local --yes", "berth migrate acme ops@box.example   # ccenv's: to berth over ssh"},
	"password":           {"berth password acme", "berth password acme rotate"},
	"remote":             {"berth remote acme status", "berth remote acme restart"},
	"takeover":           {"berth takeover acme"},
	"handback":           {"berth handback acme"},
	"auth":               {"berth auth acme"},
	"token":              {"berth token acme", "berth token acme --paste --no-restart"},
	"login":              {"berth login acme"},
	"logout":             {"berth logout acme", "berth logout acme --all"},
	"gh-login":           {"berth gh-login acme"},
	"repo":               {"berth repo add acme acme/widgets", "berth repo ls acme", "berth repo policy acme enforce"},
	"clone":              {"berth clone acme acme/widgets --branch main"},
	"fw":                 {"berth fw acme show", "berth fw acme allow pypi.org @python", "berth fw acme test"},
	"env":                {"berth env acme ls", "berth env acme set OPENROUTER_API_KEY", "berth env acme unset OPENROUTER_API_KEY"},
	"secrets":            {"berth secrets migrate acme"},
	"backup":             {"berth backup acme", "berth backup --all --keep 14", "berth backup acme -o - > acme.tar.zst.age"},
	"restore":            {"berth restore backups/acme-20260101-030000.tar.zst.age", "berth restore - --as acme2 < acme.tar.zst.age"},
	"schedule":           {"berth schedule --at 03:30 --keep 14", "berth schedule status", "berth schedule --host box1"},
	"keygen":             {"berth keygen"},
	"host":               {"berth host add box1 ops@box1.example", "berth host ls"},
	"use":                {"berth use acme@box1", "berth use", "berth use --clear"},
	"install":            {"berth install", "berth install ~/bin --alias ccenv"},
	"upgrade":            {"berth upgrade", "berth upgrade --version v0.4.0", "berth upgrade --rollback"},
	"pull":               {"berth pull"},
	"build":              {"berth build", "berth build --no-cache"},
	"completion":         {"berth completion bash > ~/.local/share/bash-completion/completions/berth"},
	"parity-check":       {"berth --home ~/Work/claude-envs parity-check"},
	"image-tag":          {"berth image-tag"},
	"host add":           {"berth host add box1 ops@box1.example", "berth host add box1 box1.example:2222 --fingerprint SHA256:…"},
	"host ls":            {"berth host ls", "berth --output json host ls"},
	"host rm":            {"berth host rm box1", "berth host rm box1 --force"},
	"host guard":         {"berth host guard box1", "berth host guard box1 off"},
	"host rotate-access": {"berth host rotate-access box1"},
	"secrets migrate":    {"berth secrets migrate acme", "berth secrets migrate acme --no-backup"},
}

// organize lays the tree out: sections, the noun groups, examples, and which commands help hides.
func organize(root *cobra.Command) {
	root.AddGroup(sections...)
	root.Long = root.Short + ".\n\nccenv's commands keep working as they were (berth init, berth token, berth whoami, …): help lists them\n" +
		"under berth org, berth account and berth system. docs/cli.md has both spellings."
	byName := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		byName[c.Name()] = c
	}
	grouped := map[string]string{} // top-level command -> its group spelling ("org create")
	for _, g := range nounGroups {
		gc := &cobra.Command{Use: g.name, Short: g.short, GroupID: placement[g.name]}
		var lines []string
		for _, v := range g.verbs {
			src := byName[v[1]]
			if src == nil {
				panic("cli: no command " + v[1] + " for " + g.name + " " + v[0])
			}
			gc.AddCommand(copyAs(src, v[0], g.name))
			if grouped[v[1]] == "" {
				grouped[v[1]] = g.name + " " + v[0]
			}
			lines = append(lines, fmt.Sprintf("  berth %s %-13s = berth %s", g.name, v[0], v[1]))
		}
		gc.Long = g.short + ".\n\nEach is also a top-level command, as ccenv spelled it:\n" + strings.Join(lines, "\n")
		gc.Example = "  " + strings.Join(respell(examples[g.verbs[0][1]], g.verbs[0][1], g.name+" "+g.verbs[0][0]), "\n  ")
		root.AddCommand(gc)
	}
	for _, c := range root.Commands() {
		if id, ok := placement[c.Name()]; ok {
			c.GroupID = id
		} else if grouped[c.Name()] != "" {
			c.Hidden = true
		}
		setExample(c, c.Name())
		for _, sub := range c.Commands() {
			setExample(sub, c.Name()+" "+sub.Name())
		}
	}
	linkDocs(root, grouped)
	root.Long += "\n\nDocs: " + docgen.Site + "/"
}

// linkDocs ends every command's help with its page on the docs site (#56). A command hidden from
// help links to its group spelling's page, which is the same command.
func linkDocs(c *cobra.Command, grouped map[string]string) {
	for _, s := range c.Commands() {
		if s.Name() == "help" {
			continue
		}
		page := s.CommandPath()
		if s.Hidden {
			g, ok := grouped[s.Name()]
			if !ok || s.Parent() != c || c.Parent() != nil {
				linkDocs(s, grouped)
				continue
			}
			page = "berth " + g
		}
		long := s.Long
		if long == "" {
			long = s.Short
		}
		s.Long = long + "\n\nDocs: " + docgen.PageURL(page)
		linkDocs(s, grouped)
	}
}

func setExample(c *cobra.Command, key string) {
	if c.Example == "" && len(examples[key]) > 0 {
		c.Example = "  " + strings.Join(examples[key], "\n  ")
	}
}

// copyAs is a copy of the top-level command c, as the verb `name` of group: the same run function,
// arguments, completion and access annotation.
func copyAs(c *cobra.Command, name, group string) *cobra.Command {
	use := name
	if _, rest, ok := strings.Cut(c.Use, " "); ok {
		use += " " + rest
	}
	return &cobra.Command{
		Use:                use,
		Short:              c.Short,
		Long:               c.Long,
		Example:            "  " + strings.Join(respell(examples[c.Name()], c.Name(), group+" "+name), "\n  "),
		Args:               c.Args,
		ValidArgs:          c.ValidArgs,
		DisableFlagParsing: c.DisableFlagParsing,
		ValidArgsFunction:  c.ValidArgsFunction,
		RunE:               c.RunE,
		Annotations:        c.Annotations,
	}
}

// respell turns an example's command word from into to (berth --output json ls → berth --output
// json org ls): its first whole-word occurrence after "berth".
func respell(lines []string, from, to string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		words := strings.Fields(l)
		for j := 1; j < len(words); j++ {
			if words[j] == from {
				words[j] = to
				break
			}
		}
		out[i] = strings.Join(words, " ")
	}
	return out
}
