// Package app implements berth's commands on top of internal/host, mirroring legacy/ccenv's
// behaviour byte for byte unless PARITY.md records a divergence. Docker is driven through the docker
// CLI with the same argv ccenv uses, so the parity harness sees identical calls.
package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/org"
)

// Tool is the name berth uses in its own command hints ("berth up acme" where ccenv says
// "ccenv up acme"); the parity harness normalizes both.
const Tool = "berth"

// App is one berth invocation: the state it runs against, the host holding it, and stdio.
type App struct {
	State  config.State
	Host   *host.Host
	Orgs   org.Orgs
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// OpenTTY opens the operator's terminal for prompts ccenv reads from /dev/tty (nil: none).
	OpenTTY func() (*os.File, error)

	umaskVal *fs.FileMode // cached by umask()
}

// New builds an App for st on h. h's FS is guarded, so writes fail under --read-only.
func New(st config.State, h *host.Host, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) *App {
	h = host.Guard(h, st)
	return &App{
		State: st, Host: h,
		Orgs:  org.Orgs{FS: h.FS, Dir: path.Join(st.Home.Path, "orgs")},
		Stdin: stdin, Stdout: stdout, Stderr: stderr, Getenv: getenv,
	}
}

// Exit ends berth with Code and no message: ccenv exits that way where set -e stops a command.
type Exit struct{ Code int }

func (e *Exit) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode lets the CLI end with Code without printing anything.
func (e *Exit) ExitCode() int { return e.Code }

var orgName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// needOrg is ccenv's need_org for commands that only read: it doesn't refuse orgs by MANAGER,
// because berth must be able to read legacy-owned orgs before cutover (PARITY.md). A name that
// isn't a valid org name is reported as unknown (ccenv would look for it and not find it).
func (a *App) needOrg(o string) error {
	if o == "" {
		return errors.New("missing <org>")
	}
	if !orgName.MatchString(o) || !a.isFile(a.Orgs.EnvPath(o)) {
		return fmt.Errorf("unknown org '%s' (run: %s init %s)", o, Tool, o)
	}
	return nil
}

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
		fmt.Fprintf(a.Stderr, "cat: %s: %s\n", p, msg)
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}
