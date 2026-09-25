package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ar4mirez/berth/internal/host"
)

// needOwnedOrg is needOrg for commands that change an org or act as it: the org must be managed by
// berth (MANAGER=berth). Legacy orgs have no MANAGER line, which means ccenv, so berth leaves the
// live orgs alone until cutover sets MANAGER=berth (plan, "Rules for coexisting with legacy").
// ccenv refuses berth's orgs the same way.
func (a *App) needOwnedOrg(o string) error {
	if err := a.needOrg(o); err != nil {
		return err
	}
	if m := a.env(o, "MANAGER"); m != "berth" {
		if m == "" {
			m = "unset, so ccenv"
		}
		return fmt.Errorf("org '%s' is managed by ccenv (MANAGER=%s); use: ccenv ... %s", o, m, o)
	}
	return nil
}

// needUp is ccenv's need_up.
func (a *App) needUp(ctx context.Context, o string) error {
	if !a.running(ctx, o) {
		return fmt.Errorf("claude-%s is not running (%s up %s)", o, Tool, o)
	}
	return nil
}

// Down is `ccenv down <org>`: compose down.
func (a *App) Down(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	return a.compose(ctx, o, "down")
}

// Restart is `ccenv restart <org>`: recreate the container.
func (a *App) Restart(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	return a.compose(ctx, o, "up", "-d", "--force-recreate")
}

// execIn runs `docker exec <flags> claude-<org> <cmd>` on berth's own stdio. tty is `-it`: the
// operator's terminal is handed to docker (no pty in berth). The exit code passes through.
func (a *App) execIn(ctx context.Context, tty bool, dockerFlags []string, o string, cmd ...string) error {
	argv := append(append([]string{"docker", "exec"}, dockerFlags...), "claude-"+o)
	return a.Host.Exec.Run(ctx, host.Cmd{
		Args: append(argv, cmd...), TTY: tty,
		Stdin: a.Stdin, Stdout: a.Stdout, Stderr: a.Stderr,
	})
}

// interactive checks the org is berth's and up, as attach/shell/claude/run do.
func (a *App) interactive(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	return a.needUp(ctx, o)
}

// Attach is `ccenv attach <org>`: the shared tmux session "main".
func (a *App) Attach(ctx context.Context, o string) error {
	if err := a.interactive(ctx, o); err != nil {
		return err
	}
	return a.execIn(ctx, true, []string{"-it", "-u", "node"}, o, "tmux", "new-session", "-A", "-s", "main", "-c", "/workspace")
}

// Shell is `ccenv shell <org>`: a login bash in /workspace.
func (a *App) Shell(ctx context.Context, o string) error {
	if err := a.interactive(ctx, o); err != nil {
		return err
	}
	return a.execIn(ctx, true, []string{"-it", "-u", "node", "-w", "/workspace"}, o, "bash", "-l")
}

// Claude is `ccenv claude <org> [args...]`: interactive claude in /workspace, args passed through.
func (a *App) Claude(ctx context.Context, o string, args []string) error {
	if err := a.interactive(ctx, o); err != nil {
		return err
	}
	return a.execIn(ctx, true, []string{"-it", "-u", "node", "-w", "/workspace"}, o, append([]string{"claude"}, args...)...)
}

// Run is `ccenv run <org> "<prompt>" [args...]`: headless claude -p, stdin passed (docker exec -i).
func (a *App) Run(ctx context.Context, o string, args []string) error {
	if err := a.interactive(ctx, o); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("missing prompt")
	}
	return a.execIn(ctx, false, []string{"-i", "-u", "node", "-w", "/workspace"}, o, append([]string{"claude", "-p", args[0]}, args[1:]...)...)
}
