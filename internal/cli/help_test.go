package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestHelpSections: help shows the sections and the noun groups, and hides the top-level commands
// a group now covers (#55).
func TestHelpSections(t *testing.T) {
	var out bytes.Buffer
	if code := Execute([]string{"--help"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Orgs (", "Inside an org:", "Sign-in:", "Backups:", "Hosts:", "berth itself:", "  org ", "  account ", "  system ", "  use "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
	for _, hidden := range []string{"  init ", "  token ", "  gh-login ", "  whoami ", "  install ", "  parity-check "} {
		if strings.Contains(out.String(), hidden) {
			t.Errorf("help lists %q, which lives in a group now", hidden)
		}
	}
}

// TestEveryCommandHasAnExample: every command, hidden or not, top-level or in a group.
func TestEveryCommandHasAnExample(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" {
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
	root := NewRoot()
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
	if code, out, _ := run("use"); code != 0 || !strings.Contains(out, "No default org") {
		t.Fatalf("use, none: %d %s", code, out)
	}
	if code, _, errs := run("use", "nope"); code == 0 || !strings.Contains(errs, "unknown org") {
		t.Errorf("use of an unknown org: %d %s", code, errs)
	}
	if code, _, errs := run("--read-only", "use", "acme"); code == 0 || !strings.Contains(errs, "read-only") {
		t.Errorf("use under --read-only: %d %s", code, errs)
	}
	if code, out, errs := run("use", "acme"); code != 0 || !strings.Contains(out, "Default org: acme") {
		t.Fatalf("use acme: %d %s %s", code, out, errs)
	}

	// Left out: the default goes in, and says so.
	_, out, errs := run("fw", "show")
	if !strings.Contains(out, "acme.example") || !strings.Contains(errs, "(acme, the default org") {
		t.Errorf("fw show with the default: %q %q", out, errs)
	}
	// Named: the named org, no note.
	_, out, errs = run("fw", "globex", "show")
	if !strings.Contains(out, "globex.example") || strings.Contains(errs, "default org") {
		t.Errorf("fw globex show: %q %q", out, errs)
	}
	// A mistyped org is an error, never replaced by the default.
	if code, _, errs := run("fw", "acmee", "show"); code == 0 || !strings.Contains(errs, "unknown org 'acmee'") {
		t.Errorf("fw acmee show: %d %q", code, errs)
	}
	// Group verbs too.
	if _, _, errs := run("org", "info"); !strings.Contains(errs, "(acme, the default org") {
		t.Errorf("org info with the default: %q", errs)
	}
	if code, out, _ := run("use", "--clear"); code != 0 || !strings.Contains(out, "No default org now") {
		t.Errorf("use --clear: %d %s", code, out)
	}
	if code, _, errs := run("fw", "show"); code == 0 || strings.Contains(errs, "default org") {
		t.Errorf("fw show without a default: %d %q", code, errs)
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
	var paths [][]string
	var walk func(c *cobra.Command, at []string)
	walk = func(c *cobra.Command, at []string) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" {
				continue
			}
			p := append(append([]string{}, at...), sub.Name())
			paths = append(paths, p)
			walk(sub, p)
		}
	}
	walk(NewRoot(), nil)
	if len(paths) < 80 {
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
	for _, args := range [][]string{{"fw", "acme", "--help"}, {"repo", "add", "acme", "-h"}, {"backup", "--all", "--help"}, {"destroy", "acme", "--yes", "--help"}} {
		var out, errOut bytes.Buffer
		if code := Execute(args, strings.NewReader(""), &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Errorf("berth %s: exit %d, %q", strings.Join(args, " "), code, errOut.String())
		}
	}
	// What follows the org of claude, run and exec is theirs, and so are build's arguments after the first.
	for _, args := range [][]string{{"claude", "acme", "--help"}, {"exec", "acme", "--", "ls", "-h"}, {"run", "acme", "-h"}, {"repo", "ls", "acme", "--", "--help"}} {
		var out, errOut bytes.Buffer
		if Execute(args, strings.NewReader(""), &out, &errOut); strings.Contains(out.String(), "Usage:") {
			t.Errorf("berth %s printed berth's help", strings.Join(args, " "))
		}
	}
	if _, err := os.Stat(filepath.Join(home, "orgs")); err == nil {
		t.Error("something was created in the state root")
	}
}
