package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// execOpts are the options `claude` and `exec` take before `--`: where the command starts, and
// variables it gets on top of the org's.
type execOpts struct {
	cwd string   // a directory in the container, inside the workspace
	env []string // KEY=VALUE
}

// parseExecOpts reads `[--cwd DIR] [--env K=V]... --` from the start of args, and returns the rest
// (after `--`). The `--` is required, so nothing after it is ever read as berth's.
func parseExecOpts(args []string) (execOpts, []string, error) {
	o := execOpts{cwd: contract.Workspace}
	for i := 0; i < len(args); i++ {
		switch x := args[i]; x {
		case "--":
			return o, args[i+1:], nil
		case "--cwd", "--env":
			if i+1 >= len(args) {
				return o, nil, fmt.Errorf("%s needs a value", x)
			}
			v := args[i+1]
			i++
			if x == "--env" {
				k, _, ok := strings.Cut(v, "=")
				if !ok || !envKey.MatchString(k) {
					return o, nil, fmt.Errorf("--env %q: want KEY=VALUE, KEY like OTEL_RESOURCE_ATTRIBUTES", v)
				}
				o.env = append(o.env, v)
				continue
			}
			if !path.IsAbs(v) {
				v = path.Join(contract.Workspace, v)
			}
			v = path.Clean(v)
			if v != contract.Workspace && !strings.HasPrefix(v, contract.Workspace+"/") {
				return o, nil, fmt.Errorf("--cwd %s: must be inside %s", args[i], contract.Workspace)
			}
			o.cwd = v
		default:
			return o, nil, fmt.Errorf("unknown option %s (the command goes after --)", x)
		}
	}
	return o, nil, errors.New("missing -- before the command")
}

// command is cmd with the options' variables set for it. They go after the org's secrets are
// loaded (execLoader), so a variable given here wins over the org's own.
func (e execOpts) command(o string, a *App, cmd ...string) []string {
	if len(e.env) > 0 {
		cmd = append(append([]string{"env"}, e.env...), cmd...)
	}
	return a.execLoader(o, cmd...)
}

// Exec is `berth exec <org> [--cwd DIR] [--env K=V]... -- <command> [args...]`: a command in the
// container as node, not interactive (stdin is passed, as `run` does), with the org's variables.
// It ends with the command's exit code. It reaches nothing `shell` doesn't; it's for tools
// (crew start) that need to run a step in the org.
func (a *App) Exec(ctx context.Context, o string, args []string) error {
	if err := a.interactive(ctx, o); err != nil {
		return err
	}
	opts, cmd, err := parseExecOpts(args)
	if err != nil {
		return fmt.Errorf("%w; usage: %s exec <org> [--cwd DIR] [--env K=V]... -- <command> [args...]", err, Tool)
	}
	if len(cmd) == 0 {
		return fmt.Errorf("missing command; usage: %s exec <org> [--cwd DIR] [--env K=V]... -- <command> [args...]", Tool)
	}
	return a.execIn(ctx, false, []string{"-i", "-u", "node", "-w", opts.cwd}, o, opts.command(o, a, cmd...)...)
}
