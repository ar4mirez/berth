//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/host"
)

// TestOrgSpecMatchesCompose: host.OrgSpec, berth's own description of an org's container, is what
// `docker compose config` resolves compose.yml to for the same org and environment (#43, phase 1 of
// docs/prd/043-engine-api.md): the name, hostname, image and build, capabilities, environment,
// ports, mounts, limits, restart policy, init, and the rootless Podman user namespace. A change to
// compose.yml that the spec doesn't follow fails here.
func TestOrgSpecMatchesCompose(t *testing.T) {
	requireDocker(t)
	f := newFixture(t)
	base := map[string]string{"BIND_ADDR": "127.0.0.1", "ORG": org, "ORG_DIR": f.orgDir,
		"HOST_UID": strconv.Itoa(os.Getuid()), "HOST_GID": strconv.Itoa(os.Getgid())}
	orgEnv, err := os.ReadFile(filepath.Join(f.orgDir, "org.env"))
	mustDo(t, err)
	orgVars := map[string]string{}
	for _, l := range strings.Split(string(orgEnv), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			orgVars[k] = v
		}
	}

	for _, c := range []struct {
		name  string
		extra map[string]string
	}{
		{"ccenv's", nil},
		{"berth's image, rootless Podman, a CPU cap", map[string]string{"CLAUDE_ENV_IMAGE": "berth/claude-env", "IMAGE_TAG": "0123456789ab",
			"CLAUDE_ENV_IMAGE_DIR": t.TempDir(), "BERTH_USERNS": "keep-id", "BERTH_USER": "0:0", "CPUS": "0.5"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range base {
				env[k] = v
			}
			for k, v := range c.extra {
				env[k] = v
			}
			envFile := filepath.Join(f.orgDir, "org.env")
			spec, err := host.NewOrgSpec(env, orgVars, envFile)
			mustDo(t, err)

			cmd := exec.Command("docker", "compose", "-f", filepath.Join(f.root, "compose.yml"), "--env-file", envFile, "config", "--format", "json")
			cmd.Env = os.Environ()
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			var cfg struct {
				Name     string
				Services map[string]struct {
					ContainerName string `json:"container_name"`
					Hostname      string
					Image         string
					Build         struct {
						Context string
						Args    map[string]*string
					}
					CapAdd      []string           `json:"cap_add"`
					Environment map[string]*string `json:"environment"`
					Ports       []struct {
						HostIP    string `json:"host_ip"`
						Published string
						Target    int
					}
					Volumes []struct {
						Type, Source, Target string
						ReadOnly             bool `json:"read_only"`
					}
					MemLimit   json.RawMessage `json:"mem_limit"`
					CPUs       json.Number     `json:"cpus"`
					Restart    string
					Init       *bool
					UsernsMode string `json:"userns_mode"`
					User       string
				}
			}
			mustDo(t, json.Unmarshal([]byte(run(t, cmd)), &cfg))
			s, ok := cfg.Services["claude"]
			if !ok {
				t.Fatalf("no service claude: %+v", cfg)
			}
			check := func(what string, got, want any) {
				t.Helper()
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("%s: compose %v, spec %v", what, got, want)
				}
			}
			check("project", cfg.Name, spec.Project)
			check("container name", s.ContainerName, spec.Name)
			check("hostname", s.Hostname, spec.Hostname)
			check("image", s.Image, spec.Image)
			ctx := spec.BuildContext
			if !filepath.IsAbs(ctx) {
				ctx = filepath.Join(f.root, ctx)
			}
			check("build context", s.Build.Context, ctx)
			for k, v := range spec.BuildArgs {
				got := ""
				if s.Build.Args[k] != nil {
					got = *s.Build.Args[k]
				}
				check("build arg "+k, got, v)
			}
			check("cap_add", s.CapAdd, spec.CapAdd)
			// compose puts org.env's lines and environment: together; the spec has them apart.
			want := map[string]string{}
			for k, v := range orgVars {
				want[k] = v
			}
			for k, v := range spec.Env {
				want[k] = v
			}
			for k, v := range want {
				got := ""
				if s.Environment[k] != nil {
					got = *s.Environment[k]
				}
				check("environment "+k, got, v)
			}
			check("number of environment variables", len(s.Environment), len(want))
			check("number of ports", len(s.Ports), len(spec.Ports))
			for i := range min(len(s.Ports), len(spec.Ports)) {
				check(fmt.Sprintf("port %d", i), fmt.Sprintf("%s:%s:%d", s.Ports[i].HostIP, s.Ports[i].Published, s.Ports[i].Target),
					spec.Ports[i].HostIP+":"+spec.Ports[i].Host+":"+spec.Ports[i].Container)
			}
			check("number of mounts", len(s.Volumes), len(spec.Mounts))
			for i := range min(len(s.Volumes), len(spec.Mounts)) {
				v, m := s.Volumes[i], spec.Mounts[i]
				check("mount "+m.Target, fmt.Sprintf("%s %s:%s ro=%v", v.Type, v.Source, v.Target, v.ReadOnly), fmt.Sprintf("bind %s:%s ro=%v", m.Source, m.Target, m.ReadOnly))
			}
			check("mem_limit", strings.Trim(string(s.MemLimit), `"`), strconv.FormatInt(bytesOf(t, spec.Memory), 10))
			gotCPU, _ := s.CPUs.Float64()
			wantCPU, _ := strconv.ParseFloat(spec.CPUs, 64)
			check("cpus", gotCPU, wantCPU)
			check("restart", s.Restart, spec.Restart)
			check("init", s.Init != nil && *s.Init, spec.Init)
			check("userns_mode", s.UsernsMode, spec.UsernsMode)
			check("user", s.User, spec.User)
		})
	}
}

// bytesOf is a compose memory size (256m, 8g) in bytes.
func bytesOf(t *testing.T, s string) int64 {
	t.Helper()
	mult := map[byte]int64{'b': 1, 'k': 1 << 10, 'm': 1 << 20, 'g': 1 << 30}
	n, err := strconv.ParseInt(strings.TrimRight(s, "bkmgBKMG"), 10, 64)
	mustDo(t, err)
	if u, ok := mult[strings.ToLower(s)[len(s)-1]]; ok {
		n *= u
	}
	return n
}
