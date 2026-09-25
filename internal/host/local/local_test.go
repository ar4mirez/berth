package local

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/host"
)

func inode(t *testing.T, name string) uint64 {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

func mode(t *testing.T, name string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestWriteFileInPlace(t *testing.T) {
	var fsys FS
	name := filepath.Join(t.TempDir(), "org.env")
	if err := fsys.WriteFile(name, []byte("SSH_PORT=2290\nTTYD_PORT=7790\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, name); m != 0o600 {
		t.Errorf("created with %o, want 600", m)
	}
	ino := inode(t, name)
	link := name + ".link"
	if err := os.Link(name, link); err != nil {
		t.Fatal(err)
	}
	// perm is ignored for an existing file: mode and inode stay, and the hard link sees the
	// new (shorter) content, like ccenv's `cat tmp > f`.
	if err := fsys.WriteFile(name, []byte("SSH_PORT=2291\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := inode(t, name); got != ino {
		t.Errorf("inode changed: %d -> %d", ino, got)
	}
	if m := mode(t, name); m != 0o600 {
		t.Errorf("mode changed to %o", m)
	}
	if b, _ := os.ReadFile(link); string(b) != "SSH_PORT=2291\n" {
		t.Errorf("hard link reads %q", b)
	}
}

func TestWriteFileCreateIgnoresUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	name := filepath.Join(t.TempDir(), "public")
	if err := (FS{}).WriteFile(name, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, name); m != 0o644 {
		t.Errorf("created with %o, want 644 regardless of umask", m)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	var fsys FS
	dir := t.TempDir()
	name := filepath.Join(dir, "firewall.txt")
	if err := os.WriteFile(name, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	ino := inode(t, name)
	if err := fsys.WriteFileAtomic(name, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(name); string(b) != "new" {
		t.Errorf("content %q", b)
	}
	if m := mode(t, name); m != 0o600 {
		t.Errorf("mode %o, want 600", m)
	}
	if inode(t, name) == ino {
		t.Error("atomic write should replace the file, not rewrite it")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
	// A failed write leaves nothing behind either.
	if err := fsys.WriteFileAtomic(filepath.Join(dir, "missing", "x"), []byte("y"), 0o600); err == nil {
		t.Error("writing into a missing directory should fail")
	}
}

func TestDirOps(t *testing.T) {
	var fsys FS
	dir := t.TempDir()
	orgs := filepath.Join(dir, "orgs", "acme")
	if err := fsys.MkdirAll(orgs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(filepath.Join(orgs, "org.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Chmod(filepath.Join(orgs, "org.env"), 0o640); err != nil {
		t.Fatal(err)
	}
	if fi, err := fsys.Stat(filepath.Join(orgs, "org.env")); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("stat after chmod: %v %v", fi, err)
	}
	if err := fsys.Rename(orgs, filepath.Join(dir, "orgs", "globex")); err != nil {
		t.Fatal(err)
	}
	entries, err := fsys.ReadDir(filepath.Join(dir, "orgs"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "globex" || !entries[0].IsDir() {
		t.Errorf("ReadDir: %v %v", entries, err)
	}
	if err := fsys.Remove(filepath.Join(dir, "orgs", "globex")); err == nil {
		t.Error("Remove of a non-empty directory must fail (there is no RemoveAll on purpose)")
	}
	if _, err := fsys.ReadFile(filepath.Join(dir, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}

func TestLock(t *testing.T) {
	var fsys FS
	name := filepath.Join(t.TempDir(), ".lock")
	first, err := fsys.Lock(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := fsys.Lock(ctx, name); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while held: %v", err)
	}

	got := make(chan error, 1)
	go func() {
		u, err := fsys.Lock(context.Background(), name)
		if err == nil {
			err = u.Unlock()
		}
		got <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never got the lock after Unlock")
	}
}

func TestExec(t *testing.T) {
	var ex Exec
	ctx := context.Background()
	dir := t.TempDir()

	var out bytes.Buffer
	err := ex.Run(ctx, host.Cmd{
		Args:   []string{"sh", "-c", `printf '%s|%s|' "$BERTH_T" "$PWD"; cat`},
		Env:    []string{"BERTH_T=a b$c"},
		Dir:    dir,
		Stdin:  strings.NewReader("in"),
		Stdout: &out,
	})
	if want := "a b$c|" + dir + "|in"; err != nil || out.String() != want {
		t.Errorf("got %q, %v; want %q", out.String(), err, want)
	}

	err = ex.Run(ctx, host.Cmd{Args: []string{"sh", "-c", "exit 7"}})
	var ee *host.ExitError
	if !errors.As(err, &ee) || ee.Code != 7 {
		t.Errorf("exit 7: %v", err)
	}

	err = ex.Run(ctx, host.Cmd{Args: []string{"sh", "-c", "kill -TERM $$"}})
	if !errors.As(err, &ee) || ee.Code != 128+int(syscall.SIGTERM) {
		t.Errorf("killed by SIGTERM: %v, want code %d", err, 128+int(syscall.SIGTERM))
	}

	if err := ex.Run(ctx, host.Cmd{Args: []string{"berth-no-such-binary"}}); err == nil || host.IsExit(err) {
		t.Errorf("a missing binary is a start error, not an exit code: %v", err)
	}
	if err := ex.Run(ctx, host.Cmd{}); err == nil {
		t.Error("empty argv must be an error")
	}
}

func TestExecTTYPropagatesExitCode(t *testing.T) {
	// No terminal in tests: this checks inherited stdio doesn't break exit-code propagation.
	err := Exec{}.Run(context.Background(), host.Cmd{Args: []string{"sh", "-c", "exit 3"}, TTY: true, Stdin: strings.NewReader("")})
	var ee *host.ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Errorf("got %v, want exit 3", err)
	}
}

func TestFacts(t *testing.T) {
	h := New()
	f, err := h.Facts.Facts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.UID != os.Getuid() || f.GID != os.Getgid() || f.Arch != runtime.GOARCH {
		t.Errorf("got %+v; want uid %d gid %d arch %s", f, os.Getuid(), os.Getgid(), runtime.GOARCH)
	}
}

func TestPortsInUseSeesListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	// No Docker here: facts without an engine, to test the listening-socket half.
	ports, err := host.ExecFacts{Exec: Exec{}}.PortsInUse(context.Background())
	if err != nil {
		t.Skipf("no ss or netstat on this machine: %v", err)
	}
	if !slices.Contains(ports, port) {
		t.Errorf("listening port %d not in %v", port, ports)
	}
}

func TestDockerHonoursDockerHost(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	c, err := Docker{}.DialEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	w, err := FS{}.Create(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("new file mode %o, want 600", m)
	}
	// An existing file is truncated and keeps its inode and mode, like a shell `>`.
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	ino := inode(t, p)
	w, err = FS{}.Create(p, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("x"))
	_ = w.Close()
	if b, _ := os.ReadFile(p); string(b) != "x" || mode(t, p) != 0o640 || inode(t, p) != ino {
		t.Errorf("rewrite: content %q mode %o, same inode %v", b, mode(t, p), inode(t, p) == ino)
	}
}

func TestMkdirTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	p, err := FS{}.MkdirTemp()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != tmp || !regexp.MustCompile(`^tmp\.[A-Za-z0-9]{10}$`).MatchString(filepath.Base(p)) {
		t.Errorf("MkdirTemp = %s, want %s/tmp.XXXXXXXXXX", p, tmp)
	}
	if m := mode(t, p); m != 0o700 {
		t.Errorf("mode %o, want 700", m)
	}
	if err := os.WriteFile(filepath.Join(p, "pass"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (FS{}).RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("RemoveAll left %s", p)
	}
	if err := (FS{}).RemoveAll(p); err != nil {
		t.Errorf("RemoveAll of a missing dir: %v", err)
	}
}
