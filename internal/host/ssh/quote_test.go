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

// TestProxyCommand: the ProxyCommand for a jump host runs through a shell as ssh gives it, with
// berth's known_hosts and keys for the jump host, and %h:%p as the only %-tokens (#99).
func TestProxyCommand(t *testing.T) {
	got := ProxyCommand(Config{Addr: "bastion.example:2222", User: "jump", KnownHosts: "/home/op/.config/ber th/known_hosts", IdentityFiles: []string{"/k/100%"}})
	want := "ssh -o 'UserKnownHostsFile=/home/op/.config/ber th/known_hosts' -o GlobalKnownHostsFile=/dev/null -o StrictHostKeyChecking=yes " +
		"-o BatchMode=yes -o IdentitiesOnly=yes -i /k/100%% -p 2222 -l jump -W %h:%p -- bastion.example"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := ProxyCommand(Config{Addr: "bastion.example", User: "jump", KnownHosts: "/kh"}); !strings.Contains(got, "-p 22 -l jump") || strings.Contains(got, "IdentitiesOnly") {
		t.Errorf("default port, the agent's keys: %s", got)
	}
}
