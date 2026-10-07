package image

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretDrop runs image/berth-secret-drop (#10): a value read from stdin is left in a 0600
// file that only --show prints; names and values env set would refuse are refused here too; and
// nothing that isn't a regular file is ever listed or shown.
func TestSecretDrop(t *testing.T) {
	script, err := filepath.Abs(filepath.Join(dir, "berth-secret-drop"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	drops := filepath.Join(home, ".config", "berth-drop")
	run := func(stdin string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "BERTH_DROP_DIR=")
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return out.String(), errb.String(), code
	}

	out, errOut, code := run("sk-or-SECRET value\nsecond line\n", "OPENROUTER_API_KEY")
	if code != 0 || !strings.Contains(out, "Dropped OPENROUTER_API_KEY. It isn't set yet: on the host, run  berth env ") || strings.Contains(out+errOut, "sk-or-SECRET") {
		t.Fatalf("drop: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	fi, err := os.Stat(filepath.Join(drops, "OPENROUTER_API_KEY"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the drop: %v, mode %v", err, fi)
	}
	if di, _ := os.Stat(drops); di == nil || di.Mode().Perm() != 0o700 {
		t.Errorf("the drop folder's mode: %v", di)
	}
	// One line is the value; --show prints exactly it.
	if out, _, code = run("", "--show", "OPENROUTER_API_KEY"); code != 0 || out != "sk-or-SECRET value" {
		t.Errorf("--show: code %d, %q", code, out)
	}
	if out, _, code = run("", "--list"); code != 0 || out != "OPENROUTER_API_KEY\n" {
		t.Errorf("--list: code %d, %q", code, out)
	}

	// Refused: a bad name, a value on the command line, an empty value, a single quote.
	for _, c := range []struct {
		stdin string
		args  []string
		want  string
	}{
		{"v\n", []string{"lower_case"}, "KEY must look like"},
		{"v\n", []string{"1KEY"}, "KEY must look like"},
		{"v\n", []string{"../ESCAPE"}, "KEY must look like"},
		{"", []string{"KEY", "the-value"}, "never from the command line"},
		{"\n", []string{"EMPTY"}, "empty value; nothing dropped"},
		{"it's\n", []string{"QUOTE"}, "single quote"},
		{"", []string{"--show", "NOPE"}, "nothing dropped for NOPE"},
		{"", []string{"--show", "../../etc/passwd"}, "KEY must look like"},
		{"", []string{"--bogus"}, "unknown option"},
	} {
		if _, errOut, code := run(c.stdin, c.args...); code != 1 || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: code %d, stderr %q (want %q)", c.args, code, errOut, c.want)
		}
	}

	// A link in the drop folder is not a drop: not listed, not shown, whatever it points at.
	secret := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(secret, []byte("NOT FOR THE ORG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(drops, "LINKED")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drops, "not-a-key"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, _ = run("", "--list"); out != "OPENROUTER_API_KEY\n" {
		t.Errorf("--list with a link and a bad name: %q", out)
	}
	if out, errOut, code = run("", "--show", "LINKED"); code != 1 || out != "" || !strings.Contains(errOut, "nothing dropped for LINKED") {
		t.Errorf("--show of a link: code %d, stdout %q, stderr %q", code, out, errOut)
	}

	// --cancel takes a drop (or a link) back.
	for _, k := range []string{"OPENROUTER_API_KEY", "LINKED"} {
		if out, _, code = run("", "--cancel", k); code != 0 || out != "Cancelled: "+k+"\n" {
			t.Errorf("--cancel %s: code %d, %q", k, code, out)
		}
	}
	if out, _, _ = run("", "--list"); out != "" {
		t.Errorf("--list after cancelling: %q", out)
	}
	if b, _ := os.ReadFile(secret); string(b) != "NOT FOR THE ORG" {
		t.Error("cancelling a link removed what it pointed at")
	}
}
