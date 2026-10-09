package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/ops"
)

// TestHelpLayout: help lists the everyday commands, then the groups, each once, in that order
// (#140); and ccenv's command line, for the ccenv alias, is as it was (#55).
func TestHelpLayout(t *testing.T) {
	out, _, code := run(t, "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	last := -1
	for _, want := range []string{"Everyday:", "\n  ls ", "\n  up ", "\n  down ", "\n  restart ", "\n  shell ", "\n  claude ", "\n  attach ", "\n  logs ", "\n  info ",
		"Commands:", "\n  org ", "\n  repo ", "\n  fw ", "\n  env ", "\n  pkg ", "\n  account ", "\n  backup ", "\n  host ", "\n  system ", "\n  tui ", "\n  ui ", "\n  mcp ", "\n  serve ", "\n  help "} {
		i := strings.Index(out, want)
		if i < 0 || i < last || strings.Count(out, want) != 1 {
			t.Errorf("help: %q is missing, repeated or out of order:\n%s", want, out)
		}
		last = i
	}
	for _, gone := range []string{"  init ", "  token ", "  gh-login ", "  whoami ", "  install ", "  restore ", "  schedule ", "  use ", "  clone ", "Additional"} {
		if strings.Contains(out, gone) {
			t.Errorf("help lists %q", gone)
		}
	}
	t.Setenv(spellingsEnv, "ccenv")
	out, _, _ = run(t, "--help")
	for _, want := range []string{"Orgs (", "Inside an org:", "Sign-in:", "Backups:", "Hosts:", "berth itself:", "  org ", "  account ", "  system ", "  use "} {
		if !strings.Contains(out, want) {
			t.Errorf("ccenv's help lacks %q:\n%s", want, out)
		}
	}
	if out, _, code := run(t, "init", "--help"); code != 0 || !strings.Contains(out, "berth init <org>") {
		t.Errorf("ccenv's spellings: init --help: %d %s", code, out)
	}
}

// commands is every command of the tree, as its words after berth; gone tells the removed
// spellings from the rest.
func commands(root *cobra.Command) (live, gone [][]string) {
	var walk func(c *cobra.Command, at []string)
	walk = func(c *cobra.Command, at []string) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" {
				continue
			}
			p := append(append([]string{}, at...), sub.Name())
			if sub.Annotations[goneKey] != "" {
				gone = append(gone, p)
				continue
			}
			live = append(live, p)
			walk(sub, p)
		}
	}
	walk(root, nil)
	return live, gone
}

// TestOneSpellingPerCommand: every operation of the catalog is reachable under one name that help
// shows; the only hidden commands are the ones something other than a person runs; and a removed
// spelling names its replacement and runs nothing.
func TestOneSpellingPerCommand(t *testing.T) {
	root := NewRoot()
	live, gone := commands(root)
	hidden := map[string]bool{}
	for _, p := range live {
		c, _, err := root.Find(p)
		if err != nil {
			t.Fatal(err)
		}
		if c.Hidden {
			hidden[strings.Join(p, " ")] = true
		}
	}
	for _, keep := range []string{"install", "restore", "schedule", "completion", "image-tag"} {
		if !hidden[keep] {
			t.Errorf("%s should work, hidden: a timer, a host or a start-up file runs it", keep)
		}
		delete(hidden, keep)
	}
	if len(hidden) != 0 {
		t.Errorf("hidden commands: %v", hidden)
	}
	home := t.TempDir()
	t.Setenv("BERTH_HOME", home)
	t.Setenv("PATH", t.TempDir())
	if len(gone) < 20 {
		t.Fatalf("only %d removed spellings", len(gone))
	}
	for _, p := range gone {
		c, _, _ := root.Find(p)
		now := c.Annotations[goneKey]
		if n, _, err := root.Find(strings.Fields(now)); err != nil || n.Hidden || n.CommandPath() != "berth "+now {
			t.Errorf("%s points at %q, which isn't a command help shows", p[0], now)
		}
		for _, args := range [][]string{p, append(append([]string{}, p...), "acme"), append(append([]string{}, p...), "--help")} {
			out, errOut, code := run(t, args...)
			if code != 1 || out != "" || !strings.Contains(errOut, "berth: '"+p[0]+"' is now 'berth "+now+"' (berth "+now+" --help)") {
				t.Errorf("berth %v: exit %d, %q %q", args, code, out, errOut)
			}
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Error("a removed spelling touched the state root")
	}
}

// TestVerbFirst: `berth fw allow acme x` is the operation `fw acme allow x`; with the org left out
// it takes the default one, and without one it asks for it; the org-first order still works for
// fw, env and pkg.
func TestVerbFirst(t *testing.T) {
	for _, tc := range []struct {
		args []string
		op   string // name, sub, and the arguments in the flat command's order
	}{
		{[]string{"fw", "allow", "acme", "pypi.org", "@python"}, "fw allow [acme allow pypi.org @python]"},
		{[]string{"fw", "allow", "pypi.org"}, "fw allow [allow pypi.org]"},
		{[]string{"fw", "show"}, "fw show [show]"},
		{[]string{"fw", "show", "acme@box1"}, "fw show [acme@box1 show]"},
		{[]string{"fw", "test", "acme", "pypi.org"}, "fw test [acme test pypi.org]"},
		{[]string{"fw", "acme", "allow", "pypi.org"}, "fw allow [acme allow pypi.org]"},
		{[]string{"env", "set", "acme", "API_KEY", "--no-restart"}, "env set [acme set API_KEY --no-restart]"},
		{[]string{"env", "set", "API_KEY"}, "env set [set API_KEY]"},
		{[]string{"env", "unset", "key", "--no-restart"}, "env unset [unset key --no-restart]"},
		{[]string{"env", "accept", "acme", "--list"}, "env accept [acme accept --list]"},
		{[]string{"env", "accept", "API_KEY"}, "env accept [accept API_KEY]"},
		{[]string{"env", "migrate", "acme"}, "secrets migrate [acme]"},
		{[]string{"pkg", "add", "acme", "libpq-dev"}, "pkg add [acme add libpq-dev]"},
		{[]string{"pkg", "acme", "add", "libpq-dev"}, "pkg add [acme add libpq-dev]"},
		{[]string{"pkg", "ls"}, "pkg ls [ls]"},
		{[]string{"repo", "add", "acme", "acme/widgets"}, "repo add [add acme acme/widgets]"},
		{[]string{"repo", "policy", "acme"}, "repo policy/ [policy acme]"},
		{[]string{"repo", "policy", "acme", "warn"}, "repo policy/<mode> [policy acme warn]"},
		{[]string{"org", "password", "rotate", "acme"}, "password rotate [acme rotate]"},
		{[]string{"org", "password", "show"}, "password show [show]"},
		{[]string{"account", "remote", "logs", "acme"}, "remote logs [acme logs]"},
		{[]string{"backup", "schedule", "on", "--at", "03:30"}, "schedule  [--at 03:30]"},
		{[]string{"backup", "schedule", "status", "--host", "box1"}, "schedule status [status --host box1]"},
		{[]string{"backup", "create", "--all"}, "backup  [--all]"},
		{[]string{"backup", "--all", "--keep", "14"}, "backup  [--all --keep 14]"},
		{[]string{"org", "create", "acme"}, "init  [acme]"},
		{[]string{"account", "gh", "acme"}, "gh-login  [acme]"},
		{[]string{"org", "use", "acme"}, "use <org> [acme]"},
		{[]string{"up", "acme"}, "up  [acme]"},
		{[]string{"host", "ls"}, "host ls [ls]"},
		{[]string{"host", "guard", "box1", "status"}, "host guard/status [guard box1 status]"},
		{[]string{"host", "guard", "box1"}, "host guard/ [guard box1]"},
		{[]string{"host", "guard", "box1", "off"}, "host guard/off [guard box1 off]"},
	} {
		root := NewRoot()
		c, rest, err := root.Find(tc.args)
		if err != nil {
			t.Errorf("%v: %v", tc.args, err)
			continue
		}
		name, sub, fargs := opOf(c, rest)
		if got := fmt.Sprintf("%s %s %v", name, sub, fargs); got != tc.op {
			t.Errorf("berth %s: the operation is %q, want %q", strings.Join(tc.args, " "), got, tc.op)
		}
		if _, ok := ops.Lookup(name, sub); !ok {
			t.Errorf("berth %s: no operation %s %s in the catalog", strings.Join(tc.args, " "), name, sub)
		}
	}
}

// TestEveryCommandHasAnExample: every command, hidden or not, top-level or in a group.
func TestEveryCommandHasAnExample(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Annotations[goneKey] != "" {
				continue
			}
			if strings.TrimSpace(sub.Example) == "" {
				t.Errorf("%s has no example", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(NewRoot())
}

// TestGroupVerbsAreTheirCommands: each group verb is the top-level command under another name, with
// the same access and argument handling (the parity suite covers the top-level spellings).
func TestGroupVerbsAreTheirCommands(t *testing.T) {
	root := newRoot(true) // ccenv's command line
	for _, g := range nounGroups {
		gc, _, err := root.Find([]string{g.name})
		if err != nil || gc.Name() != g.name {
			t.Fatalf("no group %s", g.name)
		}
		for _, v := range g.verbs {
			sub, _, err := root.Find([]string{g.name, v[0]})
			top, _, err2 := root.Find([]string{v[1]})
			if err != nil || err2 != nil || sub.Name() != v[0] {
				t.Errorf("%s %s: %v %v", g.name, v[0], err, err2)
				continue
			}
			if sub.Annotations[accessKey] != top.Annotations[accessKey] || sub.DisableFlagParsing != top.DisableFlagParsing || sub.RunE == nil {
				t.Errorf("%s %s differs from %s", g.name, v[0], v[1])
			}
			if !strings.Contains(sub.Example, "berth "+g.name+" "+v[0]) && !strings.Contains(sub.Example, g.name+" "+v[0]) {
				t.Errorf("%s %s: its examples aren't respelled: %s", g.name, v[0], sub.Example)
			}
		}
	}
}

// TestDefaultOrg: berth use saves a default org; commands take it only when the org is left out.
func TestDefaultOrg(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("BERTH_HOME", state)
	for _, o := range []string{"acme", "globex"} {
		if err := os.MkdirAll(filepath.Join(state, "orgs", o, "config"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "orgs", o, "org.env"), []byte("MANAGER=berth\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "orgs", o, "config", "firewall.txt"), []byte("# "+o+"\n"+o+".example\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Execute(args, nil, &out, &errb)
		return code, out.String(), errb.String()
	}
	if code, out, _ := run("org", "use"); code != 0 || !strings.Contains(out, "No default org") {
		t.Fatalf("use, none: %d %s", code, out)
	}
	if code, _, errs := run("org", "use", "nope"); code == 0 || !strings.Contains(errs, "unknown org") {
		t.Errorf("use of an unknown org: %d %s", code, errs)
	}
	if code, _, errs := run("--read-only", "org", "use", "acme"); code == 0 || !strings.Contains(errs, "read-only") {
		t.Errorf("use under --read-only: %d %s", code, errs)
	}
	if code, out, errs := run("org", "use", "acme"); code != 0 || !strings.Contains(out, "Default org: acme") {
		t.Fatalf("use acme: %d %s %s", code, out, errs)
	}

	// Left out: the default goes in, and says so.
	_, out, errs := run("fw", "show")
	if !strings.Contains(out, "acme.example") || !strings.Contains(errs, "(acme, the default org") {
		t.Errorf("fw show with the default: %q %q", out, errs)
	}
	// Named: the named org, no note.
	_, out, errs = run("fw", "show", "globex")
	if !strings.Contains(out, "globex.example") || strings.Contains(errs, "default org") {
		t.Errorf("fw globex show: %q %q", out, errs)
	}
	// A mistyped org is an error, never replaced by the default.
	if code, _, errs := run("fw", "show", "acmee"); code == 0 || !strings.Contains(errs, "unknown org 'acmee'") {
		t.Errorf("fw acmee show: %d %q", code, errs)
	}
	// Group verbs too.
	if _, _, errs := run("org", "info"); !strings.Contains(errs, "(acme, the default org") {
		t.Errorf("org info with the default: %q", errs)
	}
	if code, out, _ := run("org", "use", "--clear"); code != 0 || !strings.Contains(out, "No default org now") {
		t.Errorf("use --clear: %d %s", code, out)
	}
	// Without one, the command asks for the org, and so does the org-first order.
	if code, _, errs := run("fw", "show"); code != 1 || !strings.Contains(errs, "fw show: which org? (berth fw show <org>; or set a default org: berth org use <org>)") {
		t.Errorf("fw show without a default: %d %q", code, errs)
	}
	if _, out, errs := run("fw", "globex", "show"); !strings.Contains(out, "globex.example") || strings.Contains(errs, "default org") {
		t.Errorf("fw globex show, ccenv's order: %q %q", out, errs)
	}
}

// TestHelpNeverRunsTheCommand: every command answers --help and -h with its help and does nothing
// else. The commands that parse their own arguments, as ccenv's do, used to take --help for an
// argument: `berth restart --help` restarted the default org, `berth logout --help` logged it out.
func TestHelpNeverRunsTheCommand(t *testing.T) {
	home, cfg := t.TempDir(), t.TempDir()
	t.Setenv("BERTH_HOME", home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("PATH", t.TempDir()) // no docker, no ssh: a command that ran would fail, not act
	// A default org, as `berth use` leaves it: what made --help dangerous.
	if err := os.MkdirAll(filepath.Join(cfg, "berth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "berth", "context"), []byte("acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, _ := commands(NewRoot())
	if len(paths) < 90 {
		t.Fatalf("only %d commands found", len(paths))
	}
	for _, p := range paths {
		for _, flag := range []string{"--help", "-h"} {
			for _, global := range [][]string{nil, {"--read-only"}} {
				args := append(append(append([]string{}, global...), p...), flag)
				var out, errOut bytes.Buffer
				code := Execute(args, strings.NewReader(""), &out, &errOut)
				if code != 0 || !strings.Contains(out.String(), "Usage:\n  berth "+strings.Join(p, " ")) || errOut.Len() != 0 {
					t.Errorf("berth %s: exit %d, stderr %q, stdout:\n%.200s", strings.Join(args, " "), code, errOut.String(), out.String())
				}
			}
		}
	}
	// With an org before it, too, and not past `--`.
	for _, args := range [][]string{{"fw", "acme", "--help"}, {"repo", "add", "acme", "-h"}, {"backup", "--all", "--help"}, {"org", "destroy", "acme", "--yes", "--help"}, {"fw", "allow", "acme", "--help"}, {"backup", "schedule", "on", "--help"}} {
		var out, errOut bytes.Buffer
		if code := Execute(args, strings.NewReader(""), &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Errorf("berth %s: exit %d, %q", strings.Join(args, " "), code, errOut.String())
		}
	}
	// What follows the org of claude, run and exec is theirs, and so are build's arguments after the first.
	for _, args := range [][]string{{"claude", "acme", "--help"}, {"org", "exec", "acme", "--", "ls", "-h"}, {"org", "run", "acme", "-h"}, {"repo", "ls", "acme", "--", "--help"}} {
		var out, errOut bytes.Buffer
		if Execute(args, strings.NewReader(""), &out, &errOut); strings.Contains(out.String(), "Usage:") {
			t.Errorf("berth %s printed berth's help", strings.Join(args, " "))
		}
	}
	if _, err := os.Stat(filepath.Join(home, "orgs")); err == nil {
		t.Error("something was created in the state root")
	}
}

// TestMessagesNameBerthsCommands: what berth prints names commands as this command line spells
// them (ops.Respell): errors, their JSON form, hints and the files a new org starts with; and with
// ccenv's spellings they stay ccenv's.
func TestMessagesNameBerthsCommands(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("BERTH_HOME", state)
	t.Setenv("PATH", t.TempDir())
	if err := os.MkdirAll(filepath.Join(state, "orgs", "acme", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "orgs", "acme", "org.env"), []byte("MANAGER=berth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string // in stdout or stderr
	}{
		{[]string{"fw", "show", "nope"}, "unknown org 'nope' (run: berth org create nope)"},
		{[]string{"--output", "json", "info", "nope"}, `"hint": "run: berth org create nope"`},
		{[]string{"env", "ls", "acme"}, "add one: berth env set acme KEY"},
		{[]string{"fw", "allow", "acme"}, "usage: berth fw allow acme <domain|ip|cidr|@preset>..."},
		{[]string{"fw", "acme", "frob"}, "usage: berth fw show|allow|deny|on|off|edit|reload|presets|test <org>"},
		{[]string{"org", "use"}, "Set one: berth org use <org>[@host]"},
		{[]string{"backup", "schedule", "status"}, "Set one with: berth backup schedule on"},
		{[]string{"org", "destroy", "acme", "--frob"}, "usage: berth org destroy <org> [--yes] [--keep-backups]"},
	} {
		out, errOut, _ := run(t, tc.args...)
		if !strings.Contains(out+errOut, tc.want) {
			t.Errorf("berth %s: no %q in %q %q", strings.Join(tc.args, " "), tc.want, out, errOut)
		}
	}
	t.Setenv(spellingsEnv, "ccenv")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"fw", "nope", "show"}, "unknown org 'nope' (run: berth init nope)"},
		{[]string{"env", "acme", "ls"}, "add one: berth env acme set KEY"},
		{[]string{"use"}, "Set one: berth use <org>[@host]"},
	} {
		out, errOut, _ := run(t, tc.args...)
		if !strings.Contains(out+errOut, tc.want) {
			t.Errorf("ccenv's spellings, berth %s: no %q in %q %q", strings.Join(tc.args, " "), tc.want, out, errOut)
		}
	}
}
