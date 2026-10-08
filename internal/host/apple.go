package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Apple's `container` (#95, #96): each container is its own lightweight VM, on Apple silicon.
// It has no compose and no Docker API, so berth starts an org itself: what compose.yml sets
// becomes `container run` arguments (appleRun), and the rest of Docker's argv is mapped to the
// commands `container` has. This machine only: a registered host is Docker or Podman.
const EngineApple = "container"

// appleImagePull keeps `container image pull` to one download at a time: with more, pulls of
// berth's image from ghcr.io ended in HTTP/2 stream errors (the hands-on spike, #95).
var appleImagePull = []string{"image", "pull", "--max-concurrent-downloads", "1"}

// envOf reads KEY=VALUE pairs: a command's environment, or an env file's lines.
func envOf(lines []string) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(l, "#") {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

// appleRun is compose.yml as `container run` arguments: env is what berth gives compose (ORG,
// ORG_DIR, BIND_ADDR, HOST_UID, HOST_GID, the image), org is the org's org.env (ports, limits),
// and envFile its path.
//
// What compose.yml has and `container` doesn't: a hostname (the container's name is its
// hostname), and a restart policy (an org doesn't come back by itself after a reboot).
func appleRun(env, org map[string]string, envFile string) ([]string, error) {
	or := func(m map[string]string, k, def string) string {
		if v := m[k]; v != "" {
			return v
		}
		return def
	}
	name, dir := env["ORG"], env["ORG_DIR"]
	if name == "" || dir == "" {
		return nil, fmt.Errorf("starting an org with Apple's container needs ORG and ORG_DIR")
	}
	ssh, ttyd := org["SSH_PORT"], org["TTYD_PORT"]
	if ssh == "" || ttyd == "" {
		return nil, fmt.Errorf("%s has no SSH_PORT or TTYD_PORT", envFile)
	}
	bind := or(env, "BIND_ADDR", "127.0.0.1")
	// CPUS can be a fraction for Docker (0.5); a VM has whole ones.
	cpus := "4"
	if f, err := strconv.ParseFloat(or(org, "CPUS", "4"), 64); err == nil && f > 0 {
		cpus = strconv.Itoa(int(math.Ceil(f)))
	}
	args := []string{"run", "-d", "--name", "claude-" + name,
		"--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW", // for the egress firewall only
		"--env-file", envFile,
		"-e", "ORG=" + name, "-e", "HOST_UID=" + env["HOST_UID"], "-e", "HOST_GID=" + env["HOST_GID"],
		"-p", bind + ":" + ssh + ":2222", "-p", bind + ":" + ttyd + ":7681",
	}
	for _, m := range [][3]string{
		{"workspace", "/workspace", ""}, {"claude", "/home/node/.claude", ""}, {"mise", "/home/node/.mise", ""},
		{"home-config", "/home/node/.config", ""}, {"ssh", "/opt/claude-secrets/ssh", "ro"}, {"sshd", "/etc/ssh/keys", ""},
		{"config", "/config", "ro"}, {"quarantine", "/quarantine", ""},
	} {
		v := dir + "/" + m[0] + ":" + m[1]
		if m[2] != "" {
			v += ":" + m[2]
		}
		args = append(args, "-v", v)
	}
	args = append(args, "-m", or(org, "MEM_LIMIT", "8g"), "-c", cpus,
		or(env, "CLAUDE_ENV_IMAGE", "claude-env")+":"+or(env, "IMAGE_TAG", "latest"))
	return args, nil
}

// appleContainer is one entry of `container ls --format json`.
type appleContainer struct {
	ID            string `json:"id"`
	Configuration struct {
		PublishedPorts []struct {
			HostPort int `json:"hostPort"`
			Count    int `json:"count"`
		} `json:"publishedPorts"`
	} `json:"configuration"`
	// Status is an object from 1.x on ({"state": "running", …}); before, the state itself.
	Status json.RawMessage `json:"status"`
}

func (c appleContainer) state() string {
	var o struct {
		State string `json:"state"`
	}
	if json.Unmarshal(c.Status, &o) == nil && o.State != "" {
		return o.State
	}
	var s string
	_ = json.Unmarshal(c.Status, &s)
	return s
}

func parseAppleList(b []byte) ([]appleContainer, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var cs []appleContainer
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("the engine's container list: %w", err)
	}
	return cs, nil
}

// parseApplePorts are the host ports containers publish.
func parseApplePorts(b []byte) ([]int, error) {
	cs, err := parseAppleList(b)
	var ports []int
	for _, c := range cs {
		for _, p := range c.Configuration.PublishedPorts {
			for i := 0; i < max(p.Count, 1) && p.HostPort > 0; i++ {
				ports = append(ports, p.HostPort+i)
			}
		}
	}
	return ports, err
}

func (e engineExec) appleList(ctx context.Context, all bool) ([]appleContainer, error) {
	args := []string{EngineApple, "ls", "--format", "json"}
	if all {
		args = append(args, "-a")
	}
	var out bytes.Buffer
	if err := e.inner.Run(ctx, Cmd{Args: args, Stdout: &out, Stderr: io.Discard}); err != nil {
		return nil, fmt.Errorf("listing Apple container's containers (is its service running? container system start): %w", err)
	}
	return parseAppleList(out.Bytes())
}

// composeArgs splits `docker compose -f F --env-file E <verb> …` into the env file and the verb's
// own arguments.
func composeArgs(args []string) (envFile string, verb []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "-p", "--project-name":
			i++
		case "--env-file":
			i++
			if i < len(args) {
				envFile = args[i]
			}
		default:
			return envFile, args[i:]
		}
	}
	return envFile, nil
}

// runApple runs c, given with Docker's argv (c.Args[0] is "docker"), with Apple's `container`.
func (e engineExec) runApple(ctx context.Context, c Cmd) error {
	a := c.Args[1:]
	with := func(args ...string) error {
		d := c
		d.Args = append([]string{EngineApple}, args...)
		return e.inner.Run(ctx, d)
	}
	quiet := func(args ...string) error {
		return e.inner.Run(ctx, Cmd{Args: append([]string{EngineApple}, args...), Stdout: io.Discard, Stderr: io.Discard})
	}
	say := func(format string, v ...any) {
		if c.Stderr != nil {
			_, _ = fmt.Fprintf(c.Stderr, format+"\n", v...)
		}
	}
	if len(a) == 0 {
		return with()
	}
	switch a[0] {
	case "compose":
		envFile, verb := composeArgs(a[1:])
		env := envOf(c.Env)
		name := "claude-" + env["ORG"]
		if len(verb) == 0 {
			return fmt.Errorf("there is no compose with Apple's container: nothing to run for %q", strings.Join(a, " "))
		}
		switch verb[0] {
		case "version":
			return fmt.Errorf("there is no compose with Apple's container")
		case "up":
			if !slices.Contains(verb, "--force-recreate") {
				cs, err := e.appleList(ctx, false)
				if err != nil {
					return err
				}
				if slices.ContainsFunc(cs, func(x appleContainer) bool { return x.ID == name && x.state() == "running" }) {
					say(" Container %s  Running", name)
					return nil
				}
			}
			var file bytes.Buffer
			if err := e.inner.Run(ctx, Cmd{Args: []string{"cat", envFile}, Stdout: &file, Stderr: io.Discard}); err != nil {
				return fmt.Errorf("reading %s: %w", envFile, err)
			}
			run, err := appleRun(env, envOf(strings.Split(file.String(), "\n")), envFile)
			if err != nil {
				return err
			}
			// compose builds an image that isn't there (compose.yml's build:); so does this.
			if image := run[len(run)-1]; quiet("image", "inspect", image) != nil {
				build := []string{"build", "-t", image, "--build-arg", "USER_UID=" + env["HOST_UID"], "--build-arg", "USER_GID=" + env["HOST_GID"]}
				if v := env["CLAUDE_CODE_VERSION"]; v != "" {
					build = append(build, "--build-arg", "CLAUDE_CODE_VERSION="+v)
				}
				if err := with(append(build, env["CLAUDE_ENV_IMAGE_DIR"])...); err != nil {
					return fmt.Errorf("building %s: %w", image, err)
				}
			}
			_ = quiet("stop", name)
			_ = quiet("rm", "-f", name)
			say(" Container %s  Starting", name)
			var id bytes.Buffer
			d := c
			d.Args, d.Stdout = append([]string{EngineApple}, run...), &id
			if err := e.inner.Run(ctx, d); err != nil {
				return err
			}
			say(" Container %s  Started", name)
			return nil
		case "down":
			say(" Container %s  Stopping", name)
			_ = quiet("stop", name)
			if err := quiet("rm", "-f", name); err != nil {
				// Not there is fine: down is how berth makes sure of it.
				if cs, lerr := e.appleList(ctx, true); lerr != nil || slices.ContainsFunc(cs, func(x appleContainer) bool { return x.ID == name }) {
					return fmt.Errorf("removing %s: %w", name, err)
				}
			}
			say(" Container %s  Removed", name)
			return nil
		case "logs":
			return with(append([]string{"logs"}, append(verb[1:], name)...)...)
		}
		return fmt.Errorf("berth doesn't know how to run `compose %s` with Apple's container", verb[0])
	case "ps":
		cs, err := e.appleList(ctx, slices.Contains(a, "-a"))
		if err != nil {
			return err
		}
		if c.Stdout != nil {
			for _, x := range cs {
				if x.state() == "running" || slices.Contains(a, "-a") {
					_, _ = fmt.Fprintln(c.Stdout, x.ID)
				}
			}
		}
		return nil
	case "inspect":
		if len(a) == 4 && a[1] == "-f" { // inspect -f {{.State.Status}} NAME
			cs, err := e.appleList(ctx, true)
			if err != nil {
				return err
			}
			for _, x := range cs {
				if x.ID == a[3] && c.Stdout != nil {
					_, _ = fmt.Fprintln(c.Stdout, x.state())
					return nil
				}
			}
			return fmt.Errorf("no such container: %s", a[3])
		}
		return with(a...)
	case "image":
		return with(a...)
	case "pull":
		return with(append(slices.Clone(appleImagePull), a[1:]...)...)
	case "tag":
		return with(append([]string{"image", "tag"}, a[1:]...)...)
	case "logs":
		// docker logs [--tail N] NAME
		if len(a) == 4 && a[1] == "--tail" {
			return with("logs", "-n", a[2], a[3])
		}
		return with(a...)
	case "version":
		var out bytes.Buffer
		if err := e.inner.Run(ctx, Cmd{Args: []string{EngineApple, "--version"}, Stdout: &out, Stderr: io.Discard}); err != nil {
			return err
		}
		// "container CLI version 1.5.0 (build: …)"
		f := strings.Fields(out.String())
		if i := slices.Index(f, "version"); i >= 0 && i+1 < len(f) && c.Stdout != nil {
			_, _ = fmt.Fprintln(c.Stdout, f[i+1])
		}
		return nil
	case "exec", "run", "rm", "build", "stop", "start":
		return with(a...)
	}
	return fmt.Errorf("berth doesn't know how to run `docker %s` with Apple's container (docs/engines.md)", a[0])
}
