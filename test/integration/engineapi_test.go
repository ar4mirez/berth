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

// inspected is what docs/plan.md says a container made by compose and one made through the
// Engine API must agree on: binds, published ports, memory, CPUs, capabilities, restart policy,
// environment, hostname, image and the compose project label, plus its network and user.
type inspected struct {
	Mounts, Ports, Env, CapAdd, Networks []string
	Memory, NanoCpus                     int64
	Restart, Hostname, Image, Project    string
	Service, User, UsernsMode            string
}

func inspectOrg(t *testing.T, name string) inspected {
	t.Helper()
	var cs []struct {
		Config struct {
			Hostname, Image, User string
			Env                   []string
			Labels                map[string]string
		}
		HostConfig struct {
			Memory, NanoCpus int64
			CapAdd           []string
			RestartPolicy    struct{ Name string }
			PortBindings     map[string][]struct{ HostIp, HostPort string }
			UsernsMode       string
		}
		Mounts []struct {
			Source, Destination string
			RW                  bool
		}
		NetworkSettings struct{ Networks map[string]any }
	}
	mustDo(t, json.Unmarshal([]byte(docker(t, "inspect", name)), &cs))
	c := cs[0]
	in := inspected{Memory: c.HostConfig.Memory, NanoCpus: c.HostConfig.NanoCpus, Restart: c.HostConfig.RestartPolicy.Name,
		Hostname: c.Config.Hostname, Image: c.Config.Image, User: c.Config.User, UsernsMode: c.HostConfig.UsernsMode,
		Project: c.Config.Labels["com.docker.compose.project"], Service: c.Config.Labels["com.docker.compose.service"],
		Env: slices.Sorted(slices.Values(c.Config.Env)), CapAdd: slices.Sorted(slices.Values(c.HostConfig.CapAdd))}
	for _, m := range c.Mounts {
		in.Mounts = append(in.Mounts, fmt.Sprintf("%s:%s rw=%v", m.Source, m.Destination, m.RW))
	}
	for port, bs := range c.HostConfig.PortBindings {
		for _, b := range bs {
			in.Ports = append(in.Ports, b.HostIp+":"+b.HostPort+"->"+port)
		}
	}
	for n := range c.NetworkSettings.Networks {
		in.Networks = append(in.Networks, n)
	}
	slices.Sort(in.Mounts)
	slices.Sort(in.Ports)
	slices.Sort(in.Networks)
	return in
}

// TestEngineAPICompatibility: a container made through the Engine API path (BERTH_ENGINE_API=1,
// #43) is the one compose makes, and either can recreate or remove what the other made, leaving no
// container or network behind (docs/prd/043-engine-api.md, phase 2).
func TestEngineAPICompatibility(t *testing.T) {
	requireDocker(t)
	f := newFixture(t)
	ctx := context.Background()
	// An image that stays up and says so, under the name berth would give its own.
	cmd := exec.Command("docker", "build", "-q", "-t", "berth-t-sleeper:1", "-")
	cmd.Stdin = strings.NewReader("FROM busybox:1.37\nCMD [\"sh\", \"-c\", \"echo ready; exec sleep 2147483647\"]\n")
	run(t, cmd)
	env := []string{"BIND_ADDR=127.0.0.1", "ORG=" + org, "ORG_DIR=" + f.orgDir,
		fmt.Sprintf("HOST_UID=%d", os.Getuid()), fmt.Sprintf("HOST_GID=%d", os.Getgid()), "CLAUDE_ENV_IMAGE=berth-t-sleeper", "IMAGE_TAG=1"}
	file, envFile := filepath.Join(f.root, "compose.yml"), filepath.Join(f.orgDir, "org.env")
	viaCompose := func(args ...string) {
		t.Helper()
		c := exec.Command("docker", append([]string{"compose", "-f", file, "--env-file", envFile}, args...)...)
		c.Env = append(os.Environ(), env...)
		run(t, c)
	}
	t.Setenv("BERTH_ENGINE_API", "1")
	h := host.WithEngine(local.New(), host.EngineDocker)
	viaAPI := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if err := h.Exec.Run(ctx, host.Cmd{Args: append([]string{"docker", "compose", "-f", file, "--env-file", envFile}, args...), Env: env, Stdout: &out, Stderr: &errb}); err != nil {
			t.Fatalf("through the API, compose %v: %v\n%s", args, err, errb.String())
		}
		return out.String()
	}
	name, project := "claude-"+org, "claude-"+org
	t.Cleanup(func() {
		c := exec.Command("docker", "compose", "-f", file, "--env-file", envFile, "down")
		c.Env = append(os.Environ(), env...)
		_ = c.Run()
	})
	left := func() (containers, networks string) {
		return strings.TrimSpace(docker(t, "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)),
			strings.TrimSpace(docker(t, "network", "ls", "-q", "--filter", "label=com.docker.compose.project="+project))
	}
	same := func(what string, a, b inspected) {
		t.Helper()
		if fmt.Sprintf("%+v", a) != fmt.Sprintf("%+v", b) {
			t.Errorf("%s:\ncompose %+v\napi     %+v", what, a, b)
		}
	}

	// compose makes it; the API recreates it: the same container.
	viaCompose("up", "-d")
	byCompose := inspectOrg(t, name)
	if byCompose.Project != project || byCompose.Networks[0] != project+"_default" {
		t.Fatalf("compose's container: %+v", byCompose)
	}
	// The switch alone recreates nothing: up through the API leaves compose's running container as it is.
	id := strings.TrimSpace(docker(t, "inspect", "-f", "{{.Id}}", name))
	viaAPI("up", "-d")
	if now := strings.TrimSpace(docker(t, "inspect", "-f", "{{.Id}}", name)); now != id {
		t.Errorf("up through the API recreated compose's running container (%s, then %s)", id, now)
	}
	viaAPI("up", "-d", "--force-recreate")
	same("recreated through the API", byCompose, inspectOrg(t, name))
	if c, _ := left(); len(strings.Fields(c)) != 1 {
		t.Errorf("containers after the API recreated compose's: %q", c)
	}
	// compose removes what the API made, network included.
	viaCompose("down")
	if c, n := left(); c != "" || n != "" {
		t.Errorf("after compose down of the API's: containers %q, networks %q", c, n)
	}

	// The API makes it; compose recreates it; the API removes it.
	viaAPI("up", "-d")
	same("made through the API", byCompose, inspectOrg(t, name))
	if out := viaAPI("logs"); !strings.Contains(out, "ready") {
		t.Errorf("logs through the API: %q", out)
	}
	viaCompose("up", "-d", "--force-recreate")
	same("compose recreating the API's", byCompose, inspectOrg(t, name))
	if c, _ := left(); len(strings.Fields(c)) != 1 {
		t.Errorf("containers after compose recreated the API's: %q", c)
	}
	viaAPI("down")
	if c, n := left(); c != "" || n != "" {
		t.Errorf("after down through the API: containers %q, networks %q", c, n)
	}
}
