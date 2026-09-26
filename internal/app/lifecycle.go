package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/ar4mirez/berth/internal/contract"
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
	a.imageFromRelease(ctx) // a release pulls its image here; otherwise compose builds it if missing
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
	return a.execIn(ctx, true, []string{"-it", "-u", "node"}, o, "tmux", "new-session", "-A", "-s", contract.TmuxSession, "-c", contract.Workspace)
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
	return a.execIn(ctx, true, []string{"-it", "-u", "node", "-w", "/workspace"}, o, a.execLoader(o, append([]string{"claude"}, args...)...)...)
}

// Run is `ccenv run <org> "<prompt>" [args...]`: headless claude -p, stdin passed (docker exec -i).
func (a *App) Run(ctx context.Context, o string, args []string) error {
	if err := a.interactive(ctx, o); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("missing prompt")
	}
	return a.execIn(ctx, false, []string{"-i", "-u", "node", "-w", "/workspace"}, o, a.execLoader(o, append([]string{"claude", "-p", args[0]}, args[1:]...)...)...)
}

// Up is `ccenv up <org>`: build if needed and recreate, then `sleep 3` and the last 5 lines of
// `docker logs claude-<org> 2>&1`. The command ends with docker logs' exit code (pipefail).
func (a *App) Up(ctx context.Context, o string) error {
	if err := a.needOwnedOrg(o); err != nil {
		return err
	}
	// ccenv's up always builds (--build). A release that pulled its image doesn't: building would
	// replace the pulled image with a local build of the same image/.
	args := []string{"up", "-d", "--build", "--force-recreate"}
	if a.imageFromRelease(ctx) {
		args = []string{"up", "-d", "--force-recreate"}
	}
	if err := a.compose(ctx, o, args...); err != nil {
		return err
	}
	if err := a.passthrough(ctx, false, "sleep", "3"); err != nil {
		return err
	}
	var logs bytes.Buffer
	err := a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "logs", "claude-" + o}, Stdout: &logs, Stderr: &logs})
	_, _ = a.Stdout.Write(tail(logs.Bytes(), 5))
	return err
}

// Build is `ccenv build [docker build args...]`: build berth's image, tagged berth/claude-env:<hash>
// (ccenv tags claude-env), from berth's image dir. Arguments go to docker build.
func (a *App) Build(ctx context.Context, args []string) error {
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	uid, _ := a.capture(ctx, false, "id", "-u")
	gid, _ := a.capture(ctx, false, "id", "-g")
	argv := append([]string{"docker", "build", "-t", set.Tag, "--build-arg", "USER_UID=" + uid, "--build-arg", "USER_GID=" + gid}, args...)
	return a.passthrough(ctx, false, append(argv, set.ImageDir)...)
}

// tail is `tail -n N`: the last n lines, keeping whether the input ended with a newline.
func tail(b []byte, n int) []byte {
	end := len(b)
	if end > 0 && b[end-1] == '\n' {
		end--
	}
	start := end
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(b[:start], '\n')
		if j < 0 {
			return b
		}
		start = j
		if i == n-1 {
			return b[start+1:]
		}
	}
	return b[start+1:]
}
