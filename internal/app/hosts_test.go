package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
	"github.com/ar4mirez/berth/internal/hosts"
	"github.com/ar4mirez/berth/internal/ops"
)

// TestHostCommandsOffline: what berth host refuses before connecting anywhere (#44; the SSH paths
// are in test/integration's TestHostRegistry).
func TestHostCommandsOffline(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	newApp := func(ro bool) (*App, *bytes.Buffer) {
		var out bytes.Buffer
		st := config.State{Home: config.Home{Path: filepath.Join(home, "state")}, ReadOnly: ro}
		return New(st, local.New(), nil, &out, &out, func(k string) string {
			switch k {
			case "HOME":
				return home
			case "USER":
				return "op"
			}
			return ""
		}), &out
	}
	a, _ := newApp(false)
	p := a.hostPaths()
	if p.Dir != home+"/.config/berth" {
		t.Fatalf("paths: %+v", p)
	}
	reg := &hosts.Registry{Hosts: []hosts.Entry{{Name: "box1", Kind: hosts.KindSSH, User: "ops", Addr: "192.0.2.1:22", Home: "/srv/berth", Key: p.Key("box1")}}}
	if err := hosts.Save(a.Host.FS, p, reg); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		run  func(*App) error
		want string
	}{
		{"add: usage", func(a *App) error { return a.HostAdd(ctx, []string{"box2"}) }, "usage"},
		{"add: bad name", func(a *App) error { return a.HostAdd(ctx, []string{"Box", "h"}) }, "invalid host name"},
		{"add: local", func(a *App) error { return a.HostAdd(ctx, []string{"local", "h"}) }, "always registered"},
		{"add: relative home", func(a *App) error { return a.HostAdd(ctx, []string{"box2", "h", "--home", "x"}) }, "absolute"},
		{"add: fingerprint", func(a *App) error { return a.HostAdd(ctx, []string{"box2", "h", "--fingerprint", "MD5:x"}) }, "SHA256"},
		{"add: twice", func(a *App) error { return a.HostAdd(ctx, []string{"box1", "h"}) }, "already registered"},
		{"rm: local", func(a *App) error { return a.HostRm(ctx, []string{"local"}) }, "this machine"},
		{"rm: unknown", func(a *App) error { return a.HostRm(ctx, []string{"nope"}) }, "unknown host"},
		{"rotate: unknown", func(a *App) error { return a.HostRotateAccess(ctx, []string{"nope"}) }, "unknown host"},
		{"ls: args", func(a *App) error { return a.HostLs(ctx, []string{"x"}) }, "usage"},
	} {
		a, _ := newApp(false)
		if err := c.run(a); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	for name, run := range map[string]func(*App) error{
		"add":    func(a *App) error { return a.HostAdd(ctx, []string{"box2", "h"}) },
		"rm":     func(a *App) error { return a.HostRm(ctx, []string{"box1"}) },
		"rotate": func(a *App) error { return a.HostRotateAccess(ctx, []string{"box1"}) },
	} {
		a, _ := newApp(true)
		if err := run(a); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s under --read-only: %v", name, err)
		}
	}

	// rm of an unreachable host needs --force; with it, the host and its files go.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Dir(p.Key("box1")), 0o700))
	must(os.WriteFile(p.Key("box1"), []byte("not a key"), 0o600))
	a, _ = newApp(false)
	if err := a.HostRm(ctx, []string{"box1"}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("rm unreachable: %v", err)
	}

	// ls under --read-only still reads: local first, the unreachable host with its error.
	a, out := newApp(true)
	a.Output = OutputJSON
	must(a.HostLs(ctx, nil))
	var res ops.Hosts
	must(json.Unmarshal(out.Bytes(), &res))
	if len(res.Hosts) != 2 || res.Hosts[0].Name != "local" || res.Hosts[0].Kind != "local" || res.Hosts[1].Reachable || res.Hosts[1].Error == "" || res.Hosts[1].Orgs != nil {
		t.Errorf("ls: %+v", res)
	}

	a, out = newApp(false)
	must(a.HostRm(ctx, []string{"box1", "--force"}))
	if !strings.Contains(out.String(), "remove the line ending \"berth:box1\"") {
		t.Errorf("rm --force output:\n%s", out)
	}
	if _, err := os.Stat(p.Key("box1")); err == nil {
		t.Error("key left behind")
	}
	if r, _ := hosts.Load(a.Host.FS, p); len(r.Hosts) != 0 {
		t.Errorf("still registered: %+v", r)
	}
}
