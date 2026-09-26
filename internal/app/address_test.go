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

func TestSplitAddressAndRefuse(t *testing.T) {
	for in, want := range map[string][2]string{
		"acme": {"acme", ""}, "acme@box1": {"acme", "box1"}, "acme@local": {"acme", "local"}, "a@b@c": {"a@b", "c"}, "@box1": {"", "box1"},
	} {
		if o, h := SplitAddress(in); o != want[0] || h != want[1] {
			t.Errorf("%s: %s %s", in, o, h)
		}
	}
	if err := RefuseRemote("backup", "acme", "--all", "-o", "/tmp/x", "acme@local", "ops@example.com"); err != nil {
		t.Errorf("nothing remote: %v", err)
	}
	if err := RefuseRemote("backup", "acme", "globex@box1"); err == nil || !strings.Contains(err.Error(), "globex@box1") {
		t.Errorf("remote: %v", err)
	}
}

// TestAddressLocal: a bare org and org@local are this machine; an unknown host is refused before
// anything connects. ls lists this machine's orgs, then reports a host it can't reach (#45).
func TestAddressLocal(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(filepath.Join(state, "orgs", "acme"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "orgs", "acme", "org.env"), []byte("MANAGER=berth\nSSH_PORT=2201\nTTYD_PORT=7701\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	a := New(config.State{Home: config.Home{Path: state}}, local.New(), nil, &out, &errb, func(k string) string {
		if k == "HOME" {
			return home
		}
		if k == "PATH" {
			return os.Getenv("PATH")
		}
		return ""
	})
	for _, arg := range []string{"acme", "acme@local"} {
		b, o, done, err := a.At(ctx, arg)
		if err != nil || b != a || o != "acme" {
			t.Errorf("%s: %v %v %s", arg, b == a, err, o)
		}
		done()
	}
	if _, _, _, err := a.At(ctx, "acme@box1"); err == nil || !strings.Contains(err.Error(), "unknown host 'box1'") {
		t.Errorf("unknown host: %v", err)
	}

	// ls with no registered host: ccenv's table, no HOST column.
	if err := a.Ls(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "HOST") || !strings.HasPrefix(out.String(), "ORG ") {
		t.Errorf("ls, no hosts:\n%s", out.String())
	}

	// With one that can't be reached (its key file isn't a key, so nothing is dialed).
	p := a.hostPaths()
	if err := hosts.Save(a.Operator.FS, p, &hosts.Registry{Hosts: []hosts.Entry{{Name: "box1", Kind: hosts.KindSSH, User: "ops", Addr: "192.0.2.1:22", Home: "/srv/berth", Key: p.Key("box1")}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.Key("box1")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Key("box1"), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := a.Ls(ctx); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "HOST") || !strings.HasSuffix(lines[1], "local") {
		t.Errorf("ls with a host:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "host box1 is unreachable") {
		t.Errorf("stderr: %s", errb.String())
	}
	out.Reset()
	a.Output = OutputJSON
	if err := a.Ls(ctx); err != nil {
		t.Fatal(err)
	}
	var res ops.Orgs
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Orgs) != 1 || res.Orgs[0].Host != "local" || len(res.Unreachable) != 1 || res.Unreachable[0].Host != "box1" {
		t.Errorf("ls json: %+v", res)
	}
}
