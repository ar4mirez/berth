package host

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Engine API path (#43, docs/prd/043-engine-api.md). On a Docker host, an org's
// `docker compose up|down|logs` goes to the engine's API instead of the compose CLI, from the
// org's OrgSpec: the same container, network and labels compose makes, so either can stop or
// recreate what the other made. Every other command, and Podman and Apple's container, are
// unchanged. BERTH_ENGINE_API=0 keeps compose; so does an engine whose socket doesn't answer (a
// Docker reached only through a docker context: Docker Desktop, colima), where compose's CLI
// knows the way.

// EngineAPIWanted is whether the Engine API path is on: unless BERTH_ENGINE_API=0.
func EngineAPIWanted() bool { return os.Getenv("BERTH_ENGINE_API") != "0" }

// apiExec runs an org's compose commands through the Engine API, and everything else as inner.
type apiExec struct {
	inner  Execer
	engine *Engine
	alive  *engineUp
}

// engineUp is whether the engine's socket answers, asked once, at the first compose command.
type engineUp struct {
	once sync.Once
	ok   bool
}

// withEngineAPI is h with its orgs' compose commands going to its engine's API.
func withEngineAPI(h *Host) *Host {
	g := *h
	g.Exec = apiExec{inner: h.Exec, engine: NewEngine(h.Docker), alive: &engineUp{}}
	return &g
}

func (e apiExec) Run(ctx context.Context, c Cmd) error {
	if len(c.Args) > 2 && c.Args[0] == EngineDocker && c.Args[1] == "compose" {
		file, envFile, verb := composeFiles(c.Args[2:])
		if len(verb) > 0 && slices.Contains([]string{"up", "down", "logs"}, verb[0]) && file != "" && envFile != "" && e.answers(ctx) {
			return e.compose(ctx, c, file, envFile, verb)
		}
	}
	return e.inner.Run(ctx, c)
}

// answers is whether the engine's socket answers; compose runs the commands when it doesn't.
func (e apiExec) answers(ctx context.Context) bool {
	e.alive.once.Do(func() {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		e.alive.ok = e.engine.Ping(pctx) == nil
	})
	return e.alive.ok
}

// composeFiles splits `-f F --env-file E <verb> …` into the compose file, the env file and the verb.
func composeFiles(args []string) (file, envFile string, verb []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--file":
			if i++; i < len(args) {
				file = args[i]
			}
		case "--env-file":
			if i++; i < len(args) {
				envFile = args[i]
			}
		case "-p", "--project-name":
			i++
		default:
			return file, envFile, args[i:]
		}
	}
	return file, envFile, nil
}

func (e apiExec) compose(ctx context.Context, c Cmd, file, envFile string, verb []string) error {
	var raw bytes.Buffer
	if err := e.inner.Run(ctx, Cmd{Args: []string{"cat", envFile}, Stdout: &raw, Stderr: io.Discard}); err != nil {
		return fmt.Errorf("reading %s: %w", envFile, err)
	}
	orgEnv := envFileVars(raw.Bytes())
	orgVars := map[string]string{}
	for _, kv := range orgEnv {
		orgVars[kv[0]] = kv[1]
	}
	spec, err := NewOrgSpec(envOf(c.Env), orgVars, envFile)
	if err != nil {
		return err
	}
	if !path.IsAbs(spec.BuildContext) {
		spec.BuildContext = path.Join(path.Dir(file), spec.BuildContext)
	}
	say := func(format string, v ...any) {
		if c.Stderr != nil {
			_, _ = fmt.Fprintf(c.Stderr, format+"\n", v...)
		}
	}
	switch verb[0] {
	case "up":
		return e.up(ctx, c, spec, orgEnv, file, slices.Contains(verb, "--build"), slices.Contains(verb, "--force-recreate"), say)
	case "down":
		return e.down(ctx, spec, say)
	default: // logs [-f]
		q := url.Values{"stdout": {"1"}, "stderr": {"1"}}
		if slices.Contains(verb, "-f") || slices.Contains(verb, "--follow") {
			q.Set("follow", "1")
		}
		return e.engine.Stream(ctx, "/containers/"+spec.Name+"/logs?"+q.Encode(), func(r io.Reader) error {
			return demux(r, c.Stdout, c.Stderr)
		})
	}
}

// network is the org's network, compose's default one for its project.
func (s OrgSpec) network() string { return s.Project + "_default" }

// labels are compose's on the org's container, so compose (and the host guard, and ccenv down)
// find it as one of its own.
func (s OrgSpec) labels(file string) map[string]string {
	return map[string]string{
		"com.docker.compose.project":              s.Project,
		"com.docker.compose.service":              "claude",
		"com.docker.compose.container-number":     "1",
		"com.docker.compose.oneoff":               "False",
		"com.docker.compose.project.config_files": file,
		"com.docker.compose.project.working_dir":  path.Dir(file),
		"berth.engine-api":                        "1",
	}
}

func (e apiExec) up(ctx context.Context, c Cmd, s OrgSpec, orgEnv [][2]string, file string, build, recreate bool, say func(string, ...any)) error {
	// The image: built when asked (compose's --build), or when it isn't there (compose.yml's build:).
	if build || errors.Is(e.engine.Do(ctx, "GET", "/images/"+s.Image+"/json", nil, nil), ErrEngineNotFound) {
		args := []string{EngineDocker, "build", "-t", s.Image}
		for _, k := range []string{"USER_UID", "USER_GID", "CLAUDE_CODE_VERSION"} {
			args = append(args, "--build-arg", k+"="+s.BuildArgs[k])
		}
		if err := e.inner.Run(ctx, Cmd{Args: append(args, s.BuildContext), Stdout: c.Stdout, Stderr: c.Stderr}); err != nil {
			return fmt.Errorf("building %s: %w", s.Image, err)
		}
	}
	// The network, as compose makes it.
	if err := e.engine.Do(ctx, "GET", "/networks/"+s.network(), nil, nil); errors.Is(err, ErrEngineNotFound) {
		say(" Network %s  Creating", s.network())
		if err := e.engine.Do(ctx, "POST", "/networks/create", map[string]any{
			"Name": s.network(), "Driver": "bridge", "CheckDuplicate": true,
			"Labels": map[string]string{"com.docker.compose.project": s.Project, "com.docker.compose.network": "default"},
		}, nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	// The container: left alone when it runs and nothing asks for a new one, else made again.
	var cur struct{ State struct{ Running bool } }
	switch err := e.engine.Do(ctx, "GET", "/containers/"+s.Name+"/json", nil, &cur); {
	case errors.Is(err, ErrEngineNotFound):
	case err != nil:
		return err
	case cur.State.Running && !recreate:
		say(" Container %s  Running", s.Name)
		return nil
	default:
		say(" Container %s  Recreate", s.Name)
		if err := e.remove(ctx, s.Name); err != nil {
			return err
		}
	}
	body, err := s.createBody(orgEnv, file)
	if err != nil {
		return err
	}
	say(" Container %s  Creating", s.Name)
	var made struct{ ID string }
	if err := e.engine.Do(ctx, "POST", "/containers/create?name="+url.QueryEscape(s.Name), body, &made); err != nil {
		return err
	}
	if err := e.engine.Do(ctx, "POST", "/containers/"+made.ID+"/start", nil, nil); err != nil {
		return err
	}
	say(" Container %s  Started", s.Name)
	return nil
}

func (e apiExec) remove(ctx context.Context, name string) error {
	if err := e.engine.Do(ctx, "POST", "/containers/"+name+"/stop?t=10", nil, nil); err != nil && !errors.Is(err, ErrEngineNotFound) {
		return err
	}
	if err := e.engine.Do(ctx, "DELETE", "/containers/"+name+"?force=1", nil, nil); err != nil && !errors.Is(err, ErrEngineNotFound) {
		return err
	}
	return nil
}

func (e apiExec) down(ctx context.Context, s OrgSpec, say func(string, ...any)) error {
	say(" Container %s  Stopping", s.Name)
	if err := e.remove(ctx, s.Name); err != nil {
		return err
	}
	say(" Container %s  Removed", s.Name)
	if err := e.engine.Do(ctx, "DELETE", "/networks/"+s.network(), nil, nil); err != nil && !errors.Is(err, ErrEngineNotFound) {
		return err
	}
	say(" Network %s  Removed", s.network())
	return nil
}

// createBody is POST /containers/create's body for s: what compose sends for compose.yml.
func (s OrgSpec) createBody(orgEnv [][2]string, file string) (map[string]any, error) {
	mem, err := memBytes(s.Memory)
	if err != nil {
		return nil, fmt.Errorf("MEM_LIMIT %s: %w", s.Memory, err)
	}
	cpus, err := strconv.ParseFloat(s.CPUs, 64)
	if err != nil || cpus <= 0 {
		return nil, fmt.Errorf("CPUS %s: not a number of CPUs", s.CPUs)
	}
	// env_file's lines, then environment:, which wins.
	vars := map[string]string{}
	var order []string
	for _, kv := range orgEnv {
		if _, ok := vars[kv[0]]; !ok {
			order = append(order, kv[0])
		}
		vars[kv[0]] = kv[1]
	}
	for _, k := range []string{"ORG", "HOST_UID", "HOST_GID"} {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = s.Env[k]
	}
	env := make([]string, 0, len(order))
	for _, k := range order {
		env = append(env, k+"="+vars[k])
	}
	exposed, bindings := map[string]any{}, map[string]any{}
	for _, p := range s.Ports {
		k := p.Container + "/tcp"
		exposed[k] = map[string]any{}
		bindings[k] = []map[string]string{{"HostIp": p.HostIP, "HostPort": p.Host}}
	}
	binds := make([]string, 0, len(s.Mounts))
	for _, m := range s.Mounts {
		b := m.Source + ":" + m.Target
		if m.ReadOnly {
			b += ":ro"
		} else {
			b += ":rw"
		}
		binds = append(binds, b)
	}
	caps := make([]string, 0, len(s.CapAdd))
	for _, c := range s.CapAdd {
		caps = append(caps, "CAP_"+strings.TrimPrefix(c, "CAP_")) // as compose sends them
	}
	host := map[string]any{
		"Binds": binds, "PortBindings": bindings, "CapAdd": caps,
		"RestartPolicy": map[string]any{"Name": s.Restart}, "Memory": mem, "NanoCpus": int64(cpus * 1e9),
		"Init": s.Init, "NetworkMode": s.network(),
	}
	if s.UsernsMode != "" {
		host["UsernsMode"] = s.UsernsMode
	}
	body := map[string]any{
		"Image": s.Image, "Hostname": s.Hostname, "Env": env, "Labels": s.labels(file), "ExposedPorts": exposed,
		"HostConfig": host,
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{
			s.network(): map[string]any{"Aliases": []string{"claude", s.Name}},
		}},
	}
	if s.User != "" {
		body["User"] = s.User
	}
	// compose counts a container as its own only with a config hash (to stop, remove or recreate
	// it). This is a hash of what was asked for, not compose's own, so compose's plain `up` makes
	// the container again, as it does whenever its configuration changed.
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	body["Labels"].(map[string]string)["com.docker.compose.config-hash"] = hex.EncodeToString(sum[:])
	return body, nil
}

// memBytes is a compose memory size (256m, 8g, 1024) in bytes.
func memBytes(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		n      int64
	}{{"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30}, {"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"b", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.n
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("not a size")
	}
	return n * mult, nil
}

// envFileVars are an env file's KEY=VALUE lines, in order, as compose reads them: blank lines and
// comments skipped, a quoted value unquoted.
func envFileVars(b []byte) [][2]string {
	var out [][2]string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(strings.TrimPrefix(k, "export ")), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out = append(out, [2]string{k, v})
	}
	return out
}

// demux copies a container's log stream, without a TTY: frames of an 8-byte header (stream, 0,0,0,
// size) and that many bytes, to stdout or stderr.
func demux(r io.Reader, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	br := bufio.NewReader(r)
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		w := stdout
		if hdr[0] == 2 {
			w = stderr
		}
		if _, err := io.CopyN(w, br, int64(binary.BigEndian.Uint32(hdr[4:]))); err != nil {
			return err
		}
	}
}
