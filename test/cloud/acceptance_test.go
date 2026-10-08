//go:build cloud

// Package cloud is #52's acceptance test: `berth host create` against Hetzner Cloud itself. It
// makes a real server (billed by the hour) in the project of HCLOUD_TOKEN, and needs this machine
// on the tailnet of BERTH_TAILSCALE_AUTHKEY:
//
//	go test -tags cloud -count=1 -timeout 30m -v ./test/cloud/
//
// Whatever happens, it leaves nothing labelled for its host in the project.
package cloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/cloud"
)

// openPorts are the TCP ports of addr that take a connection, among ports.
func openPorts(addr string, ports []int) []int {
	var (
		mu   sync.Mutex
		open []int
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 200)
	)
	for _, p := range ports {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(addr, fmt.Sprint(p)), 3*time.Second)
			if err == nil {
				_ = c.Close()
				mu.Lock()
				open = append(open, p)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return open
}

// hostKey is the fingerprint of the ssh host key addr presents.
func hostKey(addr string) (string, error) {
	var seen string
	stop := errors.New("seen")
	_, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{User: "ops", Timeout: 10 * time.Second,
		HostKeyAlgorithms: []string{gossh.KeyAlgoED25519},
		HostKeyCallback: func(_ string, _ net.Addr, k gossh.PublicKey) error {
			seen = gossh.FingerprintSHA256(k)
			return stop
		}})
	if seen == "" {
		return "", err
	}
	return seen, nil
}

func TestHostCreateOnHetzner(t *testing.T) {
	token, tsKey := os.Getenv("HCLOUD_TOKEN"), os.Getenv("BERTH_TAILSCALE_AUTHKEY")
	if token == "" || tsKey == "" {
		t.Skip("HCLOUD_TOKEN and BERTH_TAILSCALE_AUTHKEY are not both set: this test makes a real server")
	}
	ctx := context.Background()
	work, err := os.MkdirTemp("", "berth-cloud-")
	if err != nil {
		t.Fatal(err)
	}
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		bin = filepath.Join(work, "berth")
		if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/berth").CombinedOutput(); err != nil {
			t.Fatalf("building berth: %v\n%s", err, out)
		}
	}
	rnd := make([]byte, 3)
	_, _ = rand.Read(rnd)
	name, o := "t-acc"+hex.EncodeToString(rnd), "t-cloud"
	vm := "berth-" + name
	env := append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(work, "cfg"), "BERTH_HOME="+filepath.Join(work, "state"), "BERTH_SPELLINGS=")
	berth := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := berth(args...)
		if err != nil {
			t.Fatalf("berth %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	h := &cloud.Hetzner{Token: token, Endpoint: os.Getenv("BERTH_HCLOUD_ENDPOINT")}
	if h.Endpoint == "" {
		h.Endpoint = cloud.HetznerEndpoint
	}
	// Nothing of this run is left at the provider, however the test ends.
	t.Cleanup(func() {
		_, _ = berth("host", "destroy", name, "--force")
		if removed, err := h.Remove(ctx, name, 2*time.Minute); err != nil {
			t.Errorf("LEFT AT HETZNER, labelled berth.host=%s: %v", name, err)
		} else if len(removed) > 0 {
			t.Logf("the cleanup removed: %s", strings.Join(removed, ", "))
		}
		_ = os.RemoveAll(work)
	})

	// 1. Create. The host key berth pinned is the one the server presents.
	out := must("host", "create", name, "--provider", "hetzner")
	t.Log(out)
	m := regexp.MustCompile(`ssh host key (SHA256:[A-Za-z0-9+/]+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no host key in create's output")
	}
	if got, err := hostKey(net.JoinHostPort(vm, "22")); err != nil || got != m[1] {
		t.Errorf("the server presents host key %q (%v), berth pinned %s", got, err, m[1])
	}
	if strings.Contains(out, "without a tag") {
		t.Log("the auth key was not tagged: berth warned")
	}
	if ls := must("host", "ls"); !regexp.MustCompile(`(?m)^` + name + `\s+ssh\s+ops@` + vm + `:22\s+yes\s+docker`).MatchString(ls) {
		t.Errorf("host ls:\n%s", ls)
	}

	// 2. What is at the provider: one server and one firewall, labelled for this host.
	servers, err := h.Servers(ctx, name)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers labelled for %s: %v %v", name, servers, err)
	}
	public := servers[0].Public.IPv4.IP
	if fws, err := h.Firewalls(ctx, name); err != nil || len(fws) != 1 {
		t.Errorf("firewalls labelled for %s: %v %v", name, fws, err)
	}

	// 3. An org on it, up. Its ports answer on the tailnet.
	must("org", "create", o+"@"+name, "--name", "Test User", "--email", "test@example.com")
	t.Log(must("up", o+"@"+name))
	row := regexp.MustCompile(`(?m)^` + o + `\s+up\s+(\d+)\s+(\d+)\s`).FindStringSubmatch(must("ls"))
	if row == nil {
		t.Fatalf("the org isn't up in ls:\n%s", must("ls"))
	}
	var ports [2]int
	_, _ = fmt.Sscan(row[1], &ports[0])
	_, _ = fmt.Sscan(row[2], &ports[1])
	deadline := time.Now().Add(90 * time.Second)
	for len(openPorts(vm, ports[:])) != len(ports) {
		if time.Now().After(deadline) {
			t.Errorf("the org's ports %v don't all answer on the tailnet: %v do", ports, openPorts(vm, ports[:]))
			break
		}
		time.Sleep(3 * time.Second)
	}

	// 4. From outside: no port of the public address is open.
	if public == "" {
		t.Errorf("the server has no public IPv4 address to check")
	} else {
		scan := append([]int{2222, 2375, 2376, 7681}, ports[:]...)
		for p := 1; p <= 1024; p++ {
			scan = append(scan, p)
		}
		if open := openPorts(public, scan); len(open) > 0 {
			t.Errorf("open on the public address %s: %v", public, open)
		}
	}

	// 5. Destroy is refused while the host has an org; then everything goes, and nothing is left.
	if out, err := berth("host", "destroy", name); err == nil {
		t.Errorf("host destroy with an org on the host succeeded:\n%s", out)
	}
	must("org", "destroy", o+"@"+name, "--yes")
	if out := must("host", "destroy", name); !strings.Contains(out, "Deleted at Hetzner: server "+vm+", firewall "+vm) {
		t.Errorf("host destroy:\n%s", out)
	}
	if out, err := berth("host", "reconcile"); err != nil {
		t.Errorf("host reconcile after destroy: %v\n%s", err, out)
	}
	if s, err := h.Servers(ctx, name); err != nil || len(s) != 0 {
		t.Errorf("servers left: %v %v", s, err)
	}
	if f, err := h.Firewalls(ctx, name); err != nil || len(f) != 0 {
		t.Errorf("firewalls left: %v %v", f, err)
	}
	if ls := must("host", "ls"); strings.Contains(ls, name) {
		t.Errorf("host ls still has it:\n%s", ls)
	}
}
