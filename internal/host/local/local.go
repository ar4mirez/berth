// Package local is the host berth itself runs on. It is the only package allowed to touch org
// state with the os package directly; everything else goes through host.FS and host.Execer.
package local

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ar4mirez/berth/internal/host"
)

// New returns the local host.
func New() *host.Host {
	ex := Exec{}
	d := Docker{}
	return host.New("local", FS{}, ex, d, host.ExecFacts{Exec: ex, Docker: d}, nil)
}

// FS is the local filesystem.
type FS struct{}

func (FS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) } // #nosec G304 -- paths come from the state root

func (FS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0) // #nosec G304 -- existing file: keep inode and mode
	if errors.Is(err, fs.ErrNotExist) {
		f, err = create(name, perm)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// create makes name with exactly perm (not perm masked by the umask) before any data is written.
func create(name string, perm fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm&0o600) // #nosec G304
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (FS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".berth-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

func (FS) Chmod(name string, mode fs.FileMode) error                    { return os.Chmod(name, mode) }
func (FS) Stat(name string) (fs.FileInfo, error)                        { return os.Stat(name) }
func (FS) ReadDir(name string) ([]fs.DirEntry, error)                   { return os.ReadDir(name) }
func (FS) MkdirAll(name string, perm fs.FileMode) error                 { return os.MkdirAll(name, perm) }
func (FS) Rename(oldpath, newpath string) error                         { return os.Rename(oldpath, newpath) }
func (FS) Remove(name string) error                                     { return os.Remove(name) }
func (FS) RemoveAll(name string) error                                  { return os.RemoveAll(name) }
func (FS) Lock(ctx context.Context, name string) (host.Unlocker, error) { return lock(ctx, name) }

func (FS) Create(name string, perm fs.FileMode) (io.WriteCloser, error) {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0) // #nosec G304 -- existing file: keep its mode
	if errors.Is(err, fs.ErrNotExist) {
		return create(name, perm)
	}
	return f, err
}

func (FS) MkdirTemp() (string, error) {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	for {
		name, err := host.TempName()
		if err != nil {
			return "", err
		}
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o700); !errors.Is(err, fs.ErrExist) { // #nosec G703 -- $TMPDIR, as mktemp honours it
			return p, err
		}
	}
}

type flockUnlocker struct{ f *os.File }

func (u flockUnlocker) Unlock() error {
	return errors.Join(syscall.Flock(int(u.f.Fd()), syscall.LOCK_UN), u.f.Close())
}

// lock polls a non-blocking flock so ctx can cancel the wait. flock is released by the kernel
// when the process dies, so a crashed berth never leaves a stale lock.
func lock(ctx context.Context, name string) (host.Unlocker, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return flockUnlocker{f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, &fs.PathError{Op: "lock", Path: name, Err: err}
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, &fs.PathError{Op: "lock", Path: name, Err: ctx.Err()}
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Exec runs local processes.
type Exec struct{}

func (Exec) Run(ctx context.Context, c host.Cmd) error {
	if len(c.Args) == 0 {
		return errors.New("exec: empty command")
	}
	cmd := exec.CommandContext(ctx, c.Args[0], c.Args[1:]...) // #nosec G204 -- argv, no shell
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Dir = c.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	if c.TTY {
		if cmd.Stdin == nil {
			cmd.Stdin = os.Stdin
		}
		if cmd.Stdout == nil {
			cmd.Stdout = os.Stdout
		}
		if cmd.Stderr == nil {
			cmd.Stderr = os.Stderr
		}
		// The terminal sends ^C / ^\ to the whole foreground group. Like a shell, let the child
		// decide what they mean and keep berth alive to report its exit code.
		signal.Ignore(syscall.SIGINT, syscall.SIGQUIT)
		defer signal.Reset(syscall.SIGINT, syscall.SIGQUIT)
	}
	return exitErr(c.Args, cmd.Run())
}

// exitErr turns a non-zero exit into *host.ExitError. A signal death maps to 128+signal, the
// code a shell would report, so passthrough commands exit the way ccenv's did.
func exitErr(args []string, err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	code := ee.ExitCode()
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
	}
	return &host.ExitError{Args: args, Code: code}
}

// Docker reaches the local engine's unix socket: DOCKER_HOST if it is unix://, else the default.
type Docker struct{}

// DefaultSocket is the engine socket used when DOCKER_HOST doesn't name a unix socket.
const DefaultSocket = "/var/run/docker.sock"

func (Docker) DialEngine(ctx context.Context) (net.Conn, error) {
	sock := DefaultSocket
	if dh, ok := strings.CutPrefix(os.Getenv("DOCKER_HOST"), "unix://"); ok && dh != "" {
		sock = dh
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", sock)
}

// OpenTTY opens /dev/tty, the operator's terminal, for prompts that must not come from stdin
// (stdin may carry data, as in `restore -`).
func OpenTTY() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
