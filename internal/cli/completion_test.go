package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionFromStateRoot(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"acme", "globex", ".restore-Ab12Cd", "t-noenv"} {
		if err := os.MkdirAll(filepath.Join(home, "orgs", d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"acme", "globex", ".restore-Ab12Cd"} {
		if err := os.WriteFile(filepath.Join(home, "orgs", d, "org.env"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		args []string
		want string // candidates, space-separated
	}{
		{[]string{"info", ""}, "acme globex"},
		{[]string{"logs", ""}, "acme globex"},
		{[]string{"whoami", "acme", ""}, "globex"},
		{[]string{"fw", ""}, "acme globex"},
		{[]string{"fw", "acme", ""}, "show allow deny on off edit reload presets test"},
		{[]string{"fw", "acme", "allow", "@go", ""}, "@mise @python @node @go @rust @ruby @docker @gitlab @bitbucket @aws @gcp @azure @debian"},
		{[]string{"repo", ""}, "add new publish ls rm adopt sync audit policy"},
		{[]string{"clone", ""}, "acme globex"},
		{[]string{"schedule", ""}, "status run off --at --keep -o"},
		{[]string{"backup", ""}, "acme globex --all"},
		{[]string{"repo", "ls", ""}, "acme globex"},
		{[]string{"ls", ""}, ""},
	}
	for _, tt := range tests {
		out, errOut, code := run(t, append([]string{"--home", home, "__complete"}, tt.args...)...)
		var got []string
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" && !strings.HasPrefix(l, ":") {
				got = append(got, l)
			}
		}
		if code != 0 || strings.Join(got, " ") != tt.want {
			t.Errorf("%v: got %q (code %d, stderr %q), want %q", tt.args, got, code, errOut, tt.want)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for shell, marker := range map[string]string{"": "bash completion V2 for berth", "bash": "bash completion V2 for berth",
		"zsh": "#compdef berth", "fish": "complete -c berth", "powershell": "Register-ArgumentCompleter"} {
		args := []string{"completion"}
		if shell != "" {
			args = append(args, shell)
		}
		out, errOut, code := run(t, args...)
		if code != 0 || !strings.Contains(out, marker) {
			t.Errorf("completion %s: code %d, stderr %q, output lacks %q", shell, code, errOut, marker)
		}
	}
	if _, errOut, code := run(t, "completion", "tcsh"); code != 1 || !strings.Contains(errOut, "unsupported shell") {
		t.Errorf("tcsh: code %d, stderr %q", code, errOut)
	}
}
