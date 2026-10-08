// Package app implements berth's commands on top of internal/host, mirroring legacy/ccenv's
// behaviour byte for byte unless PARITY.md records a divergence. Docker is driven through the docker
// CLI with the same argv ccenv uses, so the parity harness sees identical calls.
package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
	"github.com/ar4mirez/berth/internal/org"
)

// Tool is the name berth uses in its own command hints ("berth up acme" where ccenv says
// "ccenv up acme"); the parity harness normalizes both.
const Tool = ops.Tool

// App is one berth invocation: the state it runs against, the host holding it, and stdio.
type App struct {
	State config.State
	// Host is the machine the org lives on: this one, or a registered host for org@host (#45).
	Host *host.Host
	// Operator is the machine berth runs on: the operator's own files (~/.ssh keys, berth's host
	// registry) are read and written here, whichever host the org is on.
	Operator *host.Host
	// HostName is the registered host an org@host resolved to ("" for this machine).
	HostName string
	// DefaultBind is BIND_ADDR for new orgs here ("": ccenv's default, tailscale when it's
	// installed, else 127.0.0.1): bind: in config.yaml, or a registered host's (#58).
	DefaultBind string
	// TakeLease lets up and restart move the org's active-host lease here (--take-lease, #47).
	TakeLease bool
	// HostAppFn, when set, replaces how an App for another host is made (tests).
	HostAppFn func(ctx context.Context, name string) (*App, func(), error)
	root      *App // the App on this machine, for an org@host App
	Orgs      org.Orgs
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Getenv    func(string) string
	// Upgrader fetches and verifies releases for `berth upgrade` (nil: not available).
	Upgrader *Upgrader
	// Output is OutputText (the default, ccenv's) or OutputJSON, for operations that return data.
	Output string
	// Progress is where a long operation reports its steps (nil: nowhere). With one, Stdout and
	// Stderr are its writers, so everything printed is an event too.
	Progress *ops.Progress
	// Self is the berth binary itself (symlinks resolved): what install links to.
	Self string
	// CloudRegister registers a host berth created at a cloud provider, once it answers (the tests
	// replace the wait and the ssh that it takes).
	CloudRegister func(ctx context.Context, c CloudHost) error
	// Invoked is the path berth was run as, symlinks kept (~/.local/bin/berth): what the scheduled
	// backup job runs, so it keeps working when an upgrade moves the link to a new binary.
	Invoked string
	// OpenTTY opens the operator's terminal for prompts ccenv reads from /dev/tty (nil: none).
	OpenTTY func() (*os.File, error)

	umaskVal *fs.FileMode // cached by umask()

	lockHeld  host.Unlocker // the state root's lock, while held (lock.go)
	lockDepth int
}

// New builds an App for st on h. h's FS is guarded, so writes fail under --read-only.
func New(st config.State, h *host.Host, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) *App {
	h = host.Guard(h, st)
	a := &App{
		State: st, Host: h, Operator: h,
		Orgs:  org.Orgs{FS: h.FS, Dir: path.Join(st.Home.Path, "orgs")},
		Stdin: stdin, Stdout: stdout, Stderr: stderr, Getenv: getenv,
	}
	a.Orgs.Lock = func() (func(), error) { return a.lock(context.Background()) }
	return a
}

// Exit ends berth with Code and no message: ccenv exits that way where set -e stops a command.
type Exit = ops.Exit

// needOrg is ccenv's need_org for commands that only read (ops.NeedOrg).
func (a *App) needOrg(o string) error { return ops.NeedOrg(a, o) }

// isFile is `[ -f p ]`: a regular file, following symlinks.
func (a *App) isFile(p string) bool {
	fi, err := a.Host.FS.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// isDir is `[ -d p ]`, following symlinks.
func (a *App) isDir(p string) bool {
	fi, err := a.Host.FS.Stat(p)
	return err == nil && fi.IsDir()
}

// env is envval: "" when the key or the file is missing.
func (a *App) env(o, key string) string {
	v, _, _ := a.Orgs.Get(o, key)
	return v
}

// orgDirs is ccenv's `for d in "$ORGS"/*/`: non-hidden entries that are directories (symlinks
// followed), in byte order. (bash sorts the glob by the locale's collation; for org names,
// [a-z0-9-], that is byte order in the C locale.)
func (a *App) orgDirs() []string {
	entries, err := a.Host.FS.ReadDir(a.Orgs.Dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") && a.isDir(path.Join(a.Orgs.Dir, e.Name())) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// catFile is `$(cat f)`: the content without trailing newlines, and cat's error on stderr.
func (a *App) catFile(p string) string {
	b, err := a.Host.FS.ReadFile(p)
	if err != nil {
		msg := err.Error()
		switch {
		case errors.Is(err, fs.ErrNotExist):
			msg = "No such file or directory"
		case errors.Is(err, fs.ErrPermission):
			msg = "Permission denied"
		}
		sayf(a.Stderr, "cat: %s: %s\n", p, msg)
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}
