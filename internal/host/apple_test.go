package host

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeApple stands in for the machine: it records what is run, and answers `container ls`, `cat`
// and the commands named in fail.
type fakeApple struct {
	ran  []string
	list string
	fail map[string]bool
}

func (f *fakeApple) Run(_ context.Context, c Cmd) error {
	line := strings.Join(c.Args, " ")
	f.ran = append(f.ran, line)
	for prefix := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return errors.New("exit status 1")
		}
	}
	switch {
	case strings.HasPrefix(line, "container ls"):
		_, _ = c.Stdout.Write([]byte(f.list))
	case strings.HasPrefix(line, "cat "):
		_, _ = c.Stdout.Write([]byte("# ports\nSSH_PORT=2201\nTTYD_PORT=7701\nMEM_LIMIT=6g\nCPUS=2.5\nCLAUDE_CODE_OAUTH_TOKEN=sk-TOKENVALUE\n"))
	case line == "container --version":
		_, _ = c.Stdout.Write([]byte("container CLI version 1.5.0 (build: release, commit: abc)\n"))
	}
	return nil
}

const appleLs = `[{"id":"claude-acme","configuration":{"publishedPorts":[{"containerPort":2222,"count":1,"hostAddress":"127.0.0.1","hostPort":2201,"proto":"tcp"},{"containerPort":7681,"count":1,"hostPort":7701}]},"status":{"state":"running"}},
{"id":"claude-globex","configuration":{"publishedPorts":[{"hostPort":2202,"count":2}]},"status":{"state":"stopped"}},
{"id":"other","configuration":{},"status":"running"}]`

func appleEngine(f *fakeApple) engineExec {
	return engineExec{inner: f, bin: EngineApple, rootless: &rootless{}}
}

var composeEnv = []string{"BIND_ADDR=100.64.0.7", "ORG=acme", "ORG_DIR=/s/orgs/acme", "HOST_UID=501", "HOST_GID=20",
	"CLAUDE_ENV_IMAGE=berth/claude-env", "IMAGE_TAG=abc123", "CLAUDE_ENV_IMAGE_DIR=/s/berth/image"}

func compose(verb ...string) []string {
	return append([]string{"docker", "compose", "-f", "/s/berth/compose.yml", "--env-file", "/s/orgs/acme/org.env"}, verb...)
}

// TestAppleStartsAnOrgAsComposeWould: `compose up` becomes one `container run` with everything
// compose.yml sets: the capabilities, the env file, the ports on the bind address, the eight
// mounts (two read-only), and the limits.
func TestAppleStartsAnOrgAsComposeWould(t *testing.T) {
	f := &fakeApple{list: appleLs}
	var errb bytes.Buffer
	if err := appleEngine(f).Run(context.Background(), Cmd{Args: compose("up", "-d", "--force-recreate"), Env: composeEnv, Stderr: &errb}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cat /s/orgs/acme/org.env",
		"container image inspect berth/claude-env:abc123",
		"container stop claude-acme",
		"container rm -f claude-acme",
		"container run -d --name claude-acme --cap-add NET_ADMIN --cap-add NET_RAW --env-file /s/orgs/acme/org.env" +
			" -e ORG=acme -e HOST_UID=501 -e HOST_GID=20 -p 100.64.0.7:2201:2222 -p 100.64.0.7:7701:7681" +
			" -v /s/orgs/acme/workspace:/workspace -v /s/orgs/acme/claude:/home/node/.claude -v /s/orgs/acme/mise:/home/node/.mise" +
			" -v /s/orgs/acme/home-config:/home/node/.config -v /s/orgs/acme/ssh:/opt/claude-secrets/ssh:ro -v /s/orgs/acme/sshd:/etc/ssh/keys" +
			" -v /s/orgs/acme/config:/config:ro -v /s/orgs/acme/quarantine:/quarantine -m 6g -c 3 berth/claude-env:abc123",
	}
	if strings.Join(f.ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran:\n%s\nwant:\n%s", strings.Join(f.ran, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(errb.String(), "Container claude-acme  Started") {
		t.Errorf("stderr: %q", errb.String())
	}
	// No secret from org.env is on a command line: the file is passed by name.
	if strings.Contains(strings.Join(f.ran, " "), "TOKENVALUE") {
		t.Error("a value from org.env is in an argument")
	}

	// The image isn't there: it is built first, as compose does.
	f = &fakeApple{list: appleLs, fail: map[string]bool{"container image inspect": true}}
	if err := appleEngine(f).Run(context.Background(), Cmd{Args: compose("up", "-d"), Env: append(composeEnv[:1:1], "ORG=initech", "ORG_DIR=/s/orgs/initech", "HOST_UID=501", "HOST_GID=20", "CLAUDE_ENV_IMAGE_DIR=/img")}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.ran, "\n"); !strings.Contains(got, "container build -t claude-env:latest --build-arg USER_UID=501 --build-arg USER_GID=20 /img\ncontainer stop claude-initech") {
		t.Errorf("no build before the run:\n%s", got)
	}
	// Already running, and not asked to recreate: nothing happens.
	f = &fakeApple{list: appleLs}
	if err := appleEngine(f).Run(context.Background(), Cmd{Args: compose("up", "-d"), Env: composeEnv}); err != nil || len(f.ran) != 1 {
		t.Errorf("up on a running org: %v, ran %v", err, f.ran)
	}
	// org.env without its ports: an error, and nothing is stopped.
	if _, err := appleRun(envOf(composeEnv), map[string]string{}, "/x/org.env"); err == nil {
		t.Error("no ports was accepted")
	}
}

// TestAppleTranslatesDockersCommands: each command berth runs with Docker's argv, as `container`
// takes it; and one berth has no translation for is an error, not a guess.
func TestAppleTranslatesDockersCommands(t *testing.T) {
	for in, want := range map[string]string{
		"docker exec -u node claude-acme tmux ls":                                    "container exec -u node claude-acme tmux ls",
		"docker exec -it -u node -e K=V -w /workspace claude-acme x":                 "container exec -it -u node -e K=V -w /workspace claude-acme x",
		"docker run --rm -i --network none --entrypoint bash -v /a:/src:ro img -c x": "container run --rm -i --network none --entrypoint bash -v /a:/src:ro img -c x",
		"docker rm -f claude-acme":                                                   "container rm -f claude-acme",
		"docker image inspect berth/claude-env:abc":                                  "container image inspect berth/claude-env:abc",
		"docker pull ghcr.io/ar4mirez/berth-image@sha256:1":                          "container image pull --max-concurrent-downloads 1 ghcr.io/ar4mirez/berth-image@sha256:1",
		"docker tag a:1 b:2":                                                         "container image tag a:1 b:2",
		"docker build -t x:1 --build-arg A=1 /dir":                                   "container build -t x:1 --build-arg A=1 /dir",
		"docker logs claude-acme":                                                    "container logs claude-acme",
		"docker logs --tail 50 claude-acme":                                          "container logs -n 50 claude-acme",
		"docker inspect claude-acme":                                                 "container inspect claude-acme",
	} {
		f := &fakeApple{}
		if err := appleEngine(f).Run(context.Background(), Cmd{Args: strings.Fields(in)}); err != nil || len(f.ran) != 1 || f.ran[0] != want {
			t.Errorf("%s:\n ran  %v (%v)\n want %s", in, f.ran, err, want)
		}
	}
	run := func(f *fakeApple, env []string, args ...string) (string, error) {
		var out bytes.Buffer
		err := appleEngine(f).Run(context.Background(), Cmd{Args: args, Env: env, Stdout: &out, Stderr: &bytes.Buffer{}})
		return out.String(), err
	}
	f := &fakeApple{list: appleLs}
	if out, err := run(f, nil, "docker", "ps", "--format", "{{.Names}}"); err != nil || out != "claude-acme\nother\n" {
		t.Errorf("ps: %q %v", out, err)
	}
	if out, err := run(f, nil, "docker", "inspect", "-f", "{{.State.Status}}", "claude-globex"); err != nil || out != "stopped\n" {
		t.Errorf("inspect -f: %q %v", out, err)
	}
	if _, err := run(f, nil, "docker", "inspect", "-f", "{{.State.Status}}", "nope"); err == nil {
		t.Error("inspect -f of a missing container succeeded")
	}
	if out, err := run(f, nil, "docker", "version", "--format", "{{.Server.Version}}"); err != nil || out != "1.5.0\n" {
		t.Errorf("version: %q %v", out, err)
	}
	// down: stop and remove; a container that isn't there is fine.
	f = &fakeApple{list: appleLs, fail: map[string]bool{"container stop": true, "container rm": true}}
	if _, err := run(f, []string{"ORG=nope"}, compose("down")...); err != nil {
		t.Errorf("down of an org that isn't running: %v", err)
	}
	if _, err := run(f, []string{"ORG=acme"}, compose("down")...); err == nil {
		t.Error("down reported success though the container is still there")
	}
	f = &fakeApple{}
	if _, err := run(f, []string{"ORG=acme"}, compose("logs", "-f")...); err != nil || f.ran[0] != "container logs -f claude-acme" {
		t.Errorf("logs: %v %v", f.ran, err)
	}
	for _, args := range [][]string{{"docker", "network", "ls"}, {"docker", "compose", "version", "--short"}, compose("restart")} {
		f := &fakeApple{}
		if _, err := run(f, []string{"ORG=acme"}, args...); err == nil || len(f.ran) != 0 {
			t.Errorf("%v: ran %v, err %v", args, f.ran, err)
		}
	}
	// The ports containers publish, for choosing a new org's.
	if ports, err := parseApplePorts([]byte(appleLs)); err != nil || len(ports) != 4 || ports[2] != 2202 || ports[3] != 2203 {
		t.Errorf("ports: %v %v", ports, err)
	}
	if err := CheckEngine(EngineApple); err != nil {
		t.Error(err)
	}
}
