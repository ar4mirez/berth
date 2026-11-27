package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// Supervised processes (#216): a named long-lived process in a running org's container, started
// and supervised there by image/berth-proc as the container's user, never root. berth reads their
// state from it; it lives as long as its container, and an up, restart or down ends it.

// ProcUser is who a supervised process runs as: the container's normal user, the one SSH uses.
const ProcUser = "node"

// ProcBin is the supervisor in berth's image.
const ProcBin = "/usr/local/bin/berth-proc"

// ProcessInfo is one supervised process, as each of berth.processes/v1 lists it.
type ProcessInfo struct {
	Name string `json:"name"`
	// State is starting, running, exited or backoff (waiting to start it again after a failure).
	State     string   `json:"state"`
	PID       *int     `json:"pid"`
	StartedAt *string  `json:"started_at"`
	ExitCode  *int     `json:"exit_code"`
	Restarts  int      `json:"restarts"`
	Argv      []string `json:"argv"`
	// EnvNames are its variables, by name: their values are never shown.
	EnvNames []string `json:"env_names"`
}

// Process is one supervised process (berth.process/v1): ProcessInfo with its schema and org.
type Process struct {
	Schema    string   `json:"schema"`
	Org       string   `json:"org"`
	Name      string   `json:"name"`
	State     string   `json:"state"`
	PID       *int     `json:"pid"`
	StartedAt *string  `json:"started_at"`
	ExitCode  *int     `json:"exit_code"`
	Restarts  int      `json:"restarts"`
	Argv      []string `json:"argv"`
	EnvNames  []string `json:"env_names"`
}

// Processes is `proc <org> ls` (berth.processes/v1).
type Processes struct {
	Schema    string        `json:"schema"`
	Org       string        `json:"org"`
	Processes []ProcessInfo `json:"processes"`
}

// ProcSpec is what starts a process: berth-proc reads it on its stdin.
type ProcSpec struct {
	Argv    []string          `json:"argv"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
	Restart string            `json:"restart"`
	Grace   int               `json:"stop_grace_seconds"`
}

var (
	procName = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	envName  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// CheckProcName is a process's name: lowercase letters, digits and dashes, up to 32.
func CheckProcName(n string) error {
	if !procName.MatchString(n) {
		return Usage("the process's name %q: lowercase letters, digits and dashes, up to 32", n)
	}
	return nil
}

// CheckProcSpec fills in a spec's defaults (cwd /workspace, restart on-failure, a 30s grace) and
// checks it: argv a list (never a shell string) whose first word is an absolute path or a name to
// look up on the container user's PATH; cwd an absolute path; variables named like A_B1, each
// value one string; restart never or on-failure; a grace of 1 to 300 seconds.
func CheckProcSpec(s *ProcSpec) error {
	if s.Cwd == "" {
		s.Cwd = contract.Workspace
	}
	if s.Restart == "" {
		s.Restart = "on-failure"
	}
	if s.Grace == 0 {
		s.Grace = 30
	}
	if s.Env == nil {
		s.Env = map[string]string{}
	}
	switch {
	case len(s.Argv) == 0 || s.Argv[0] == "":
		return Usage("argv is the command and its arguments, as a list; its first word is an absolute path or a name on the container user's PATH")
	case strings.Contains(s.Argv[0], "/") && !path.IsAbs(s.Argv[0]),
		strings.HasPrefix(s.Argv[0], "-"), strings.Contains(s.Argv[0], "="):
		return Usage("argv[0] %q: an absolute path, or a name on the container user's PATH", s.Argv[0])
	case !path.IsAbs(s.Cwd) || path.Clean(s.Cwd) != s.Cwd:
		return Usage("cwd %q: an absolute path in the container, written plainly", s.Cwd)
	case s.Restart != "never" && s.Restart != "on-failure":
		return Usage("restart %q: never or on-failure", s.Restart)
	case s.Grace < 1 || s.Grace > 300:
		return Usage("stop_grace_seconds %d: 1 to 300", s.Grace)
	}
	for _, a := range s.Argv {
		if strings.ContainsRune(a, 0) {
			return Usage("argv: an argument with a NUL byte")
		}
	}
	for k, v := range s.Env {
		if !envName.MatchString(k) {
			return Usage("env %q: a name of capitals, digits and underscores, as in API_URL", k)
		}
		if strings.ContainsRune(v, 0) {
			return Usage("env %s: a value with a NUL byte", k)
		}
	}
	return nil
}

// EnvNames are a process's variables as the audit log holds them: each one's name, what comes
// before its =, never its value; sorted. Each of vars is NAME=value or a name alone.
func EnvNames(vars ...string) []string {
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		n, _, _ := strings.Cut(v, "=")
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// RedactEnvArgs is a command line as the audit log holds it: each --env keeps its variable's
// name, NAME=***, never its value. The command after -- is kept as it is.
func RedactEnvArgs(args []string) []string {
	out := slices.Clone(args)
	for i := 0; i < len(out); i++ {
		switch x := out[i]; {
		case x == "--":
			return out
		case x == "--env" && i+1 < len(out):
			i++
			out[i] = EnvNames(out[i])[0] + "=***"
		case strings.HasPrefix(x, "--env="):
			out[i] = "--env=" + EnvNames(strings.TrimPrefix(x, "--env="))[0] + "=***"
		}
	}
	return out
}

// procExec is berth-proc's command line in the org's container, as its user.
func procExec(org string, args ...string) []string {
	return append([]string{"docker", "exec", "-u", ProcUser, contract.Container(org), ProcBin}, args...)
}

// ProcErr is berth-proc's exit code as an error a caller can act on: 3 a process of that name
// exists (409 state), 4 no such process (404), 2 a spec it refused (400), 127 or 126 an image
// that predates supervised processes.
func ProcErr(org string, code int, msg string) error {
	msg = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg), "berth-proc: "))
	switch code {
	case 2:
		return Usage("%s", msg)
	case 3:
		return State("%s", msg)
	case 4:
		return &Error{Kind: KindNotFound, Code: 1, Msg: msg}
	case 126, 127:
		return fail(KindState, Tool+" restart "+org+", which stops the work running in it", "%s's container runs an image that has no supervised processes: restart it on berth's current image", org)
	}
	if msg == "" {
		msg = fmt.Sprintf("berth-proc ended with exit status %d", code)
	}
	return fail(KindFailed, "", "%s", msg)
}

// GetProcesses is `proc <org> ls`. A stopped org has none: a process lives as long as its
// container.
func GetProcesses(ctx context.Context, s System, org string) (Processes, error) {
	out := Processes{Schema: "berth.processes/v1", Org: org, Processes: []ProcessInfo{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	if !Running(ctx, s, org) {
		return out, nil
	}
	raw, err := s.Capture(ctx, true, procExec(org, "status")...)
	if err != nil {
		var code interface{ ExitCode() int }
		if errors.As(err, &code) {
			return out, ProcErr(org, code.ExitCode(), "")
		}
		return out, err
	}
	if err := json.Unmarshal([]byte(raw), &out.Processes); err != nil {
		return out, fmt.Errorf("berth-proc status: %w", err)
	}
	for i := range out.Processes {
		if out.Processes[i].EnvNames == nil {
			out.Processes[i].EnvNames = []string{}
		}
	}
	return out, nil
}

// ParseProcess is one process as berth-proc prints it, as a berth.process/v1 document.
func ParseProcess(org, raw string) (Process, error) {
	var p Process
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, fmt.Errorf("berth-proc: %w", err)
	}
	p.Schema, p.Org = "berth.process/v1", org
	if p.EnvNames == nil {
		p.EnvNames = []string{}
	}
	if p.Argv == nil {
		p.Argv = []string{}
	}
	return p, nil
}
