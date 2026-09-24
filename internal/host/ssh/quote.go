package ssh

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
)

// SSH runs a remote command as one string through the remote user's shell, so every argv
// element is quoted here. The remote shell is assumed to be POSIX sh-compatible.

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// quote returns s as one POSIX sh word.
func quote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// remoteCommand turns c into the command line sent over SSH:
//
//	cd '<dir>' && exec env -- 'K=V'... 'argv0' 'argv1'...
//
// Env goes through env(1) rather than the SSH "env" request, which sshd refuses unless AcceptEnv
// allows each name.
func remoteCommand(c host.Cmd) (string, error) {
	if len(c.Args) == 0 {
		return "", errors.New("exec: empty command")
	}
	var b strings.Builder
	if c.Dir != "" {
		if !strings.HasPrefix(c.Dir, "/") {
			return "", fmt.Errorf("exec: dir %q must be absolute on a remote host", c.Dir)
		}
		b.WriteString("cd " + quote(c.Dir) + " && ")
	}
	b.WriteString("exec")
	if len(c.Env) > 0 {
		b.WriteString(" env --")
		for _, e := range c.Env {
			// Without a NAME=, env would take the entry as the command to run.
			if !envName.MatchString(e) {
				return "", fmt.Errorf("exec: env entry %q is not NAME=VALUE", e)
			}
			b.WriteString(" " + quote(e))
		}
	}
	for _, a := range c.Args {
		b.WriteString(" " + quote(a))
	}
	return b.String(), nil
}
