//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
)

const org = "t-smoke"

// fixture is a synthetic org laid out like `ccenv init` makes it (no keys or tokens needed here).
type fixture struct {
	root     string // repo root: compose.yml lives here
	orgDir   string
	override string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	f := &fixture{root: root, orgDir: filepath.Join(home, "orgs", org), override: filepath.Join(home, "override.yml")}

	for _, d := range []string{"workspace", "claude", "ssh", "sshd", "mise", "home-config", "quarantine", "config/secrets"} {
		mustDo(t, os.MkdirAll(filepath.Join(f.orgDir, d), 0o755))
	}
	for _, d := range []string{"", "ssh", "claude", "home-config", "config/secrets"} {
		mustDo(t, os.Chmod(filepath.Join(f.orgDir, d), 0o700))
	}
	mustDo(t, os.WriteFile(filepath.Join(f.orgDir, "org.env"), []byte(strings.Join([]string{
		"CLAUDE_CODE_OAUTH_TOKEN=",
		"GIT_USER_NAME=Test User",
		"GIT_USER_EMAIL=test@example.com",
		"BIND_ADDR=127.0.0.1",
		"SSH_PORT=2290",
		"TTYD_PORT=7790",
		"REMOTE_CONTROL=0",
		"REPO_POLICY=enforce",
		"MEM_LIMIT=256m",
		"CPUS=1",
	}, "\n")+"\n"), 0o600))
	// busybox instead of claude-env: this tests the compose contract, not image/.
	mustDo(t, os.WriteFile(f.override, []byte(`services:
  claude:
    image: busybox:1.37
    build: !reset null
    entrypoint: ["sleep", "infinity"]
`), 0o600))
	return f
}

// compose mirrors ccenv's compose(): same env vars, same -f / --env-file.
func (f *fixture) compose(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"compose", "-f", filepath.Join(f.root, "compose.yml"), "-f", f.override,
		"--env-file", filepath.Join(f.orgDir, "org.env")}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Env = append(os.Environ(),
		"BIND_ADDR=127.0.0.1", "ORG="+org, "ORG_DIR="+f.orgDir,
		fmt.Sprintf("HOST_UID=%d", os.Getuid()), fmt.Sprintf("HOST_GID=%d", os.Getgid()))
	return run(t, cmd)
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	return run(t, exec.Command("docker", args...))
}

func run(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(cmd.Args, " "), err, errb.String())
	}
	return out.String()
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// The subset of `docker inspect` that docs/plan.md compares between legacy and berth.
type container struct {
	State  struct{ Running bool }
	Config struct {
		Hostname string
		Env      []string
		Labels   map[string]string
	}
	HostConfig struct {
		Memory        int64
		NanoCpus      int64
		CapAdd        []string
		RestartPolicy struct{ Name string }
	}
	NetworkSettings struct {
		Ports map[string][]struct{ HostIp, HostPort string }
	}
	Mounts []struct {
		Source, Destination string
		RW                  bool
	}
}

func inspect(t *testing.T, name string) container {
	t.Helper()
	var cs []container
	mustDo(t, json.Unmarshal([]byte(docker(t, "inspect", name)), &cs))
	if len(cs) != 1 {
		t.Fatalf("inspect %s: %d results", name, len(cs))
	}
	return cs[0]
}

// projectIDs lists the containers or networks carrying this org's compose project label.
func projectIDs(t *testing.T, kind string) []string {
	t.Helper()
	args := []string{"ps", "-aq"}
	if kind == "network" {
		args = []string{"network", "ls", "-q"}
	}
	return strings.Fields(docker(t, append(args, "--filter", "label=com.docker.compose.project=claude-"+org)...))
}

func TestComposeLifecycle(t *testing.T) {
	requireDocker(t)
	f := newFixture(t)
	name := "claude-" + org
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	t.Run("config", func(t *testing.T) {
		var cfg struct {
			Name     string
			Services map[string]struct {
				ContainerName string `json:"container_name"`
			}
		}
		mustDo(t, json.Unmarshal([]byte(f.compose(t, "config", "--format", "json")), &cfg))
		if cfg.Name != name || cfg.Services["claude"].ContainerName != name {
			t.Errorf("project %q, container_name %q; want %q", cfg.Name, cfg.Services["claude"].ContainerName, name)
		}
	})

	f.compose(t, "up", "-d", "--force-recreate")
	c := inspect(t, name)

	t.Run("container", func(t *testing.T) {
		checks := []struct {
			what      string
			got, want any
		}{
			{"running", c.State.Running, true},
			{"compose project label", c.Config.Labels["com.docker.compose.project"], name},
			{"hostname", c.Config.Hostname, org},
			{"memory", c.HostConfig.Memory, int64(256 << 20)},
			{"nano cpus", c.HostConfig.NanoCpus, int64(1e9)},
			{"restart policy", c.HostConfig.RestartPolicy.Name, "unless-stopped"},
		}
		for _, ch := range checks {
			if ch.got != ch.want {
				t.Errorf("%s = %v, want %v", ch.what, ch.got, ch.want)
			}
		}
		for _, cp := range []string{"NET_ADMIN", "NET_RAW"} {
			if !slices.Contains(c.HostConfig.CapAdd, cp) && !slices.Contains(c.HostConfig.CapAdd, "CAP_"+cp) {
				t.Errorf("CapAdd %v lacks %s", c.HostConfig.CapAdd, cp)
			}
		}
		for _, kv := range []string{"ORG=" + org, "REPO_POLICY=enforce", "SSH_PORT=2290"} {
			if !slices.Contains(c.Config.Env, kv) {
				t.Errorf("container env lacks %s", kv)
			}
		}
	})

	t.Run("ports", func(t *testing.T) {
		for port, hostPort := range map[string]string{"2222/tcp": "2290", "7681/tcp": "7790"} {
			b := c.NetworkSettings.Ports[port]
			if len(b) != 1 || b[0].HostIp != "127.0.0.1" || b[0].HostPort != hostPort {
				t.Errorf("%s bound to %+v, want 127.0.0.1:%s", port, b, hostPort)
			}
		}
	})

	// host/local against the real engine: the org's published ports must count as in use, or the
	// port allocator could hand them to the next org.
	t.Run("local host sees the ports", func(t *testing.T) {
		h := local.New()
		if err := host.NewEngine(h.Docker).Ping(context.Background()); err != nil {
			t.Fatalf("engine ping: %v", err)
		}
		ports, err := h.Facts.PortsInUse(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []int{2290, 7790} {
			if !slices.Contains(ports, p) {
				t.Errorf("port %d not reported in use: %v", p, ports)
			}
		}
	})

	t.Run("mounts", func(t *testing.T) {
		want := map[string]struct {
			src string
			rw  bool
		}{
			"/workspace":              {"workspace", true},
			"/home/node/.claude":      {"claude", true},
			"/home/node/.mise":        {"mise", true},
			"/home/node/.config":      {"home-config", true},
			"/opt/claude-secrets/ssh": {"ssh", false},
			"/etc/ssh/keys":           {"sshd", true},
			"/config":                 {"config", false},
			"/quarantine":             {"quarantine", true},
		}
		seen := map[string]bool{}
		for _, m := range c.Mounts {
			w, ok := want[m.Destination]
			if !ok {
				t.Errorf("unexpected mount %s -> %s", m.Source, m.Destination)
				continue
			}
			seen[m.Destination] = true
			if src := filepath.Join(f.orgDir, w.src); m.Source != src || m.RW != w.rw {
				t.Errorf("%s: source %s rw=%v, want %s rw=%v", m.Destination, m.Source, m.RW, src, w.rw)
			}
		}
		for dst := range want {
			if !seen[dst] {
				t.Errorf("missing mount %s", dst)
			}
		}
	})

	t.Run("recreate keeps one container", func(t *testing.T) {
		f.compose(t, "up", "-d", "--force-recreate")
		if ids := projectIDs(t, "container"); len(ids) != 1 {
			t.Errorf("%d containers after recreate, want 1", len(ids))
		}
	})

	t.Run("down leaves nothing behind", func(t *testing.T) {
		f.compose(t, "down")
		if ids := projectIDs(t, "container"); len(ids) != 0 {
			t.Errorf("orphan containers: %v", ids)
		}
		if ids := projectIDs(t, "network"); len(ids) != 0 {
			t.Errorf("orphan networks: %v", ids)
		}
	})
}

// TestComposeImageVariables: compose.yml's image and build context are claude-env:latest and
// ./image for ccenv (no variables set: unchanged behaviour), and berth's own tag and dir when berth
// sets CLAUDE_ENV_IMAGE, IMAGE_TAG and CLAUDE_ENV_IMAGE_DIR.
func TestComposeImageVariables(t *testing.T) {
	requireDocker(t)
	f := newFixture(t)
	config := func(extra ...string) (image, context string) {
		t.Helper()
		cmd := exec.Command("docker", "compose", "-f", filepath.Join(f.root, "compose.yml"),
			"--env-file", filepath.Join(f.orgDir, "org.env"), "config", "--format", "json")
		cmd.Env = append(os.Environ(), append([]string{"BIND_ADDR=127.0.0.1", "ORG=" + org, "ORG_DIR=" + f.orgDir}, extra...)...)
		var cfg struct {
			Services map[string]struct {
				Image string
				Build struct{ Context string }
			}
		}
		mustDo(t, json.Unmarshal([]byte(run(t, cmd)), &cfg))
		return cfg.Services["claude"].Image, cfg.Services["claude"].Build.Context
	}
	if img, ctx := config(); img != "claude-env:latest" || ctx != filepath.Join(f.root, "image") {
		t.Errorf("ccenv (no variables): image %q, context %q", img, ctx)
	}
	dir := t.TempDir()
	if img, ctx := config("CLAUDE_ENV_IMAGE=berth/claude-env", "IMAGE_TAG=0123456789ab", "CLAUDE_ENV_IMAGE_DIR="+dir); img != "berth/claude-env:0123456789ab" || ctx != dir {
		t.Errorf("berth: image %q, context %q", img, ctx)
	}
}
