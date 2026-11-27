//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
)

// TestSocketMount: an org's socket mount (#216) is the same container through compose (the second
// compose file berth writes) and through the Engine API (BERTH_SOCKET_MOUNTS): the socket's
// directory, read-write, under /run/berth-mounts/. The container sees the socket as a socket, and
// a daemon that makes its socket again is seen without a restart, since the directory is what is
// mounted.
func TestSocketMount(t *testing.T) {
	requireDocker(t)
	f := newFixture(t)
	ctx := context.Background()
	cmd := exec.Command("docker", "build", "-q", "-t", "berth-t-sleeper:1", "-")
	cmd.Stdin = strings.NewReader("FROM busybox:1.37\nCMD [\"sh\", \"-c\", \"echo ready; exec sleep 2147483647\"]\n")
	run(t, cmd)

	dir, err := os.MkdirTemp("", "bm")
	mustDo(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	mustDo(t, os.Chmod(dir, 0o700))
	sock := filepath.Join(dir, "agent.sock")
	l, err := net.Listen("unix", sock)
	mustDo(t, err)
	// The compose file and the variable, as berth writes them (app.socketMounts).
	override := filepath.Join(t.TempDir(), "mounts.compose.yml")
	mustDo(t, os.WriteFile(override, []byte(fmt.Sprintf(`services:
  claude:
    volumes:
      - type: bind
        source: %q
        target: "/run/berth-mounts/agent"
        bind:
          create_host_path: false
`, dir)), 0o644))
	binds, _ := json.Marshal([][2]string{{dir, "/run/berth-mounts/agent"}})
	env := []string{"BIND_ADDR=127.0.0.1", "ORG=" + org, "ORG_DIR=" + f.orgDir, "BERTH_SOCKET_MOUNTS=" + string(binds),
		fmt.Sprintf("HOST_UID=%d", os.Getuid()), fmt.Sprintf("HOST_GID=%d", os.Getgid()), "CLAUDE_ENV_IMAGE=berth-t-sleeper", "IMAGE_TAG=1"}
	file, envFile := filepath.Join(f.root, "compose.yml"), filepath.Join(f.orgDir, "org.env")
	files := []string{"compose", "-f", file, "-f", override, "--env-file", envFile}
	viaCompose := func(args ...string) {
		t.Helper()
		c := exec.Command("docker", append(files, args...)...)
		c.Env = append(os.Environ(), env...)
		run(t, c)
	}
	t.Setenv("BERTH_ENGINE_API", "1")
	h := host.WithEngine(local.New(), host.EngineDocker)
	viaAPI := func(args ...string) {
		t.Helper()
		var errb bytes.Buffer
		if err := h.Exec.Run(ctx, host.Cmd{Args: append([]string{"docker"}, append(files, args...)...), Env: env, Stdout: &bytes.Buffer{}, Stderr: &errb}); err != nil {
			t.Fatalf("through the API, compose %v: %v\n%s", args, err, errb.String())
		}
	}
	name := "claude-" + org
	t.Cleanup(func() {
		c := exec.Command("docker", append(files, "down")...)
		c.Env = append(os.Environ(), env...)
		_ = c.Run()
	})
	isSocket := func() bool {
		return exec.Command("docker", "exec", name, "test", "-S", "/run/berth-mounts/agent/agent.sock").Run() == nil
	}

	viaCompose("up", "-d")
	byCompose := inspectOrg(t, name)
	want := dir + ":/run/berth-mounts/agent rw=true"
	if !strings.Contains(strings.Join(byCompose.Mounts, "\n"), want) {
		t.Fatalf("compose's container has no %s:\n%s", want, strings.Join(byCompose.Mounts, "\n"))
	}
	if !isSocket() {
		t.Error("the container doesn't see the socket as one")
	}
	viaAPI("up", "-d", "--force-recreate")
	if byAPI := inspectOrg(t, name); fmt.Sprintf("%v", byAPI.Mounts) != fmt.Sprintf("%v", byCompose.Mounts) {
		t.Errorf("mounts through the API:\n%v\nthrough compose:\n%v", byAPI.Mounts, byCompose.Mounts)
	}
	// The daemon goes and comes back with a new socket: the container sees the new one.
	mustDo(t, l.Close())
	_ = os.Remove(sock)
	if isSocket() {
		t.Error("the socket is still there after the daemon removed it")
	}
	l, err = net.Listen("unix", sock)
	mustDo(t, err)
	defer func() { _ = l.Close() }()
	if !isSocket() {
		t.Error("a socket made again isn't seen without a restart")
	}
	viaAPI("down")
}
