package app

import (
	"context"
	"path"

	"github.com/ar4mirez/berth/internal/assets"
	"github.com/ar4mirez/berth/internal/ops"
)

// App is the ops.System its commands run against (internal/ops): each method is the helper the
// commands already use, so an operation there makes the same calls as the command did.
var _ ops.System = (*App)(nil)

// Capture is ops.System's `$(cmd)`.
func (a *App) Capture(ctx context.Context, quiet bool, args ...string) (string, error) {
	return a.capture(ctx, quiet, args...)
}

// CaptureRaw is Capture with the trailing newlines kept.
func (a *App) CaptureRaw(ctx context.Context, quiet bool, args ...string) (string, error) {
	return a.captureRaw(ctx, quiet, args...)
}

// Succeeds runs a command for its exit status.
func (a *App) Succeeds(ctx context.Context, quiet bool, args ...string) bool {
	return a.passthrough(ctx, quiet, args...) == nil
}

// ReadFile reads a file on the org's host.
func (a *App) ReadFile(p string) ([]byte, error) { return a.Host.FS.ReadFile(p) }

// IsFile is `[ -f p ]`.
func (a *App) IsFile(p string) bool { return a.isFile(p) }

// IsDir is `[ -d p ]`.
func (a *App) IsDir(p string) bool { return a.isDir(p) }

// OrgsDir is <state root>/orgs.
func (a *App) OrgsDir() string { return a.Orgs.Dir }

// OrgDirs are the directories under OrgsDir, in byte order.
func (a *App) OrgDirs() []string { return a.orgDirs() }

// EnvGet is a key in an org's org.env.
func (a *App) EnvGet(o, key string) (string, bool) {
	v, ok, _ := a.Orgs.Get(o, key)
	return v, ok
}

// Secret is a variable's value wherever the org keeps it.
func (a *App) Secret(o, key string) string { return a.secret(o, key) }

// SecretFile reports whether the org keeps the variable as a file (#37).
func (a *App) SecretFile(o, key string) bool {
	return a.migrated(o) && a.exists(path.Join(a.secretsDir(o), key))
}

// OperatorEnv is a variable in the operator's environment.
func (a *App) OperatorEnv(key string) string { return a.Getenv(key) }

// HostLabel is where these orgs are.
func (a *App) HostLabel() string { return a.hostLabel() }

// Peers are the registered hosts, each dialed when a listing gets to it.
func (a *App) Peers(_ context.Context) []ops.Peer {
	var peers []ops.Peer
	for _, e := range a.otherHosts() {
		peers = append(peers, ops.Peer{Name: e.Name, Open: func(ctx context.Context) (ops.System, func(), error) {
			h, err := a.dialHost(ctx, e, e.Key)
			if err != nil {
				return nil, nil, err
			}
			return a.On(e.Name, h, e.Home), func() { _ = h.Close() }, nil
		}})
	}
	return peers
}

// Destroyed are the orgs `destroy` removed here.
func (a *App) Destroyed() []ops.DestroyedOrg { return a.destroyedOrgs() }

// Address is host_addr.
func (a *App) Address(ctx context.Context, o string) string { return a.hostAddr(ctx, o) }

// DefaultOrg is the saved default org.
func (a *App) DefaultOrg() string { return a.ContextOrg() }

// Image is the tag of the image this berth runs its orgs on.
func (a *App) Image() (string, error) {
	set, err := assets.Embedded(a.State.Home.Path) // computed, never written: this only reads
	return set.Tag, err
}

// Tunnel is how to reach an org through an SSH tunnel (#58): one bound to localhost, or to
// 127.0.0.1 on a registered host. nil for any other org.
func (a *App) Tunnel(ctx context.Context, o, _, _ string) *ops.Tunnel {
	// Only these can need a tunnel, and only they resolve the bind again (ccenv's sheet makes no
	// other calls: the parity suite checks it).
	if a.env(o, "BIND_ADDR") != BindLocalhost && a.HostName == "" {
		return nil
	}
	if b, err := a.resolveBind(ctx, o); err != nil || b != "127.0.0.1" {
		return nil
	}
	t := &ops.Tunnel{Connect: o}
	if a.HostName != "" {
		if e, err := a.base().registered(a.HostName); err == nil {
			t.SSHTarget = sshTarget(e.User, e.Addr)
		}
		t.Connect = o + "@" + a.HostName
	} else {
		h, _ := a.capture(ctx, true, "hostname")
		t.SSHTarget = a.Getenv("USER") + "@" + h
	}
	return t
}
