// Package ssh is a host reached over SSH: files through SFTP, processes through SSH sessions,
// and the Docker engine through a streamlocal channel to its unix socket, so the remote host
// needs nothing but sshd, a POSIX shell and Docker.
//
// Host keys are checked against berth's own known_hosts file. There is no trust on first use
// unless the caller sets AcceptNewHostKey explicitly.
package ssh

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/ar4mirez/berth/internal/host"
)

// DefaultDockerSocket is the engine socket on the remote host.
const DefaultDockerSocket = "/var/run/docker.sock"

// Config says how to reach a host.
type Config struct {
	// Addr is host or host:port (default port 22).
	Addr string
	User string
	// KnownHosts is berth's own known_hosts file on the operator's machine. Required.
	KnownHosts string
	// AcceptNewHostKey trusts and records the key of a host that isn't in KnownHosts yet.
	// A key that differs from the recorded one is refused regardless.
	AcceptNewHostKey bool
	// IdentityFiles are private keys on the operator's machine. Passphrase-protected keys must
	// be loaded into ssh-agent instead.
	IdentityFiles []string
	// Signers are extra in-memory keys. They can't be used by TTY commands, which run the
	// operator's ssh binary; use IdentityFiles or the agent for those.
	Signers []gossh.Signer
	// NoAgent skips $SSH_AUTH_SOCK.
	NoAgent bool
	// DockerSocket defaults to DefaultDockerSocket.
	DockerSocket string
	// Timeout bounds the TCP connect and handshake (default 15s).
	Timeout time.Duration
}

// Dial connects to the host. operator is the machine berth runs on: it holds known_hosts and the
// identity files, and runs the ssh binary for TTY commands.
func Dial(ctx context.Context, cfg Config, operator *host.Host) (*host.Host, error) {
	if cfg.KnownHosts == "" {
		return nil, errors.New("ssh: a known_hosts file is required")
	}
	if cfg.User == "" {
		return nil, errors.New("ssh: a user is required")
	}
	addr := cfg.Addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "22")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.DockerSocket == "" {
		cfg.DockerSocket = DefaultDockerSocket
	}

	auth, closeAgent, err := authMethods(cfg, operator.FS)
	if err != nil {
		return nil, err
	}
	hk := hostKeys{fs: operator.FS, path: cfg.KnownHosts, acceptNew: cfg.AcceptNewHostKey}
	known, err := hk.load()
	if err != nil {
		closeAgent()
		return nil, err
	}
	cc := &gossh.ClientConfig{
		User:              cfg.User,
		Auth:              auth,
		HostKeyCallback:   hk.callback(known),
		HostKeyAlgorithms: algorithms(known, addr),
		Timeout:           cfg.Timeout,
	}

	client, err := dial(ctx, addr, cc)
	if err != nil {
		closeAgent()
		return nil, fmt.Errorf("ssh %s@%s: %w", cfg.User, addr, err)
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		closeAgent()
		return nil, fmt.Errorf("ssh %s@%s: sftp: %w", cfg.User, addr, err)
	}

	hostname, port, _ := net.SplitHostPort(addr)
	ex := &execer{client: client, operator: operator, cfg: cfg, hostname: hostname, port: port}
	d := docker{client: client, sock: cfg.DockerSocket}
	closeFn := func() error {
		defer closeAgent()
		return errors.Join(sc.Close(), client.Close())
	}
	name := "ssh://" + cfg.User + "@" + addr
	return host.New(name, &sftpFS{c: sc, ex: ex}, ex, d, host.ExecFacts{Exec: ex, Docker: d}, closeFn), nil
}

func dial(ctx context.Context, addr string, cc *gossh.ClientConfig) (*gossh.Client, error) {
	d := net.Dialer{Timeout: cc.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// Bound the handshake too; cleared once the connection is up.
	deadline := time.Now().Add(cc.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	c, chans, reqs, err := gossh.NewClientConn(conn, addr, cc)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return gossh.NewClient(c, chans, reqs), nil
}

// authMethods offers the identity files and in-memory signers first, then the agent.
func authMethods(cfg Config, fsys host.FS) ([]gossh.AuthMethod, func(), error) {
	signers := slices.Clone(cfg.Signers)
	for _, f := range cfg.IdentityFiles {
		b, err := fsys.ReadFile(f)
		if err != nil {
			return nil, nil, fmt.Errorf("ssh identity: %w", err)
		}
		s, err := gossh.ParsePrivateKey(b)
		var pm *gossh.PassphraseMissingError
		if errors.As(err, &pm) {
			return nil, nil, fmt.Errorf("ssh identity %s is passphrase-protected: load it into ssh-agent instead", f)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("ssh identity %s: %w", f, err)
		}
		signers = append(signers, s)
	}
	var methods []gossh.AuthMethod
	if len(signers) > 0 {
		methods = append(methods, gossh.PublicKeys(signers...))
	}
	closeAgent := func() {}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" && !cfg.NoAgent {
		if conn, err := net.Dial("unix", sock); err == nil { // #nosec G704 -- the operator's own ssh-agent socket
			methods = append(methods, gossh.PublicKeysCallback(agent.NewClient(conn).Signers))
			closeAgent = func() { _ = conn.Close() }
		}
	}
	if len(methods) == 0 {
		return nil, nil, errors.New("ssh: no keys to authenticate with (no identity files, and no ssh-agent)")
	}
	return methods, closeAgent, nil
}

// ---------------------------------------------------------------------------- exec

type execer struct {
	client         *gossh.Client
	operator       *host.Host
	cfg            Config
	hostname, port string
}

func (e *execer) Run(ctx context.Context, c host.Cmd) error {
	line, err := remoteCommand(c)
	if err != nil {
		return err
	}
	if c.TTY {
		return e.runTTY(ctx, c, line)
	}
	s, err := e.client.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	s.Stdin, s.Stdout, s.Stderr = c.Stdin, c.Stdout, c.Stderr
	if err := s.Start(line); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err := <-done:
		return exitErr(c.Args, err)
	case <-ctx.Done():
		_ = s.Signal(gossh.SIGKILL)
		_ = s.Close()
		<-done
		return ctx.Err()
	}
}

// runTTY hands interactive commands to the operator's ssh binary (ssh -t), with the same
// known_hosts policy, rather than reimplementing a pty. ssh exits with the remote command's code.
func (e *execer) runTTY(ctx context.Context, c host.Cmd, line string) error {
	args := []string{"ssh", "-t",
		"-o", "UserKnownHostsFile=" + e.cfg.KnownHosts,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=yes",
		"-p", e.port, "-l", e.cfg.User}
	for _, f := range e.cfg.IdentityFiles {
		args = append(args, "-i", f)
	}
	args = append(args, "--", e.hostname, line)
	err := e.operator.Exec.Run(ctx, host.Cmd{Args: args, Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr, TTY: true})
	var ee *host.ExitError
	if errors.As(err, &ee) {
		return &host.ExitError{Args: c.Args, Code: ee.Code}
	}
	return err
}

// signals maps SSH signal names to numbers for the 128+n exit code a shell reports.
var signals = map[gossh.Signal]int{
	gossh.SIGHUP: 1, gossh.SIGINT: 2, gossh.SIGQUIT: 3, gossh.SIGILL: 4, gossh.SIGABRT: 6, gossh.SIGFPE: 8,
	gossh.SIGKILL: 9, gossh.SIGUSR1: 10, gossh.SIGSEGV: 11, gossh.SIGUSR2: 12, gossh.SIGPIPE: 13,
	gossh.SIGALRM: 14, gossh.SIGTERM: 15,
}

func exitErr(args []string, err error) error {
	var ee *gossh.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitStatus()
		if n, ok := signals[gossh.Signal(ee.Signal())]; ok {
			code = 128 + n
		}
		return &host.ExitError{Args: args, Code: code}
	}
	var missing *gossh.ExitMissingError
	if errors.As(err, &missing) {
		return fmt.Errorf("%s: the remote end closed without an exit status", args[0])
	}
	return err
}

// ---------------------------------------------------------------------------- docker

type docker struct {
	client *gossh.Client
	sock   string
}

// DialEngine opens a direct-streamlocal channel to the remote engine socket (like ssh -L to a
// unix socket), so the engine is never exposed on a TCP port.
func (d docker) DialEngine(ctx context.Context) (net.Conn, error) {
	c, err := d.client.DialContext(ctx, "unix", d.sock)
	var oce *gossh.OpenChannelError
	if errors.As(err, &oce) {
		// sshd gives the same "open failed" for a refusal and a missing socket; its log tells which.
		return nil, fmt.Errorf("docker engine %s over ssh: %w (the host's sshd must allow it: "+
			"AllowStreamLocalForwarding yes, and AllowTcpForwarding not \"no\", which OpenSSH also applies to "+
			"unix sockets; and Docker must be running)", d.sock, err)
	}
	return c, err
}

// ---------------------------------------------------------------------------- fs

type sftpFS struct {
	c  *sftp.Client
	ex *execer
}

func (f *sftpFS) ReadFile(name string) ([]byte, error) {
	fh, err := f.c.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	return io.ReadAll(fh)
}

func (f *sftpFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	fh, err := f.c.OpenFile(name, os.O_WRONLY|os.O_TRUNC) // existing file: keeps inode and mode
	if errors.Is(err, fs.ErrNotExist) {
		fh, err = f.create(name, perm)
	}
	if err != nil {
		return err
	}
	if _, err := fh.Write(data); err != nil {
		_ = fh.Close()
		return err
	}
	return fh.Close()
}

// create makes name and sets perm before any data is written (the remote umask is unknown).
func (f *sftpFS) create(name string, perm fs.FileMode) (*sftp.File, error) {
	fh, err := f.c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, err
	}
	if err := fh.Chmod(perm); err != nil {
		_ = fh.Close()
		return nil, err
	}
	return fh, nil
}

func (f *sftpFS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) (err error) {
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(name), "."+path.Base(name)+".berth-"+hex.EncodeToString(rnd[:]))
	fh, err := f.create(tmp, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.c.Remove(tmp)
		}
	}()
	if _, err = fh.Write(data); err != nil {
		_ = fh.Close()
		return err
	}
	if err = fh.Close(); err != nil {
		return err
	}
	return f.c.PosixRename(tmp, name)
}

func (f *sftpFS) Chmod(name string, mode fs.FileMode) error { return f.c.Chmod(name, mode) }
func (f *sftpFS) Stat(name string) (fs.FileInfo, error)     { return f.c.Stat(name) }
func (f *sftpFS) Rename(oldpath, newpath string) error      { return f.c.PosixRename(oldpath, newpath) }
func (f *sftpFS) Remove(name string) error                  { return f.c.Remove(name) }

func (f *sftpFS) RemoveAll(name string) error {
	if _, err := f.c.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return f.c.RemoveAll(name)
}

func (f *sftpFS) Create(name string, perm fs.FileMode) (io.WriteCloser, error) {
	fh, err := f.c.OpenFile(name, os.O_WRONLY|os.O_TRUNC) // existing file: keeps its mode
	if errors.Is(err, fs.ErrNotExist) {
		return f.create(name, perm)
	}
	return fh, err
}

func (f *sftpFS) Open(name string) (io.ReadCloser, error) { return f.c.Open(name) }

// MkdirTemp uses /tmp when dir is "": the remote $TMPDIR isn't known over SFTP.
func (f *sftpFS) MkdirTemp(dir, pattern string) (string, error) {
	if dir == "" {
		dir = "/tmp"
	}
	for {
		name, err := host.TempName(pattern)
		if err != nil {
			return "", err
		}
		p := path.Join(dir, name)
		if _, err := f.c.Lstat(p); err == nil {
			continue
		}
		if err := f.c.Mkdir(p); err != nil {
			return "", err
		}
		return p, f.c.Chmod(p, 0o700)
	}
}

func (f *sftpFS) ReadDir(name string) ([]fs.DirEntry, error) {
	infos, err := f.c.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, len(infos))
	for i, fi := range infos {
		out[i] = fs.FileInfoToDirEntry(fi)
	}
	return out, nil
}

// MkdirAll creates each missing directory with exactly perm (the remote umask is unknown).
func (f *sftpFS) MkdirAll(name string, perm fs.FileMode) error {
	if fi, err := f.c.Stat(name); err == nil {
		if !fi.IsDir() {
			return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrExist}
		}
		return nil
	}
	if parent := path.Dir(name); parent != name {
		if err := f.MkdirAll(parent, perm); err != nil {
			return err
		}
	}
	if err := f.c.Mkdir(name); err != nil {
		if fi, serr := f.c.Stat(name); serr == nil && fi.IsDir() { // created concurrently
			return nil
		}
		return err
	}
	return f.c.Chmod(name, perm)
}

// Lock holds flock(1) on the host for as long as an SSH session stays open: the remote side
// prints "locked" once it has the lock, then waits on stdin. Unlock closes stdin; a dropped
// connection releases the lock too. The host needs flock (util-linux, or BusyBox's applet).
func (f *sftpFS) Lock(ctx context.Context, name string) (host.Unlocker, error) {
	if !strings.HasPrefix(name, "/") {
		return nil, &fs.PathError{Op: "lock", Path: name, Err: errors.New("path must be absolute")}
	}
	s, err := f.ex.client.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := s.StdinPipe()
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	stdout, err := s.StdoutPipe()
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	var errb bytes.Buffer
	s.Stderr = &errb
	if err := s.Start("exec flock -x " + quote(name) + ` sh -c 'echo locked; exec cat >/dev/null'`); err != nil {
		_ = s.Close()
		return nil, err
	}
	got := make(chan error, 1)
	go func() {
		l, err := bufio.NewReader(stdout).ReadString('\n')
		if l == "locked\n" {
			got <- nil
			return
		}
		werr := s.Wait() // the remote side ended; stderr is complete now
		got <- fmt.Errorf("remote flock: %s", strings.TrimSpace(errors.Join(err, werr).Error()+" "+errb.String()))
	}()
	select {
	case err := <-got:
		if err != nil {
			_ = s.Close()
			return nil, &fs.PathError{Op: "lock", Path: name, Err: err}
		}
		return &remoteLock{s: s, stdin: stdin}, nil
	case <-ctx.Done():
		_ = s.Close()
		return nil, &fs.PathError{Op: "lock", Path: name, Err: ctx.Err()}
	}
}

type remoteLock struct {
	s     *gossh.Session
	stdin io.WriteCloser
}

func (l *remoteLock) Unlock() error {
	err := l.stdin.Close()
	if werr := l.s.Wait(); werr != nil && err == nil {
		err = werr
	}
	_ = l.s.Close()
	return err
}
