package parity

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// parityEngine is a Docker engine API on a unix socket for the parity scenarios (#43): images are
// there, nothing else is until asked for, and every request is recorded.
type parityEngine struct {
	sock string
	mu   sync.Mutex
	reqs []string
}

func (e *parityEngine) requests() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.reqs)
}

func startParityEngine(t *testing.T) *parityEngine {
	t.Helper()
	// A short path: a unix socket's must fit in 104 bytes on macOS.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &parityEngine{sock: filepath.Join(dir, "d.sock")}
	l, err := net.Listen("unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // #nosec G112 -- a test server
		_, _ = io.Copy(io.Discard, r.Body)
		e.mu.Lock()
		e.reqs = append(e.reqs, r.Method+" "+r.URL.Path)
		e.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/_ping":
			_, _ = io.WriteString(w, "OK")
		case r.Method == "GET" && strings.HasPrefix(p, "/images/"):
			_, _ = io.WriteString(w, "{}")
		case r.Method == "GET" && strings.HasSuffix(p, "/logs"):
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET", r.Method == "DELETE", strings.HasSuffix(p, "/stop"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"no such object"}`)
		case strings.HasSuffix(p, "/create"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"Id":"c0ffee"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return e
}

// orgCompose is a recorded call of an org's compose up, down or logs: what the API path takes.
var orgCompose = regexp.MustCompile(`^docker "compose" "-f" "[^"]+" "--env-file" "[^"]+" "(up|down|logs)"`)

// progress are the API path's own progress lines, which compose prints too but the fake doesn't.
var progress = regexp.MustCompile(`(?m)^ (Container|Network) \S+  \S+.*\n`)

// TestBerthEngineAPIPath: every ported scenario in which berth runs an org's compose up, down or
// logs behaves the same with BERTH_ENGINE_API=1 (#43, phase 3 of docs/prd/043-engine-api.md): the
// same output, exit code and files, and the same calls but for those compose ones, which become
// the engine API requests that do the same. The comparison with ccenv stays on compose
// (harness.go pins BERTH_ENGINE_API=0); TestEngineAPICompatibility checks the containers
// themselves against compose's, on Docker.
func TestBerthEngineAPIPath(t *testing.T) {
	berth := Berth(berthBin)
	var n atomic.Int32
	t.Run("scenarios", func(t *testing.T) {
		for _, sc := range scenarios {
			// A scenario that fakes compose's own answers (a port taken, a log) tests compose's side.
			if !ported[sc.Name] || slices.ContainsFunc(sc.Rules, func(r Rule) bool { return strings.Contains(r.Match, "compose") }) {
				continue
			}
			t.Run(sc.Name, func(t *testing.T) {
				t.Parallel() // each run has its own dir, fake-tool log and engine
				base := run(t, berth, sc.Scenario)
				var verbs []string
				builds := false
				for _, c := range base.Calls {
					if m := orgCompose.FindStringSubmatch(c); m != nil {
						verbs = append(verbs, m[1])
						builds = builds || m[1] == "up" && strings.Contains(c, `"--build"`)
					}
				}
				if len(verbs) == 0 {
					return
				}
				n.Add(1)
				eng := startParityEngine(t)
				s := sc.Scenario
				s.Env = append(slices.Clone(s.Env), "BERTH_ENGINE_API=1", "DOCKER_HOST=unix://"+eng.sock)
				api := run(t, berth, s)
				// compose up --build builds the image (compose.yml's build:): the API path runs that build itself.
				apiCalls := api.Calls
				if builds {
					apiCalls = slices.DeleteFunc(slices.Clone(apiCalls), func(c string) bool { return strings.HasPrefix(c, `docker "build" "-t" `) })
					if len(apiCalls) != len(api.Calls)-1 {
						t.Errorf("compose up --build, and the API path built %d times:\n%s", len(api.Calls)-len(apiCalls), strings.Join(api.Calls, "\n"))
					}
				}

				rest := slices.DeleteFunc(slices.Clone(base.Calls), func(c string) bool { return orgCompose.MatchString(c) })
				if d := Diff("compose", Result{Stdout: base.Stdout, Stderr: base.Stderr, Exit: base.Exit, Calls: rest, Tree: base.Tree},
					"engine API", Result{Stdout: api.Stdout, Stderr: progress.ReplaceAllString(api.Stderr, ""), Exit: api.Exit, Calls: apiCalls, Tree: api.Tree}); d != "" {
					t.Errorf("the Engine API path differs from compose:\n%s", d)
				}
				// Each compose call became the requests that do the same.
				reqs := strings.Join(eng.requests(), "\n")
				for _, v := range verbs {
					want := map[string][]string{
						"up":   {"POST /containers/create", "/start"},
						"down": {"DELETE /containers/", "DELETE /networks/"},
						"logs": {"/logs"},
					}[v]
					for _, w := range want {
						if !strings.Contains(reqs, w) {
							t.Errorf("compose %s, and no %s request:\n%s", v, w, reqs)
						}
					}
				}
			})
		}
	})
	if n.Load() < 15 {
		t.Errorf("only %d scenarios run an org's compose: the replay isn't covering what it should", n.Load())
	}
}
