//go:build integration

package integration

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSupervisedProcess: image/berth-proc in a real container (#216). A process started with
// `docker exec -i -u node … berth-proc start` keeps running after that exec has returned, runs as
// the container's user and not root, reaches a host socket mounted into the container the way a
// socket mount mounts it, and ends with the container. The image is a small one with what berth's
// has for this (bash, jq, setsid, as user node), so the test doesn't wait for a release.
func TestSupervisedProcess(t *testing.T) {
	requireDocker(t)
	root, err := filepath.Abs("../..")
	mustDo(t, err)
	ctx := t.TempDir()
	b, err := os.ReadFile(filepath.Join(root, "image", "berth-proc"))
	mustDo(t, err)
	mustDo(t, os.WriteFile(filepath.Join(ctx, "berth-proc"), b, 0o755))
	mustDo(t, os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte(`FROM debian:stable-slim
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends jq socat procps >/dev/null && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 1000 node && install -d -m 755 -o node -g node /run/berth
COPY berth-proc /usr/local/bin/berth-proc
CMD ["sleep", "infinity"]
`), 0o644))
	docker(t, "build", "-q", "-t", "berth-t-proc:1", ctx)

	// A daemon on the host: it answers every line with "pong <line>".
	dir, err := os.MkdirTemp("", "bp")
	mustDo(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	mustDo(t, os.Chmod(dir, 0o777)) // the container's user (uid 1000) connects to it
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	mustDo(t, err)
	defer func() { _ = l.Close() }()
	mustDo(t, os.Chmod(filepath.Join(dir, "d.sock"), 0o777))
	got := make(chan string, 8)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					got <- sc.Text()
					_, _ = c.Write([]byte("pong " + sc.Text() + "\n"))
				}
			}()
		}
	}()

	const name = "berth-t-proc"
	_ = exec.Command("docker", "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	docker(t, "run", "-d", "--name", name, "-v", dir+":/run/berth-mounts/daemon", "berth-t-proc:1")

	// Started, and the exec that started it has returned.
	spec := `{"argv":["sh","-c","while :; do echo \"hello $(id -u) $GREETING\" | socat - UNIX-CONNECT:/run/berth-mounts/daemon/d.sock; sleep 1; done"],"cwd":"/tmp","env":{"GREETING":"from-inside"}}`
	start := exec.Command("docker", "exec", "-i", "-u", "node", name, "/usr/local/bin/berth-proc", "start", "runner")
	start.Stdin = strings.NewReader(spec)
	out, err := start.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"state": "running"`) {
		t.Fatalf("start: %v\n%s", err, out)
	}
	select {
	case line := <-got:
		if line != "hello 1000 from-inside" {
			t.Errorf("the daemon got %q, want it from uid 1000 with its variable", line)
		}
	case <-time.After(30 * time.Second):
		logs, _ := exec.Command("docker", "exec", "-u", "node", name, "/usr/local/bin/berth-proc", "logs", "runner").CombinedOutput()
		t.Fatalf("the process never reached the host's socket; its log:\n%s", logs)
	}
	// Its user, and its state readable by a later exec.
	if st := docker(t, "exec", "-u", "node", name, "/usr/local/bin/berth-proc", "status", "runner"); !strings.Contains(st, `"state": "running"`) {
		t.Errorf("status: %s", st)
	}
	if ps := docker(t, "exec", name, "ps", "-o", "user=,args=", "-C", "sh"); strings.Contains(ps, "root") || !strings.Contains(ps, "node") {
		t.Errorf("the process's user:\n%s", ps)
	}
	if out, err := exec.Command("docker", "exec", "-i", name, "/usr/local/bin/berth-proc", "start", "x").CombinedOutput(); err == nil || !strings.Contains(string(out), "never as root") {
		t.Errorf("berth-proc as root: %v %s", err, out)
	}

	// It ends with the container: nothing reaches the daemon after the container is gone.
	docker(t, "rm", "-f", name)
	for drained := false; !drained; {
		select {
		case <-got:
		case <-time.After(1500 * time.Millisecond):
			drained = true
		}
	}
	select {
	case line := <-got:
		t.Errorf("the process outlived its container: the daemon got %q", line)
	case <-time.After(3 * time.Second):
	}
}
