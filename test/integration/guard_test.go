//go:build integration

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/contract"
)

// TestHostGuard: on the sshd + dind host, an org container can't reach the host (its bridge
// gateway) or 169.254.169.254, while a container on another network can; allowlisted egress
// still works; host rm takes the rules out (#48).
func TestHostGuard(t *testing.T) {
	if os.Getenv("BERTH_BIN") == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	hostGuard(t, startSSHTarget(t), "berth-t-sshd", "docker", "ops")
}

// TestHostGuardPodman: the same checks on a host with a rootful Podman (#97): the guard's rules
// are hooked into FORWARD, the org's bridge is netavark's, and DNS (aardvark, on the gateway)
// still answers.
func TestHostGuardPodman(t *testing.T) {
	if os.Getenv("BERTH_BIN") == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	hostGuard(t, startPodmanTarget(t), "berth-t-podman", "podman", "root")
}

// startPodmanTarget is the sshd + rootful Podman fixture.
func startPodmanTarget(t *testing.T) *sshTarget {
	t.Helper()
	requireDocker(t)
	const img, name = "berth-test-sshd-podman", "berth-t-podman"
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	mustDo(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	mustDo(t, err)
	block, err := gossh.MarshalPrivateKey(priv, "berth integration test")
	mustDo(t, err)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	mustDo(t, os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600))
	docker(t, "build", "-q", "-t", img, "testdata/sshd-podman")
	_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
	// Podman's storage on a volume: overlay can't sit on the container's own overlay filesystem.
	docker(t, "run", "-d", "--privileged", "--name", name, "-v", "/var/lib/containers",
		"-e", "AUTHORIZED_KEY="+strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))), "-p", "127.0.0.1::22", img)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", "--tail", "20", name).CombinedOutput()
			t.Logf("fixture logs:\n%s", out)
		}
		_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
	})
	deadline := time.Now().Add(90 * time.Second)
	for exec.Command("docker", "exec", name, "podman", "--url", "unix:///run/podman/podman.sock", "info").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("podman's API never came up")
		}
		time.Sleep(time.Second)
	}
	hk, _, _, _, err := gossh.ParseAuthorizedKey([]byte(docker(t, "exec", name, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
	mustDo(t, err)
	return &sshTarget{addr: strings.TrimSpace(docker(t, "port", name, "22/tcp")), hostKey: hk, keyFile: keyFile}
}

// hostGuard is the guard's test on a fixture host whose engine is docker or podman, logging in as user.
func hostGuard(t *testing.T, target *sshTarget, fixture, engine, user string) {
	bin := os.Getenv("BERTH_BIN")
	const o = "t-guard"
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_SPELLINGS=ccenv", "BERTH_HOME="+filepath.Join(home, "state"), "SSH_AUTH_SOCK=", "BERTH_PUBLISHED_IMAGE="+publishedImage)
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
	inner := func(args ...string) (string, error) {
		b, err := exec.Command("docker", append([]string{"exec", fixture, engine}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	rules := func() string {
		b, _ := exec.Command("docker", "exec", fixture, "sh", "-c",
			"iptables-nft -S 2>/dev/null; iptables-legacy -S 2>/dev/null; iptables -S 2>/dev/null").Output()
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "BERTH-") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	t.Cleanup(func() { _, _ = berth("down", o+"@box") })

	out, _ := berth("host", "add", "box", user+"@"+target.addr, "--identity", target.keyFile)
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	out = must("host", "add", "box", user+"@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1])
	if !strings.Contains(out, "guard") || !strings.Contains(out, "on: org containers can't reach") {
		t.Errorf("host add didn't report the guard:\n%s", out)
	}
	if running, _ := inner("inspect", "-f", "{{.State.Running}}", "berth-host-guard"); running != "true" {
		t.Fatalf("berth-host-guard isn't running: %s", running)
	}

	must("init", o+"@box", "--name", "Test User", "--email", "test@example.com")
	must("up", o+"@box")
	img, err := inner("inspect", "-f", "{{.Config.Image}}", "claude-"+o)
	mustDo(t, err)
	orgNet := "claude-" + o + "_default"
	gateway := func(network string) string {
		format := "{{(index .IPAM.Config 0).Gateway}}"
		if engine == "podman" {
			format = "{{(index .Subnets 0).Gateway}}"
		}
		gw, err := inner("network", "inspect", "-f", format, network)
		mustDo(t, err)
		return gw
	}
	// reach: can a container on network open a TCP connection to hostPort?
	reach := func(network, hostPort string) bool {
		h, p, _ := strings.Cut(hostPort, ":")
		_, err := inner("run", "--rm", "--network", network, "--entrypoint", "bash", img, "-c",
			"timeout 5 bash -c '</dev/tcp/"+h+"/"+p+"'")
		return err == nil
	}
	_, _ = inner("network", "create", "t-other")
	t.Cleanup(func() { _, _ = inner("network", "rm", "t-other") })

	bridge := map[string]string{"docker": "br-", "podman": "podman"}[engine]
	if s := rules(); !strings.Contains(s, "-A BERTH-INPUT -i "+bridge) || !strings.Contains(s, "169.254.0.0/16") {
		t.Fatalf("the guard's rules don't cover the org's network:\n%s", s)
	}
	if engine == "podman" {
		// Podman's containers resolve names through aardvark-dns on the gateway: the guard lets DNS through.
		if s := rules(); !strings.Contains(s, "--dport 53 -j RETURN") || !strings.Contains(s, "-A FORWARD -j BERTH-FORWARD") {
			t.Errorf("on podman the guard lets DNS through and hooks FORWARD:\n%s", s)
		}
		if out, err := inner("run", "--rm", "--network", orgNet, "--entrypoint", "bash", img, "-c", "getent hosts example.com"); err != nil {
			t.Errorf("a container on the org's network can't resolve names: %s", out)
		}
	}
	// The host's sshd (port 22 on the dind host), through each network's gateway.
	if !reach("t-other", gateway("t-other")+":22") {
		t.Fatal("control: a container on another network can't reach the host's sshd either; the test proves nothing")
	}
	if reach(orgNet, gateway(orgNet)+":22") {
		t.Error("an org container reaches the host through its gateway")
	}
	// Cloud metadata: the runner's own endpoint, when it answers at all from nested containers.
	if reach("t-other", "169.254.169.254:80") {
		if reach(orgNet, "169.254.169.254:80") {
			t.Error("an org container reaches 169.254.169.254")
		}
	} else {
		t.Log("169.254.169.254 doesn't answer here even without the guard; only the rule itself is checked")
	}

	// Allowlisted egress still works, through the org's own firewall.
	deadline := time.Now().Add(3 * time.Minute)
	for st, _ := inner("exec", "claude-"+o, "cat", contract.FirewallStatus); !strings.HasPrefix(st, "on"); st, _ = inner("exec", "claude-"+o, "cat", contract.FirewallStatus) {
		if time.Now().After(deadline) {
			t.Fatalf("the org's firewall never came up: %q", st)
		}
		time.Sleep(2 * time.Second)
	}
	must("fw", o+"@box", "allow", "example.com")
	if code, err := inner("exec", "claude-"+o, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "-m", "20", "https://example.com"); err != nil || code != "200" {
		t.Errorf("allowlisted egress: %s %v", code, err)
	}

	if out := must("host", "guard", "box"); !strings.Contains(out, "running") || !strings.Contains(out, "BERTH-INPUT") {
		t.Errorf("host guard status:\n%s", out)
	}

	// host rm takes the guard and its rules away.
	must("down", o+"@box")
	must("host", "rm", "box", "--force")
	if _, err := inner("inspect", "berth-host-guard"); err == nil {
		t.Error("berth-host-guard remains after host rm")
	}
	if s := rules(); s != "" {
		t.Errorf("rules remain after host rm:\n%s", s)
	}
}
