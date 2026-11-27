package mcpsrv

import (
	"context"
	"errors"

	"github.com/ar4mirez/berth/internal/ops"
)

// Socket mounts (#216): a Unix socket of the org's host, in its container under /run/berth-mounts/,
// from the org's next up or restart. Setting one only writes the org's list: the restart that
// applies it is its own operation, with its own confirm.

type mountSetArgs struct {
	Org    string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name   string `json:"name" jsonschema:"the mount's name: lowercase letters, digits and dashes, up to 32"`
	Source string `json:"source" jsonschema:"the absolute path of an existing Unix socket on the org's host, owned by berth's user, in a directory of its own that nobody else may write to; nothing else is accepted"`
	Target string `json:"target" jsonschema:"where it is in the container: an absolute path under /run/berth-mounts/, in a directory of its own, ending in the socket's own name (as in /run/berth-mounts/agent/agent.sock)"`
	Mode   string `json:"mode,omitempty" jsonschema:"rw, the only mode (a socket needs it); the default"`
}

func (a mountSetArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name, "source": a.Source, "target": a.Target, "mode": a.Mode}
}

type mountRemoveArgs struct {
	Org  string `json:"org" jsonschema:"the org, as in acme, or acme@box1 for one on a registered host"`
	Name string `json:"name" jsonschema:"the mount's name, as mounts_list shows it"`
}

func (a mountRemoveArgs) auditArgs() map[string]any {
	return map[string]any{"org": a.Org, "name": a.Name}
}

func init() {
	// A socket mount gives an org a daemon of its host: for a person to grant through the API, never
	// for an agent to give itself.
	noMCP["mount_set"], noMCP["mount_remove"] = true, true
}

func (s *server) mountTools() {
	add(s, "mounts_list", "mount", "ls", "An org's socket mounts: each Unix socket from its host, where it is in the container, and whether the running container has it yet.",
		func(ctx context.Context, c *call, in orgArg) (ops.Mounts, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Mounts{}, err
			}
			return ops.GetMounts(ctx, b, o)
		})
	add(s, "mount_set", "mount", "add", "Mount a Unix socket of the org's host in its container, under /run/berth-mounts/: its directory is what is mounted, so a daemon that makes the socket again is seen. Saved for the org; it applies at the org's next up or restart, which this doesn't do.",
		func(ctx context.Context, c *call, in mountSetArgs) (ops.MountDoc, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.MountDoc{}, err
			}
			if in.Mode == "" {
				in.Mode = "rw"
			}
			if in.Mode != "rw" {
				return ops.MountDoc{}, ops.Usage("mode %q: a socket is mounted rw, and nothing else is taken", in.Mode)
			}
			for what, v := range map[string]string{"name": in.Name, "source": in.Source, "target": in.Target} {
				if v == "" {
					return ops.MountDoc{}, errors.New(what + " is required")
				}
				if err := flagLike(what, v); err != nil {
					return ops.MountDoc{}, err
				}
			}
			if err := b.Mount(ctx, o, []string{"add", in.Name, in.Source, in.Target}); err != nil {
				return ops.MountDoc{}, err
			}
			doc := ops.MountDoc{Schema: "berth.mount/v1", Org: in.Org, Name: in.Name, Source: in.Source, Target: in.Target, Mode: in.Mode}
			ms, err := ops.GetMounts(ctx, b, o)
			for _, m := range ms.Mounts {
				if m.Name == in.Name {
					doc.Applied = m.Applied
				}
			}
			return doc, err
		})
	add(s, "mount_remove", "mount", "rm", "Remove one of an org's socket mounts. It leaves the container at the org's next up or restart, which this doesn't do.",
		func(ctx context.Context, c *call, in mountRemoveArgs) (ops.Mounts, error) {
			b, o, err := c.at(ctx, in.Org)
			if err != nil {
				return ops.Mounts{}, err
			}
			if in.Name == "" {
				return ops.Mounts{}, errors.New("name is required")
			}
			if err := flagLike("name", in.Name); err != nil {
				return ops.Mounts{}, err
			}
			if err := b.Mount(ctx, o, []string{"rm", in.Name}); err != nil {
				return ops.Mounts{}, err
			}
			out, err := ops.GetMounts(ctx, b, o)
			out.Org = in.Org
			return out, err
		})
}
