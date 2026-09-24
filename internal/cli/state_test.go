package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/config"
)

// testRoot is the real root plus a reading and a writing command that record what they saw.
func testRoot(ran *[]string, seen *config.State) *cobra.Command {
	root := NewRoot()
	record := func(cmd *cobra.Command, _ []string) error {
		*ran = append(*ran, cmd.Name())
		*seen = stateFrom(cmd.Context())
		return nil
	}
	root.AddCommand(
		reads(&cobra.Command{Use: "peek", RunE: record}),
		writes(&cobra.Command{Use: "touch", RunE: record}),
		&cobra.Command{Use: "undeclared", RunE: record},
	)
	return root
}

func runRoot(root *cobra.Command, args ...string) (stdout, stderr string, code int) {
	var out, errb bytes.Buffer
	code = execute(root, args, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func TestReadOnlyRefusesWriters(t *testing.T) {
	var ran []string
	var seen config.State
	home := t.TempDir()

	_, errOut, code := runRoot(testRoot(&ran, &seen), "--home", home, "--read-only", "touch")
	if code != 1 || errOut != "berth: read-only mode (--read-only): refusing to run touch\n" {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
	if len(ran) != 0 {
		t.Errorf("a writer ran under --read-only: %v", ran)
	}

	// The flag is global: it works after the subcommand too.
	if _, _, code := runRoot(testRoot(&ran, &seen), "touch", "--read-only", "--home", home); code != 1 || len(ran) != 0 {
		t.Errorf("trailing --read-only: code=%d ran=%v", code, ran)
	}
}

func TestReadOnlyAllowsReaders(t *testing.T) {
	var ran []string
	var seen config.State
	home := t.TempDir()

	if _, errOut, code := runRoot(testRoot(&ran, &seen), "--read-only", "--home", home, "peek"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	want := config.State{Home: config.Home{Path: home, Source: config.SourceFlag}, ReadOnly: true}
	if len(ran) != 1 || seen != want {
		t.Errorf("ran=%v state=%+v, want %+v", ran, seen, want)
	}
}

func TestWritersRunWithoutReadOnly(t *testing.T) {
	var ran []string
	var seen config.State
	home := t.TempDir()

	if _, errOut, code := runRoot(testRoot(&ran, &seen), "--home", home, "touch"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if len(ran) != 1 || seen.ReadOnly || seen.Home.Path != home {
		t.Errorf("ran=%v state=%+v", ran, seen)
	}
}

func TestUndeclaredCommandRefused(t *testing.T) {
	var ran []string
	var seen config.State
	_, errOut, code := runRoot(testRoot(&ran, &seen), "--home", t.TempDir(), "undeclared")
	if code != 1 || !strings.Contains(errOut, "declares no access") || len(ran) != 0 {
		t.Errorf("code=%d stderr=%q ran=%v", code, errOut, ran)
	}
}

func TestHelpNeedsNoState(t *testing.T) {
	// A broken config must not break help: help doesn't resolve the state root.
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("BERTH_HOME", "")
	writeBrokenConfig(t, user)
	if out, errOut, code := runRoot(NewRoot()); code != 0 || !strings.Contains(out, "--read-only") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
}

// TestEveryCommandDeclaresAccess keeps the --read-only guard complete: each runnable command in
// the real tree must say whether it reads or writes, and none may replace the root's pre-run hook.
func TestEveryCommandDeclaresAccess(t *testing.T) {
	root := NewRoot()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" {
				continue
			}
			if sub.Runnable() {
				switch sub.Annotations[accessKey] {
				case accessRead, accessWrite:
				default:
					t.Errorf("%s: wrap it in reads() or writes()", sub.CommandPath())
				}
			}
			if sub.PersistentPreRun != nil || sub.PersistentPreRunE != nil {
				t.Errorf("%s: defines PersistentPreRun, which would bypass the root's --read-only hook", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(root)
}

func writeBrokenConfig(t *testing.T, user string) {
	t.Helper()
	dir := filepath.Join(user, ".config", "berth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("home: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
