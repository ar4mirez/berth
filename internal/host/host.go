// Package host is the machine an org runs on: its files, its processes, its Docker engine, and a
// few facts about it. Every command writes host files (org.env, firewall.txt, repos.txt, secrets)
// and bind mounts resolve on the machine that runs the container, so a remote host needs remote
// file access, not just a Docker endpoint (docs/plan.md, decision 2).
//
// The interfaces work at the level of operations, not file handles or sessions, so a future
// berthd agent can implement them without a rewrite. Implementations: host/local and host/ssh.
//
// All I/O on org state goes through here. golangci-lint (forbidigo) rejects direct os file calls
// outside internal/host/local.
package host

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
)

// Host bundles the four capabilities. Close releases whatever the implementation holds
// (an SSH connection, for example); it is safe to call on a local host.
type Host struct {
	// Name identifies the host in messages: "local", or "ssh://user@example:22".
	Name   string
	FS     FS
	Exec   Execer
	Docker Docker
	Facts  Facter
	close  func() error
}

// New assembles a Host. closeFn may be nil.
func New(name string, fsys FS, ex Execer, d Docker, f Facter, closeFn func() error) *Host {
	return &Host{Name: name, FS: fsys, Exec: ex, Docker: d, Facts: f, close: closeFn}
}

// Close releases the host's resources.
func (h *Host) Close() error {
	if h.close == nil {
		return nil
	}
	return h.close()
}

// FS is file access on the host. Paths are absolute paths on that host.
type FS interface {
	ReadFile(name string) ([]byte, error)
	// WriteFile rewrites name in place: an existing file keeps its inode and mode (ccenv's
	// `cat tmp > f`), so hard links and open handles see the change. perm applies only when
	// the file is created, and is set before any data is written.
	WriteFile(name string, data []byte, perm fs.FileMode) error
	// WriteFileAtomic writes a temp file in the same directory and renames it over name, so
	// readers see the old or the new content, never a mix. The result has mode perm.
	WriteFileAtomic(name string, data []byte, perm fs.FileMode) error
	Chmod(name string, mode fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	MkdirAll(name string, perm fs.FileMode) error
	// Rename replaces newpath if it exists (POSIX rename semantics).
	Rename(oldpath, newpath string) error
	Remove(name string) error
	// Lock takes an exclusive advisory lock on name (created if missing), waiting until it is
	// free or ctx is done. The lock is released by Unlock, or when the process or connection dies.
	Lock(ctx context.Context, name string) (Unlocker, error)
}

// Unlocker releases a lock taken with FS.Lock.
type Unlocker interface {
	Unlock() error
}

// Cmd is one process to run on the host. Args is an argv, never a shell string.
type Cmd struct {
	Args []string
	// Env is added to the process environment as KEY=VALUE entries.
	Env []string
	// Dir is the working directory ("" = the implementation's default).
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// TTY runs the command attached to the operator's terminal (docker exec -it, ssh -t):
	// stdio is inherited and nil Stdin/Stdout/Stderr mean the process's own. berth never
	// reimplements a pty.
	TTY bool
}

// Execer runs processes on the host.
type Execer interface {
	// Run runs cmd to completion. A process that exits non-zero returns *ExitError with its code.
	Run(ctx context.Context, cmd Cmd) error
}

// ExitError is a process that ran and exited non-zero. Passthrough commands (claude, run,
// attach...) hand Code straight to berth's own exit.
type ExitError struct {
	Args []string
	Code int
}

func (e *ExitError) Error() string {
	name := "command"
	if len(e.Args) > 0 {
		name = e.Args[0]
	}
	return fmt.Sprintf("%s exited with status %d", name, e.Code)
}

// Docker reaches the host's Docker engine API. berth uses it for reads only (ps, inspect, exec
// status); anything that changes containers shells out to `docker compose` through Exec.
type Docker interface {
	// DialEngine opens a connection to the engine API socket.
	DialEngine(ctx context.Context) (net.Conn, error)
}

// Facts are what berth needs to know about a host to place an org on it.
type Facts struct {
	UID, GID int
	// Arch is Docker's name for the CPU architecture: amd64, arm64...
	Arch string
	// TailscaleIP is the host's Tailscale IPv4, or "" if Tailscale isn't up (ccenv resolve_bind).
	TailscaleIP string
}

// Facter reports facts about a host.
type Facter interface {
	Facts(ctx context.Context) (Facts, error)
	// PortsInUse lists TCP ports that are listening on the host or published by its containers.
	PortsInUse(ctx context.Context) ([]int, error)
}
