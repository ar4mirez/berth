package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstall: berth links itself onto PATH with bash completion, as ccenv install does, and
// --alias adds a second name (berth install --alias ccenv, at cutover).
func TestInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	comp := filepath.Join(home, ".local", "share", "bash-completion", "completions")
	// A stale link where the alias goes (legacy's ccenv) is replaced, as ln -sfn does.
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/old/ccenv", filepath.Join(bin, "ccenv")); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "--home", t.TempDir(), "install", "--alias", "ccenv")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, name := range []string{"berth", "ccenv"} {
		if target, err := os.Readlink(filepath.Join(bin, name)); err != nil || target != self {
			t.Errorf("%s -> %q (%v), want %s", name, target, err, self)
		}
		b, err := os.ReadFile(filepath.Join(comp, name))
		if err != nil || !strings.Contains(string(b), "__start_berth") {
			t.Errorf("completion for %s: %v", name, err)
		}
		if !strings.Contains(out, "Linked "+filepath.Join(bin, name)+" -> "+self+"\n") ||
			!strings.Contains(out, "Completion installed: "+filepath.Join(comp, name)+" (new shells pick it up)\n") {
			t.Errorf("stdout lacks the %s lines:\n%s", name, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(comp, "ccenv")); !strings.Contains(string(b), "complete -o default -F __start_berth ccenv\n") {
		t.Error("the alias's completion isn't registered for ccenv")
	}
	if !strings.HasSuffix(out, "NOTE: "+bin+" is not on PATH. Add: export PATH=\""+bin+":$PATH\"\n") {
		t.Errorf("no PATH note:\n%s", out)
	}

	// A bin dir on PATH: no note. Under --read-only: refused.
	dir := t.TempDir()
	t.Setenv("PATH", dir+":/usr/bin")
	if out, _, code := run(t, "--home", t.TempDir(), "install", dir, "ignored"); code != 0 || strings.Contains(out, "NOTE") {
		t.Errorf("install into a PATH dir: code %d\n%s", code, out)
	}
	if _, errOut, code := run(t, "--home", t.TempDir(), "--read-only", "install"); code != 1 || !strings.Contains(errOut, "read-only") {
		t.Errorf("--read-only install: code %d, stderr %q", code, errOut)
	}
	for _, args := range [][]string{{"install", "--alias"}, {"install", "--alias", "berth"}, {"install", "--frob"}} {
		if _, _, code := run(t, append([]string{"--home", t.TempDir()}, args...)...); code != 1 {
			t.Errorf("%v: code %d, want 1", args, code)
		}
	}
}
