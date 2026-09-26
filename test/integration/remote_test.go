//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/ops"
)

// publishedImage is a released image both sides pull instead of building image/ (minutes, twice).
// Any release works: this tests where commands run, not the image.
const publishedImage = "ghcr.io/ar4mirez/berth-image:v0.3.0"

// TestRemoteLifecycle: the lifecycle (init, up, fw allow, repo add, down) on this machine and, as
// org@host, on the sshd + dind fixture, with the same results (#45).
func TestRemoteLifecycle(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	target := startSSHTarget(t)
	const fixture, o = "berth-t-sshd", "t-life"
	const remoteState = "/home/ops/.local/share/berth"

	home := t.TempDir()
	localState := filepath.Join(home, "state")
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_HOME="+localState, "SSH_AUTH_SOCK=", "BERTH_PUBLISHED_IMAGE="+publishedImage)
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

	// Register the fixture as box, logging in as ops (a plain user in the docker group).
	out, _ := berth("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile)
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	must("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1])

	type side struct {
		name, org, state string
		docker           func(args ...string) (string, error)
		cat              func(p string) (string, error)
	}
	sides := []side{
		{"local", o, localState,
			func(args ...string) (string, error) {
				b, err := exec.Command("docker", args...).CombinedOutput()
				return string(b), err
			},
			func(p string) (string, error) { b, err := os.ReadFile(p); return string(b), err }},
		{"box", o + "@box", remoteState,
			func(args ...string) (string, error) {
				b, err := exec.Command("docker", append([]string{"exec", fixture, "docker"}, args...)...).CombinedOutput()
				return string(b), err
			},
			func(p string) (string, error) {
				b, err := exec.Command("docker", "exec", fixture, "cat", p).CombinedOutput()
				return string(b), err
			}},
	}
	results := map[string]map[string]string{}
	for _, s := range sides {
		t.Run(s.name, func(t *testing.T) {
			res := map[string]string{}
			results[s.name] = res
			orgDir := s.state + "/orgs/" + o
			norm := func(x string) string { return strings.ReplaceAll(x, orgDir, "<ORGDIR>") }
			t.Cleanup(func() { _, _ = berth("down", s.org) })
			diag := func() string {
				st, _ := s.docker("inspect", "-f", "{{json .State}} restarts={{.RestartCount}}", "claude-"+o)
				logs, _ := s.docker("logs", "claude-"+o)
				img, _ := s.docker("inspect", "-f", "{{.Config.Image}}", "claude-"+o)
				// The entrypoint traced, the way compose starts it (as far as the environment goes).
				trace, _ := s.docker("run", "--rm", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW", "-e", "ORG="+o,
					"-e", fmt.Sprintf("HOST_UID=%d", os.Getuid()), "-e", fmt.Sprintf("HOST_GID=%d", os.Getgid()),
					"--entrypoint", "timeout", strings.TrimSpace(img), "60", "bash", "-x", "/usr/local/bin/entrypoint.sh")
				return st + "\n" + logs + "\ntrace:\n" + trace
			}

			res["init"] = norm(must("init", s.org, "--name", "Test User", "--email", "test@example.com"))
			envFile, err := s.cat(orgDir + "/org.env")
			mustDo(t, err)
			var keys []string
			for _, l := range strings.Split(envFile, "\n") {
				if k, _, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(k, "#") {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			res["org.env keys"] = strings.Join(keys, " ")

			// Earlier tests leave a stub image under berth's tag on this machine; up would use it
			// instead of pulling the released one.
			_, _ = s.docker("rmi", "-f", strings.TrimSpace(must("image-tag")))
			must("up", s.org)
			if running, _ := s.docker("inspect", "-f", "{{.State.Running}}", "claude-"+o); strings.TrimSpace(running) != "true" {
				t.Fatalf("not running after up (%s):\n%s", running, diag())
			}
			deadline := time.Now().Add(3 * time.Minute)
			for {
				st, _ := s.docker("exec", "claude-"+o, "cat", contract.FirewallStatus)
				if strings.HasPrefix(st, "on") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the firewall never came up (%q):\n%s", st, diag())
				}
				time.Sleep(2 * time.Second)
			}

			// ls sees the org where it is.
			cmd := exec.Command(bin, "--output", "json", "ls")
			cmd.Env = env
			b, err := cmd.Output()
			mustDo(t, err)
			var ls ops.Orgs
			mustDo(t, json.Unmarshal(b, &ls))
			if !slices.ContainsFunc(ls.Orgs, func(x ops.OrgStatus) bool { return x.Name == o && x.Host == s.name && x.State == "up" }) || len(ls.Unreachable) != 0 {
				t.Errorf("ls: %s", b)
			}

			res["fw allow"] = norm(must("fw", s.org, "allow", "example.com"))
			res["fw show"] = regexp.MustCompile(`on \d+`).ReplaceAllString(norm(must("fw", s.org, "show")), "on N")
			res["repo add"] = norm(must("repo", "add", s.org, "acme/widgets", "--no-clone"))
			res["repo ls"] = norm(must("repo", "ls", s.org))
			repos, err := s.cat(orgDir + "/config/repos.txt")
			mustDo(t, err)
			res["repos.txt"] = repos

			must("down", s.org)
			if ps, _ := s.docker("ps", "-aq", "--filter", "name=^claude-"+o+"$"); strings.TrimSpace(ps) != "" {
				t.Errorf("a container remains after down: %s", ps)
			}
		})
	}

	// The same results on both.
	l, r := results["local"], results["box"]
	if l == nil || r == nil {
		t.Fatal("a side didn't run")
	}
	for k, want := range l {
		if k == "init" {
			continue // names the host's hostname, key and ports; compared below without them
		}
		if r[k] != want {
			t.Errorf("%s differs:\nlocal:\n%s\nbox:\n%s", k, want, r[k])
		}
	}
	strip := regexp.MustCompile(`(?m)(ssh-ed25519 \S+ \S+|\d{4,5}|claude-t-life@\S+)`)
	if a, b := strip.ReplaceAllString(l["init"], "X"), strip.ReplaceAllString(r["init"], "X"); a != b {
		t.Errorf("init differs:\nlocal:\n%s\nbox:\n%s", a, b)
	}
}
