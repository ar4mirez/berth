package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/ops"
)

// Pinned tools (#216, internal/ops/tools.go and image/berth-tools; berth-only): one exact version
// of a tool, installed with the image's mise at the org's start.

// toolWrites are the tool subcommands that change the list.
var toolWrites = map[string]bool{"set": true, "rm": true, "remove": true}

// toolsWait is how long an up or restart waits for the org's pinned tools to install.
var toolsWait = 10 * time.Minute

// Tool is `berth tool <org> [ls | set <name> <source> <version> | rm <name>]`.
func (a *App) Tool(ctx context.Context, o string, args []string) error {
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	usage := fmt.Errorf("usage: %s tool <org> [ls | set <name> <github:owner/repo|aqua:owner/repo> <version> | rm <name>]", Tool)
	for _, x := range args {
		if strings.HasPrefix(x, "-") {
			return fmt.Errorf("unknown flag %s", x)
		}
	}
	if toolWrites[sub] {
		if err := a.writable(o, "change "+o+"'s tools"); err != nil {
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
		t, err := ops.GetTools(ctx, a, o)
		if err != nil {
			return err
		}
		if a.Output == OutputJSON {
			return a.writeJSON(t)
		}
		if len(t.Tools) == 0 {
			sayf(a.Stdout, "(no pinned tools; pin one: %s tool %s set <name> github:<owner>/<repo> <version>)\n", Tool, o)
		}
		for _, x := range t.Tools {
			state := "installed"
			if !x.Installed {
				state = "at the org's next start"
			}
			sayf(a.Stdout, "%s  %s@%s  (%s)\n", x.Name, x.Source, x.Version, state)
		}
		return nil
	case "set":
		if len(args) != 3 {
			return usage
		}
		return a.toolEdit(ctx, o, ops.PinnedTool{Name: args[0], Source: args[1], Version: args[2]}, false)
	case "rm", "remove":
		if len(args) != 1 {
			return usage
		}
		return a.toolEdit(ctx, o, ops.PinnedTool{Name: args[0]}, true)
	}
	return usage
}

func (a *App) toolEdit(ctx context.Context, o string, t ops.PinnedTool, remove bool) error {
	if !remove {
		if err := ops.CheckTool(t); err != nil {
			return err
		}
	}
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	ts, err := ops.ReadTools(a, o)
	if err != nil {
		return err
	}
	kept := ts[:0]
	for _, x := range ts {
		if x.Name != t.Name {
			kept = append(kept, x)
		}
	}
	if remove && len(kept) == len(ts) {
		return &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: fmt.Sprintf("%s has no pinned tool %q", o, t.Name), Hint: "run: " + Tool + " tool " + o + " ls"}
	}
	if !remove {
		kept = append(kept, t)
	}
	f := ops.ToolsFile(a, o)
	if len(kept) == 0 {
		if err := a.Host.FS.Remove(f); err != nil && a.isFile(f) {
			return err
		}
	} else if err := a.Host.FS.WriteFileAtomic(f, ops.EncodeTools(o, kept), 0o644); err != nil {
		return err
	}
	if remove {
		sayf(a.Stdout, "Removed %s. Nothing was restarted; the org's next start no longer installs it: %s restart %s\n", t.Name, Tool, o)
	} else {
		sayf(a.Stdout, "Pinned %s to %s@%s. Nothing was restarted; it is installed at the org's next start: %s restart %s\n", t.Name, t.Source, t.Version, Tool, o)
	}
	return nil
}

// waitTools waits, after an up or restart, for the org's pinned tools to install (berth-tools in
// the entrypoint) and fails with the reason when one didn't. An org with none waits for nothing.
func (a *App) waitTools(ctx context.Context, o string) error {
	ts, err := ops.ReadTools(a, o)
	if err != nil || len(ts) == 0 {
		return err
	}
	a.Progress.Step("tools", fmt.Sprintf("installing %d pinned tools", len(ts)))
	deadline := time.Now().Add(toolsWait)
	for {
		res, done := ops.ParseToolsStatus(ops.ReadToolsStatus(ctx, a, o))
		if done {
			var failed []string
			for _, t := range ts {
				r, ok := res[t.Name]
				switch {
				case !ok || r.Spec != t.Source+"@"+t.Version:
					failed = append(failed, t.Name+": not installed (the container read another list)")
				case !r.OK:
					failed = append(failed, t.Name+" ("+r.Spec+"): "+r.Reason)
				}
			}
			if len(failed) > 0 {
				return &ops.Error{Kind: ops.KindFailed, Code: 1, Msg: o + "'s pinned tools didn't all install: " + strings.Join(failed, "; "),
					Hint: "the org is running without them; fix the pin (" + Tool + " tool " + o + " set|rm) or its firewall, then " + Tool + " restart " + o}
			}
			return nil
		}
		if time.Now().After(deadline) {
			return &ops.Error{Kind: ops.KindFailed, Code: 1, Msg: fmt.Sprintf("%s's pinned tools weren't installed after %s", o, toolsWait),
				Hint: "see /run/berth/tools.log in the org"}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
