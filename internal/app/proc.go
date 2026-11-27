package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
)

// Supervised processes (#216, internal/ops/procs.go and image/berth-proc; berth-only): a named
// long-lived process in a running org's container, as its user, supervised there.

// procWrites are the proc subcommands that start or stop a process.
var procWrites = map[string]bool{"start": true, "stop": true}

// Proc is `berth proc <org> [ls | start <name> [options] -- <command> [args...] | show <name> |
// stop <name> | logs <name> [--follow] [--lines N]]`.
func (a *App) Proc(ctx context.Context, o string, args []string) error {
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	usage := fmt.Errorf("usage: %s proc <org> [ls | start <name> [--cwd DIR] [--env K=V]... [--restart never|on-failure] [--grace SECONDS] -- <command> [args...] | show <name> | stop <name> | logs <name> [--follow] [--lines N]]", Tool)
	if procWrites[sub] {
		if err := a.writable(o, "start or stop "+o+"'s processes"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	switch sub {
	case "ls", "list":
		if len(args) > 0 {
			return usage
		}
		p, err := ops.GetProcesses(ctx, a, o)
		if err != nil {
			return err
		}
		if a.Output == OutputJSON {
			return a.writeJSON(p)
		}
		if len(p.Processes) == 0 {
			sayf(a.Stdout, "(no processes in %s; start one: %s proc %s start <name> -- <command> [args...])\n", o, Tool, o)
		}
		for _, x := range p.Processes {
			sayf(a.Stdout, "%s  %s%s  %s\n", x.Name, x.State, procWhen(x.State, x.StartedAt, x.ExitCode), strings.Join(x.Argv, " "))
		}
		return nil
	case "start":
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return usage
		}
		spec, err := parseProcStart(args[1:])
		if err != nil {
			return fmt.Errorf("%w; %s", err, usage)
		}
		p, err := a.ProcStart(ctx, o, args[0], spec)
		if err != nil {
			return err
		}
		return a.procShow(p, "Started")
	case "show", "stop":
		if len(args) != 1 {
			return usage
		}
		p, err := a.ProcAct(ctx, o, sub, args[0])
		if err != nil {
			return err
		}
		return a.procShow(p, map[string]string{"show": "", "stop": "Stopped"}[sub])
	case "logs":
		if len(args) == 0 {
			return usage
		}
		if err := ops.CheckProcName(args[0]); err != nil {
			return err
		}
		rest := []string{"logs", args[0]}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--follow", "-f":
				rest = append(rest, "--follow")
			case "--lines":
				if i+1 >= len(args) {
					return usage
				}
				if _, err := strconv.Atoi(args[i+1]); err != nil {
					return fmt.Errorf("--lines %q: a number", args[i+1])
				}
				rest = append(rest, "--lines", args[i+1])
				i++
			default:
				return usage
			}
		}
		if err := a.needUp(ctx, o); err != nil {
			return err
		}
		var errb bytes.Buffer
		err := a.Host.Exec.Run(ctx, host.Cmd{Args: procCmd(o, nil, rest...), Stdout: a.Stdout, Stderr: &errb})
		return procError(o, err, errb.String())
	}
	return usage
}

func procWhen(state string, started *string, exit *int) string {
	switch {
	case state == "running" && started != nil:
		return " since " + *started
	case exit != nil:
		return fmt.Sprintf(" (exit %d)", *exit)
	}
	return ""
}

func (a *App) procShow(p ops.Process, done string) error {
	if a.Output == OutputJSON {
		return a.writeJSON(p)
	}
	if done != "" {
		sayf(a.Stdout, "%s %s in %s.\n", done, p.Name, p.Org)
	}
	sayf(a.Stdout, "%s  %s%s, %d restarts\n  %s\n", p.Name, p.State, procWhen(p.State, p.StartedAt, p.ExitCode), p.Restarts, strings.Join(p.Argv, " "))
	if len(p.EnvNames) > 0 {
		sayf(a.Stdout, "  variables: %s\n", strings.Join(p.EnvNames, " "))
	}
	return nil
}

// parseProcStart reads `[--cwd DIR] [--env K=V]... [--restart R] [--grace N] -- <command>...`.
// The `--` is required, so nothing of the command is ever read as berth's.
func parseProcStart(args []string) (ops.ProcSpec, error) {
	s := ops.ProcSpec{Env: map[string]string{}}
	for i := 0; i < len(args); i++ {
		x := args[i]
		if x == "--" {
			s.Argv = args[i+1:]
			return s, ops.CheckProcSpec(&s)
		}
		if !strings.HasPrefix(x, "--") {
			return s, errors.New("missing -- before the command")
		}
		if i+1 >= len(args) {
			return s, fmt.Errorf("%s needs a value", x)
		}
		v := args[i+1]
		i++
		switch x {
		case "--cwd":
			s.Cwd = v
		case "--env":
			k, val, ok := strings.Cut(v, "=")
			if !ok {
				return s, fmt.Errorf("--env %q: want KEY=VALUE", v)
			}
			s.Env[k] = val
		case "--restart":
			s.Restart = v
		case "--grace":
			n, err := strconv.Atoi(v)
			if err != nil {
				return s, fmt.Errorf("--grace %q: a number of seconds", v)
			}
			s.Grace = n
		default:
			return s, fmt.Errorf("unknown option %s (the command goes after --)", x)
		}
	}
	return s, errors.New("missing -- before the command")
}

// ProcStart starts a supervised process in o's running container: berth-proc gets the spec, the
// variables' values with it, on its stdin, never on a command line, and runs it as the
// container's user with the org's own variables loaded, as `berth exec` does.
func (a *App) ProcStart(ctx context.Context, o, name string, spec ops.ProcSpec) (ops.Process, error) {
	if err := ops.CheckProcName(name); err != nil {
		return ops.Process{}, err
	}
	if err := ops.CheckProcSpec(&spec); err != nil {
		return ops.Process{}, err
	}
	if err := a.needUp(ctx, o); err != nil {
		return ops.Process{}, err
	}
	in, err := json.Marshal(spec)
	if err != nil {
		return ops.Process{}, err
	}
	raw, err := a.procRun(ctx, o, in, "start", name)
	if err != nil {
		return ops.Process{}, err
	}
	return ops.ParseProcess(o, raw)
}

// ProcAct is show or stop for one of o's processes: its state, or its last state once stopped.
func (a *App) ProcAct(ctx context.Context, o, verb, name string) (ops.Process, error) {
	if err := ops.CheckProcName(name); err != nil {
		return ops.Process{}, err
	}
	if err := a.needUp(ctx, o); err != nil {
		return ops.Process{}, err
	}
	raw, err := a.procRun(ctx, o, nil, map[string]string{"show": "status", "stop": "stop"}[verb], name)
	if err != nil {
		return ops.Process{}, err
	}
	return ops.ParseProcess(o, raw)
}

// procCmd is berth-proc's command line in o's container, as its user; with a spec on stdin
// (-i), after the org's secrets are loaded (execLoader).
func procCmd(o string, stdin []byte, args ...string) []string {
	flags := []string{"docker", "exec", "-u", ops.ProcUser}
	if stdin != nil {
		flags = append(flags, "-i")
	}
	return append(append(flags, contract.Container(o)), append([]string{ops.ProcBin}, args...)...)
}

// procRun runs berth-proc and returns what it printed.
func (a *App) procRun(ctx context.Context, o string, stdin []byte, args ...string) (string, error) {
	argv := procCmd(o, stdin, args...)
	if stdin != nil {
		// The org's variables, loaded as for `berth exec`, so the process has them too.
		head := len(argv) - len(args) - 1
		argv = append(append([]string{}, argv[:head]...), a.execLoader(o, argv[head:]...)...)
	}
	var out, errb bytes.Buffer
	c := host.Cmd{Args: argv, Stdout: &out, Stderr: &errb}
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	if err := a.Host.Exec.Run(ctx, c); err != nil {
		return "", procError(o, err, errb.String())
	}
	return out.String(), nil
}

// procError is berth-proc's failure as an error with its kind (ops.ProcErr).
func procError(o string, err error, stderr string) error {
	if err == nil {
		return nil
	}
	var code interface{ ExitCode() int }
	if errors.As(err, &code) {
		return ops.ProcErr(o, code.ExitCode(), stderr)
	}
	return err
}
