package ssh

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
)

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":             "''",
		"plain":        "plain",
		"/a/b-c_d.e":   "/a/b-c_d.e",
		"K=V":          "K=V",
		"two words":    "'two words'",
		"it's":         `'it'\''s'`,
		"$HOME":        "'$HOME'",
		"a;rm -rf /":   "'a;rm -rf /'",
		"`id`":         "'`id`'",
		"line\nbreak":  "'line\nbreak'",
		"glob*":        "'glob*'",
		"--flag=x y":   "'--flag=x y'",
		"ünïcødé":      "'ünïcødé'",
		`back\slash"q`: `'back\slash"q'`,
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestRemoteCommandRoundTrip runs the generated line through a real sh (as sshd would, via the
// user's shell) and checks every argv element, env value and the dir arrive byte for byte.
func TestRemoteCommandRoundTrip(t *testing.T) {
	dir := t.TempDir() + "/with space"
	if err := (local.FS{}).MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"sh", "-c", `printf '%s\n' "$PWD" "$T1" "$T2" "$@"`, "argv0",
		"", "it's", "$HOME", "a;b", "`id`", "*", "multi\nline", "-n", "ünï"}
	line, err := remoteCommand(host.Cmd{Args: args, Dir: dir, Env: []string{"T1=a b$c", "T2='q'"}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := (local.Exec{}).Run(context.Background(), host.Cmd{Args: []string{"sh", "-c", line}, Stdout: &out}); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	want := strings.Join(append([]string{dir, "a b$c", "'q'"}, args[4:]...), "\n") + "\n"
	if out.String() != want {
		t.Errorf("line %s\ngot  %q\nwant %q", line, out.String(), want)
	}
}

func TestRemoteCommandRejects(t *testing.T) {
	for name, c := range map[string]host.Cmd{
		"empty argv":       {},
		"relative dir":     {Args: []string{"true"}, Dir: "work"},
		"env without name": {Args: []string{"true"}, Env: []string{"rm -rf /"}},
		"env bad name":     {Args: []string{"true"}, Env: []string{"1X=y"}},
	} {
		if line, err := remoteCommand(c); err == nil {
			t.Errorf("%s: accepted as %q", name, line)
		}
	}
}
