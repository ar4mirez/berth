package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
)

// bindExec answers what resolveBind and info ask: ip, ifconfig, tailscale, hostname. Anything else
// fails, as a missing tool would.
type bindExec struct{ tailscale string }

func (b bindExec) Run(_ context.Context, c host.Cmd) error {
	out := ""
	switch strings.Join(c.Args, " ") {
	case "ip -4 -o addr show dev wg0":
		out = "5: wg0    inet 10.8.0.2/24 scope global wg0\\       valid_lft forever preferred_lft forever\n"
	case "ifconfig zt0":
		out = "zt0: flags=8863<UP,BROADCAST,RUNNING> mtu 2800\n\tinet 10.147.17.5 netmask 0xffffff00 broadcast 10.147.17.255\n"
	case "tailscale ip -4":
		if b.tailscale == "" {
			return &host.ExitError{Args: c.Args, Code: 1}
		}
		out = b.tailscale + "\n"
	case "hostname":
		out = "box1\n"
	default:
		return &host.ExitError{Args: c.Args, Code: 127}
	}
	if c.Stdout != nil {
		_, _ = c.Stdout.Write([]byte(out))
	}
	return nil
}

func TestBindModes(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	var out bytes.Buffer
	a := New(config.State{Home: config.Home{Path: state}}, host.New("t", local.FS{}, bindExec{tailscale: "100.64.0.7"}, nil, nil, nil),
		nil, &out, &out, func(k string) string {
			if k == "USER" {
				return "ops"
			}
			return ""
		})
	set := func(bind string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(state, "orgs", "acme"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "orgs", "acme", "org.env"), []byte("BIND_ADDR="+bind+"\nSSH_PORT=2201\nTTYD_PORT=7701\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for bind, want := range map[string]string{
		"":             "127.0.0.1",
		"127.0.0.1":    "127.0.0.1",
		"tailscale":    "100.64.0.7",
		"localhost":    "127.0.0.1",
		"0.0.0.0":      "0.0.0.0",
		"10.0.0.5":     "10.0.0.5",
		"ip:192.0.2.9": "192.0.2.9",
		"iface:wg0":    "10.8.0.2",
		"iface:zt0":    "10.147.17.5", // no ip(8): ifconfig
	} {
		set(bind)
		if got, err := a.resolveBind(ctx, "acme"); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", bind, got, err, want)
		}
	}
	for _, bind := range []string{"iface:nope0", "iface:../x"} {
		set(bind)
		if _, err := a.resolveBind(ctx, "acme"); err == nil || !strings.Contains(err.Error(), "no IPv4 address") {
			t.Errorf("%q: %v", bind, err)
		}
	}

	// info: a tunnel section for localhost, none for ccenv's 127.0.0.1 (its sheet is unchanged).
	for bind, tunnel := range map[string]bool{"localhost": true, "127.0.0.1": false, "iface:wg0": false} {
		set(bind)
		out.Reset()
		_ = a.Info(ctx, "acme")
		has := strings.Contains(out.String(), "SSH tunnel") && strings.Contains(out.String(), "ssh -N -L 2201:127.0.0.1:2201 -L 7701:127.0.0.1:7701 ops@box1")
		if has != tunnel {
			t.Errorf("info with %s: tunnel section %v:\n%s", bind, has, out.String())
		}
		if bind == "iface:wg0" && !strings.Contains(out.String(), "node@10.8.0.2") {
			t.Errorf("info with iface:wg0 doesn't use its address:\n%s", out.String())
		}
	}

	// 0.0.0.0 warns; the others don't.
	for bind, warns := range map[string]bool{"0.0.0.0": true, "tailscale": false} {
		set(bind)
		out.Reset()
		a.warnOpenBind("acme")
		if strings.Contains(out.String(), "every interface") != warns {
			t.Errorf("%s: %q", bind, out.String())
		}
	}
}

func TestCheckBind(t *testing.T) {
	for _, ok := range []string{"", "tailscale", "localhost", "0.0.0.0", "iface:wg0", "iface:zt3jn3nq", "ip:10.0.0.5", "10.0.0.5", "ip:fd7a::1"} {
		if err := CheckBind(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"tailscal", "iface:", "iface:a b", "ip:nope", "wg0"} {
		if CheckBind(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSSHTarget(t *testing.T) {
	if got := sshTarget("ops", "box.example:22"); got != "ops@box.example" {
		t.Error(got)
	}
	if got := sshTarget("ops", "box.example:2222"); got != "-p 2222 ops@box.example" {
		t.Error(got)
	}
}
