package parity

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const mountsFile = "state/orgs/acme/mounts.json"

// socketDir is a directory of its own, mode 0700, holding one listening Unix socket: what a daemon
// outside an org gives it (#216). The socket is real: berth checks it with the host's own stat and
// find, which the harness doesn't fake.
func socketDir(t *testing.T, name string) (dir, sock string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "bm") // short: a socket's path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock = filepath.Join(dir, name)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return dir, sock
}

// TestMountCommand: `berth mount` (#216, berth-only). A socket of berth's user, in a directory of its
// own, is saved for the org and given to its container at its next start; anything else is refused
// before anything is written; nothing restarts; and a start checks every socket again.
func TestMountCommand(t *testing.T) {
	raw := allCalls()
	dir, sock := socketDir(t, "agent.sock")
	const target = "/run/berth-mounts/agent/agent.sock"

	// add: saved, and nothing runs in docker.
	r := run(t, raw, Scenario{Args: []string{"mount", "acme", "add", "agent", sock, target}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "Mount agent: "+sock+" at "+target+". Nothing was restarted") {
		t.Fatalf("add: exit %d\nstdout %q\nstderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	saved := treeLine(r, mountsFile)
	if !strings.HasPrefix(saved, mountsFile+" 0600 ") || !strings.Contains(saved, `\"source\": \"`+sock+`\"`) || !strings.Contains(saved, `\"target\": \"`+target+`\"`) || strings.Contains(saved, "applied") {
		t.Errorf("mounts.json: %s", saved)
	}
	if n := callsMatching(r, regexp.MustCompile(`"compose"|"restart"|"rm" |"stop"`)); n != 0 {
		t.Errorf("add restarted something:\n%s", strings.Join(r.Calls, "\n"))
	}
	withMount := merge(twoOrgs, map[string]File{mountsFile: {Content: `{"mounts":[{"name":"agent","source":"` + sock + `","target":"` + target + `","mode":"rw"}]}`, Mode: 0o600}})

	// Refused, with nothing written: what isn't a socket, a link to one, a directory others share
	// or may write to, one with another socket in it or below it, and a target outside /run/berth-mounts/.
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ldir, lsock := socketDir(t, "a.sock")
	link := filepath.Join(ldir, "link.sock")
	if err := os.Symlink(lsock, link); err != nil {
		t.Fatal(err)
	}
	odir, osock := socketDir(t, "b.sock")
	if err := os.Chmod(odir, 0o777); err != nil {
		t.Fatal(err)
	}
	two, s1 := socketDir(t, "one.sock")
	if l, err := net.Listen("unix", filepath.Join(two, "two.sock")); err == nil {
		defer func() { _ = l.Close() }()
	} else {
		t.Fatal(err)
	}
	deep, s2 := socketDir(t, "top.sock")
	if err := os.Mkdir(filepath.Join(deep, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if l, err := net.Listen("unix", filepath.Join(deep, "sub", "other.sock")); err == nil {
		defer func() { _ = l.Close() }()
	} else {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, source, target, want string
	}{
		{"a file", file, "/run/berth-mounts/x/notes.txt", "not a socket"},
		{"a link", link, "/run/berth-mounts/x/link.sock", "a symbolic link, not a socket"},
		{"others may write", osock, "/run/berth-mounts/x/b.sock", "others may write to its directory"},
		{"another socket beside it", s1, "/run/berth-mounts/x/one.sock", "also holds " + filepath.Join(two, "two.sock")},
		{"another socket below it", s2, "/run/berth-mounts/x/top.sock", "also holds " + filepath.Join(deep, "sub", "other.sock")},
		{"a target outside", sock, "/home/node/agent.sock", "under /run/berth-mounts/"},
		{"the root of the mounts", sock, "/run/berth-mounts/agent.sock", "a directory of its own under /run/berth-mounts/"},
		{"another name in the container", sock, "/run/berth-mounts/x/other.sock", "keeps its name in the container"},
		{"a relative source", "agent.sock", "/run/berth-mounts/x/agent.sock", "an absolute path"},
		{"no such socket", filepath.Join(dir, "gone.sock"), "/run/berth-mounts/x/gone.sock", "no such socket"},
	} {
		r := run(t, raw, Scenario{Args: []string{"mount", "acme", "add", "x", tt.source, tt.target}, Files: twoOrgs})
		if r.Exit != 1 || !strings.Contains(r.Stderr, tt.want) || treeLine(r, mountsFile) != "" {
			t.Errorf("%s: exit %d, stderr %q, file %q", tt.name, r.Exit, r.Stderr, treeLine(r, mountsFile))
		}
	}
	// Two mounts can't share a directory in the container.
	_, other := socketDir(t, "agent.sock")
	r = run(t, raw, Scenario{Args: []string{"mount", "acme", "add", "second", other, target}, Files: withMount})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "agent already uses /run/berth-mounts/agent") {
		t.Errorf("a shared target: exit %d, stderr %q", r.Exit, r.Stderr)
	}

	// A start gives compose a second file binding the socket's directory, and the Engine API path
	// the same bind.
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: withMount, Rules: running("acme")})
	override := regexp.MustCompile(`"compose" "-f" "[^"]*/compose.yml" "-f" "<RUN>/state/berth-orgs/acme/mounts.compose.yml" "--env-file"`)
	env := regexp.MustCompile(regexp.QuoteMeta(`[BERTH_SOCKET_MOUNTS="[[\"` + dir + `\",\"/run/berth-mounts/agent\"]]"]`))
	if r.Exit != 0 || callsMatching(r, override) != 1 || callsMatching(r, env) != 1 {
		t.Errorf("restart with a mount: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}
	if y := treeLine(r, "state/berth-orgs/acme/mounts.compose.yml"); !strings.Contains(y, `source: \"`+dir+`\"`) || !strings.Contains(y, `target: \"/run/berth-mounts/agent\"`) || !strings.Contains(y, "create_host_path: false") {
		t.Errorf("mounts.compose.yml: %s", y)
	}
	// Without mounts, a start is as it was: one compose file.
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || callsMatching(r, regexp.MustCompile(`mounts.compose.yml|BERTH_SOCKET_MOUNTS`)) != 0 {
		t.Errorf("restart without mounts: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}
	// A socket that is gone stops the start before anything is stopped.
	gone := merge(twoOrgs, map[string]File{mountsFile: {Content: `{"mounts":[{"name":"agent","source":"` + filepath.Join(dir, "gone.sock") + `","target":"/run/berth-mounts/agent/gone.sock","mode":"rw"}]}`, Mode: 0o600}})
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: gone, Rules: running("acme")})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "mount agent: source") || callsMatching(r, regexp.MustCompile(`"compose"`)) != 0 {
		t.Errorf("restart with a socket gone: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}

	// ls: what is saved, and that a stopped org has none of it yet; rm takes it out.
	r = run(t, raw, Scenario{Args: []string{"--output", "json", "mount", "acme", "ls"}, Files: withMount})
	if r.Exit != 0 || !strings.Contains(r.Stdout, `"schema": "berth.mounts/v1"`) || !strings.Contains(r.Stdout, `"applied": false`) || !strings.Contains(r.Stdout, `"pending": false`) {
		t.Errorf("ls: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"mount", "acme", "rm", "agent"}, Files: withMount})
	if r.Exit != 0 || treeLine(r, mountsFile) != "" || !strings.HasPrefix(r.Stdout, "Removed mount agent.") {
		t.Errorf("rm: exit %d, stdout %q, file %q", r.Exit, r.Stdout, treeLine(r, mountsFile))
	}
	r = run(t, raw, Scenario{Args: []string{"mount", "acme", "rm", "nope"}, Files: withMount})
	if r.Exit != 1 || !strings.Contains(r.Stderr, `acme has no mount "nope"`) {
		t.Errorf("rm, unknown: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	r = run(t, raw, Scenario{Args: []string{"--read-only", "mount", "acme", "add", "agent", sock, target}, Files: twoOrgs})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "read-only") || treeLine(r, mountsFile) != "" {
		t.Errorf("--read-only: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}
