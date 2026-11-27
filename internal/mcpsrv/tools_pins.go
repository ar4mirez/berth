package mcpsrv

import (
	"context"
	"errors"

	"github.com/ar4mirez/berth/internal/ops"
)

// Pinned tools (#216): one exact version of a tool, installed with the image's mise at the org's
// next up or restart, which this doesn't do.

type toolSetArgs struct {
	Org     string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name    string `json:"name" jsonschema:"the tool's name: lowercase letters, digits and dashes, up to 32"`
	Source  string `json:"source" jsonschema:"a mise backend whose downloads mise verifies: github:owner/repo or aqua:owner/repo"`
	Version string `json:"version" jsonschema:"one exact version, as in 0.6.0: no range, no latest"`
}

func (a toolSetArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name, "source": a.Source, "version": a.Version}
}

type toolRemoveArgs struct {
	Org  string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name string `json:"name" jsonschema:"the tool's name, as tools_list shows it"`
}

func (a toolRemoveArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name}
}

func (s *server) toolPinTools() {
	add(s, "tools_list", "tool", "ls", "An org's pinned tools: each one's mise source and exact version, and whether the running container installed it at its start.",
		func(ctx context.Context, c *call, in orgArg) (ops.Tools, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Tools{}, err
			}
			out, err := ops.GetTools(ctx, b, o)
			out.Org = in.Org
			return out, err
		})
	add(s, "tool_set", "tool", "set", "Pin a tool to one exact version, installed with the image's mise, its checks on, at the org's next up or restart, which fails if it doesn't install. This doesn't restart the org.",
		func(ctx context.Context, c *call, in toolSetArgs) (ops.Tools, error) {
			return c.toolEdit(ctx, in.Org, []string{"set", in.Name, in.Source, in.Version})
		})
	add(s, "tool_remove", "tool", "rm", "Stop installing one of an org's pinned tools at its start. This doesn't restart the org.",
		func(ctx context.Context, c *call, in toolRemoveArgs) (ops.Tools, error) {
			return c.toolEdit(ctx, in.Org, []string{"rm", in.Name})
		})
}

func (c *call) toolEdit(ctx context.Context, org string, args []string) (ops.Tools, error) {
	b, o, err := c.at(ctx, org)
	if err != nil {
		return ops.Tools{}, err
	}
	for _, a := range args[1:] {
		if a == "" {
			return ops.Tools{}, errors.New("name, source and version are required")
		}
		if err := flagLike("an argument", a); err != nil {
			return ops.Tools{}, err
		}
	}
	if err := b.Tool(ctx, o, args); err != nil {
		return ops.Tools{}, err
	}
	out, err := ops.GetTools(ctx, b, o)
	out.Org = org
	return out, err
}
