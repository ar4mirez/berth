package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// Socket mounts (#216, internal/ops/mounts.go; berth-only): a Unix socket on the org's host, in its
// container under /run/berth-mounts/, from the org's next up or restart.

// mountWrites are the mount subcommands that change the list.
var mountWrites = map[string]bool{"add": true, "rm": true, "remove": true}

// Mount is `berth mount <org> [ls | add <name> <source> <target> | rm <name>]`.
func (a *App) Mount(ctx context.Context, o string, args []string) error {
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	usage := fmt.Errorf("usage: %s mount <org> [ls | add <name> <socket> <target> | rm <name>]", Tool)
	for _, x := range args {
		if strings.HasPrefix(x, "-") {
			return fmt.Errorf("unknown flag %s", x)
		}
	}
	if mountWrites[sub] {
		if err := a.writable(o, "change "+o+"'s mounts"); err != nil {
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
		return a.mountLs(ctx, o)
	case "add":
		if len(args) != 3 {
			return usage
		}
		return a.mountAdd(ctx, o, ops.Mount{Name: args[0], Source: args[1], Target: args[2], Mode: "rw"})
	case "rm", "remove":
		if len(args) != 1 {
			return usage
		}
		return a.mountRm(ctx, o, args[0])
	}
	return usage
}

func (a *App) mountLs(ctx context.Context, o string) error {
	m, err := ops.GetMounts(ctx, a, o)
	if err != nil {
		return err
	}
	if a.Output == OutputJSON {
		return a.writeJSON(m)
	}
	if len(m.Mounts) == 0 {
		sayf(a.Stdout, "(no socket mounts; add one: %s mount %s add <name> <socket> %s/<name>/<socket's name>)\n", Tool, o, ops.MountsRoot)
		return nil
	}
	for _, x := range m.Mounts {
		state := "applied"
		if !x.Applied {
			state = "at the org's next start"
		}
		sayf(a.Stdout, "%s  %s -> %s  (%s)\n", x.Name, x.Source, x.Target, state)
	}
	if m.Pending {
		sayf(a.Stdout, "The running container's mounts differ from these: they apply with %s restart %s\n", Tool, o)
	}
	return nil
}

// saveMounts writes the org's list, or removes the file when it is empty.
func (a *App) saveMounts(o string, ms []ops.Mount) error {
	f := ops.MountsFile(a, o)
	if len(ms) == 0 {
		if err := a.Host.FS.Remove(f); err != nil && a.isFile(f) {
			return err
		}
		return nil
	}
	return a.Host.FS.WriteFileAtomic(f, ops.EncodeMounts(ms), 0o600)
}

func (a *App) mountAdd(ctx context.Context, o string, m ops.Mount) error {
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := ops.ReadMounts(a, o)
	if err != nil {
		return err
	}
	if err := ops.CheckMount(m, ms); err != nil {
		return err
	}
	if err := ops.CheckSocket(ctx, a, m.Source); err != nil {
		return err
	}
	kept := ms[:0]
	for _, x := range ms {
		if x.Name != m.Name {
			kept = append(kept, x)
		}
	}
	if err := a.saveMounts(o, append(kept, m)); err != nil {
		return err
	}
	doc := ops.MountDoc{Schema: "berth.mount/v1", Org: o, Name: m.Name, Source: m.Source, Target: m.Target, Mode: m.Mode}
	if now, err := ops.GetMounts(ctx, a, o); err == nil {
		for _, x := range now.Mounts {
			if x.Name == m.Name {
				doc.Applied = x.Applied
			}
		}
	}
	if a.Output == OutputJSON {
		return a.writeJSON(doc)
	}
	sayf(a.Stdout, "Mount %s: %s at %s. Nothing was restarted; it applies at the org's next start: %s restart %s\n", m.Name, m.Source, m.Target, Tool, o)
	return nil
}

func (a *App) mountRm(ctx context.Context, o, name string) error {
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := ops.ReadMounts(a, o)
	if err != nil {
		return err
	}
	kept := ms[:0]
	for _, x := range ms {
		if x.Name != name {
			kept = append(kept, x)
		}
	}
	if len(kept) == len(ms) {
		return &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: fmt.Sprintf("%s has no mount %q", o, name), Hint: "run: " + Tool + " mount " + o + " ls"}
	}
	if err := a.saveMounts(o, kept); err != nil {
		return err
	}
	if a.Output == OutputJSON {
		m, err := ops.GetMounts(ctx, a, o)
		if err != nil {
			return err
		}
		return a.writeJSON(m)
	}
	sayf(a.Stdout, "Removed mount %s. Nothing was restarted; it leaves at the org's next start: %s restart %s\n", name, Tool, o)
	return nil
}

// socketMounts are what compose gets for the org's socket mounts: the file that adds them to
// compose.yml's service (written in the org's directory under berth's state, "" when the org has
// none) and BERTH_SOCKET_MOUNTS, the same binds for the Engine API path (host.NewOrgSpec). Before
// a start every socket is checked again: a socket that is gone or no longer berth's own stops the
// start with the reason, before anything is stopped.
func (a *App) socketMounts(ctx context.Context, o string, starting bool) (file string, env []string, err error) {
	ms, err := ops.ReadMounts(a, o)
	if err != nil || len(ms) == 0 {
		return "", nil, err
	}
	if starting {
		for _, m := range ms {
			if err := ops.CheckMount(m, ms); err != nil {
				return "", nil, fmt.Errorf("mount %s: %w", m.Name, err)
			}
			if err := ops.CheckSocket(ctx, a, m.Source); err != nil {
				return "", nil, fmt.Errorf("mount %s: %w (fix it, or: %s mount %s rm %s)", m.Name, err, Tool, o, m.Name)
			}
		}
	}
	binds := make([][2]string, 0, len(ms))
	var y strings.Builder
	y.WriteString("# berth's socket mounts for this org (berth mount): made at each start, not edited.\nservices:\n  claude:\n    volumes:\n")
	for _, m := range ms {
		src, dst := m.Binding()
		binds = append(binds, [2]string{src, dst})
		q := func(s string) string { b, _ := json.Marshal(s); return string(b) } // JSON strings are YAML
		fmt.Fprintf(&y, "      - type: bind\n        source: %s\n        target: %s\n        bind:\n          create_host_path: false\n", q(src), q(dst))
	}
	b, _ := json.Marshal(binds)
	file = path.Join(a.orgContextDir(o), "mounts.compose.yml")
	if !a.State.ReadOnly {
		if err := a.Host.FS.MkdirAll(path.Dir(file), 0o755); err != nil {
			return "", nil, err
		}
		if err := a.Host.FS.WriteFileAtomic(file, []byte(y.String()), 0o644); err != nil {
			return "", nil, err
		}
	} else if !a.isFile(file) {
		return "", nil, nil
	}
	return file, []string{"BERTH_SOCKET_MOUNTS=" + string(b)}, nil
}
