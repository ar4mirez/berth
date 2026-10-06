package ops

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// Tool is the name berth uses in the hints it gives ("berth up acme").
const Tool = "berth"

// System is what a read operation runs against: the commands and files of the host an org is on,
// and that state root's orgs. internal/app's App implements it with the helpers its commands use,
// so an operation here makes the same calls, in the same order, as the command did (the parity
// suite compares them). Nothing in this package prints: a command's stderr goes wherever the
// System sends diagnostics.
type System interface {
	// Capture is `$(cmd)`: stdout without its trailing newlines. quiet drops the command's stderr.
	// err is non-nil if the command failed or exited non-zero.
	Capture(ctx context.Context, quiet bool, args ...string) (string, error)
	// CaptureRaw is Capture with the trailing newlines kept.
	CaptureRaw(ctx context.Context, quiet bool, args ...string) (string, error)
	// Succeeds runs a command for its exit status. quiet drops its stdout too.
	Succeeds(ctx context.Context, quiet bool, args ...string) bool

	// ReadFile, IsFile (`[ -f ]`) and IsDir (`[ -d ]`) are the host's files.
	ReadFile(p string) ([]byte, error)
	IsFile(p string) bool
	IsDir(p string) bool

	// OrgsDir is <state root>/orgs. OrgDirs are its directories, in byte order (an org is one with
	// an org.env).
	OrgsDir() string
	OrgDirs() []string
	// EnvGet is a key in an org's org.env. Secret is a variable's value wherever the org keeps it
	// (org.env, or a file since #37). SecretFile reports whether it is kept as a file.
	EnvGet(org, key string) (string, bool)
	Secret(org, key string) string
	SecretFile(org, key string) bool

	// OperatorEnv is a variable in the operator's environment.
	OperatorEnv(key string) string
	// HostLabel is where these orgs are: "local", or a registered host's name.
	HostLabel() string
	// Peers are the registered hosts whose orgs a listing includes (none on a host's own System).
	Peers(ctx context.Context) []Peer
	// Destroyed are the orgs `destroy` removed from this System that don't exist again since.
	Destroyed() []DestroyedOrg
	// Hosts is this machine and every registered host, with what could be learned of each.
	Hosts(ctx context.Context) ([]HostStatus, error)
	// Address is host_addr: the address other devices use to reach an org's ports ("" if its bind
	// can't be resolved). Tunnel is how to reach an org bound to localhost, or nil if it needs none.
	Address(ctx context.Context, org string) string
	Tunnel(ctx context.Context, org, sshPort, ttydPort string) *Tunnel
	// DefaultOrg is the saved default org, as `berth use` wrote it ("" for none).
	DefaultOrg() string
	// Image is the tag of the image this berth runs its orgs on.
	Image() (string, error)
}

// Peer is a registered host. Open dials it and returns its System and a function that ends the
// connection, or why it couldn't be reached.
type Peer struct {
	Name string
	Open func(ctx context.Context) (System, func(), error)
}

// Exit ends berth with Code and no message: ccenv exits that way where set -e stops a command.
type Exit struct{ Code int }

func (e *Exit) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode lets the CLI end with Code without printing anything.
func (e *Exit) ExitCode() int { return e.Code }

// OrgName is what an org may be called.
var OrgName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// EnvPath is an org's org.env.
func EnvPath(s System, org string) string { return path.Join(s.OrgsDir(), org, "org.env") }

// env is envval: "" when the key or the file is missing.
func env(s System, org, key string) string {
	v, _ := s.EnvGet(org, key)
	return v
}

// NeedOrg is ccenv's need_org for commands that only read: it doesn't refuse orgs by MANAGER,
// because berth must be able to read legacy-owned orgs before cutover (PARITY.md). A name that
// isn't a valid org name is reported as unknown (ccenv would look for it and not find it).
func NeedOrg(s System, org string) error {
	if org == "" {
		return errors.New("missing <org>")
	}
	if !OrgName.MatchString(org) || !s.IsFile(EnvPath(s, org)) {
		return fmt.Errorf("unknown org '%s' (run: %s init %s)", org, Tool, org)
	}
	return nil
}

// Running is `docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "claude-$1"` (under pipefail,
// a failing docker ps means not running even if it printed the name).
func Running(ctx context.Context, s System, org string) bool {
	out, err := s.Capture(ctx, true, "docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(out, "\n") {
		if l == contract.Container(org) {
			return true
		}
	}
	return false
}

// NeedUp is need_up: the org's container must be running.
func NeedUp(ctx context.Context, s System, org string) error {
	if !Running(ctx, s, org) {
		return fmt.Errorf("claude-%s is not running (%s up %s)", org, Tool, org)
	}
	return nil
}

// lines splits file content into lines the way grep reads them: a final newline ends the last
// line, and a last line without one still counts.
func lines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	ls := strings.Split(string(b), "\n")
	if ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}
