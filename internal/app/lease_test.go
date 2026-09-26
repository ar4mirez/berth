package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
	"github.com/ar4mirez/berth/internal/hosts"
)

// TestLease: the active-host lease, with two state roots standing in for this machine and box1
// (#47; test/integration's TestLease runs it on a real second host, with containers).
func TestLease(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	stA, stB := filepath.Join(home, "a"), filepath.Join(home, "b")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	newOrg := func(state, o string) {
		must(os.MkdirAll(filepath.Join(state, "orgs", o), 0o700))
		must(os.WriteFile(filepath.Join(state, "orgs", o, "org.env"), []byte("MANAGER=berth\n"), 0o600))
	}
	var out bytes.Buffer
	base := New(config.State{Home: config.Home{Path: stA}}, local.New(), nil, &out, &out, func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	})
	box := base.On("box1", local.New(), stB)
	base.HostAppFn = func(_ context.Context, name string) (*App, func(), error) {
		if name == "box1" {
			return box, func() {}, nil
		}
		return base, func() {}, nil
	}
	box.HostAppFn = base.HostAppFn
	p := base.hostPaths()
	leases := func() map[string]string {
		l, err := hosts.LoadLeases(base.Operator.FS, p)
		must(err)
		return l.Orgs
	}
	exists := func(f string) bool { _, err := os.Stat(f); return err == nil }

	// No registered host: no lease, nothing written.
	newOrg(stA, "acme")
	must(base.checkLease(ctx, "acme"))
	if exists(p.Leases()) || exists(hosts.LeaseMarker(stA, "acme")) {
		t.Fatal("a lease was written with no registered host")
	}

	must(hosts.Save(base.Operator.FS, p, &hosts.Registry{Hosts: []hosts.Entry{{Name: "box1", Kind: hosts.KindSSH, User: "ops", Addr: "192.0.2.1:22", Home: stB, Key: p.Key("box1")}}}))

	// An org on one host only gets the lease there on its first up.
	must(base.checkLease(ctx, "acme"))
	if leases()["acme"] != "local" || !exists(hosts.LeaseMarker(stA, "acme")) {
		t.Fatalf("auto-claim: %v", leases())
	}
	must(base.checkLease(ctx, "acme")) // the holder, again

	// A copy on box1 is refused, naming the holder.
	newOrg(stB, "acme")
	if err := box.checkLease(ctx, "acme"); err == nil || !strings.Contains(err.Error(), "runs on local") || !strings.Contains(err.Error(), "acme@box1 --take-lease") {
		t.Fatalf("non-holder: %v", err)
	}
	// --take-lease moves it (nothing runs on local here, so nothing is stopped).
	box.TakeLease = true
	out.Reset()
	must(box.checkLease(ctx, "acme"))
	box.TakeLease = false
	if leases()["acme"] != "box1" || exists(hosts.LeaseMarker(stA, "acme")) || !exists(hosts.LeaseMarker(stB, "acme")) {
		t.Fatalf("take-lease: %v", leases())
	}
	if !strings.Contains(out.String(), "acme's lease is now on box1") {
		t.Errorf("take-lease output: %s", out.String())
	}
	if err := base.checkLease(ctx, "acme"); err == nil || !strings.Contains(err.Error(), "runs on box1") {
		t.Errorf("old holder after take-lease: %v", err)
	}

	// No lease yet, but the org is on both: say which.
	newOrg(stA, "globex")
	newOrg(stB, "globex")
	if err := base.checkLease(ctx, "globex"); err == nil || !strings.Contains(err.Error(), "also on box1") {
		t.Errorf("on both, no lease: %v", err)
	}
	if _, ok := leases()["globex"]; ok {
		t.Error("a refused check recorded a lease")
	}

	// host rm drops the removed host's leases; a lease on a host berth no longer knows just moves.
	if orgs, err := base.dropLeases("box1"); err != nil || len(orgs) != 1 || orgs[0] != "acme" {
		t.Errorf("dropLeases: %v %v", orgs, err)
	}
	l, _ := hosts.LoadLeases(base.Operator.FS, p)
	l.Orgs["acme"] = "gone"
	must(hosts.SaveLeases(base.Operator.FS, p, l))
	base.TakeLease = true
	out.Reset()
	must(base.checkLease(ctx, "acme"))
	if leases()["acme"] != "local" || !strings.Contains(out.String(), "no longer a registered host") {
		t.Errorf("stale lease: %v\n%s", leases(), out.String())
	}
}
