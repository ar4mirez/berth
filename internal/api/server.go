package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/mcpsrv"
	"github.com/ar4mirez/berth/internal/ops"
)

// Options is how the server was started.
type Options struct {
	// Version is berth's.
	Version string
	// ReadOnly is berth's --read-only: no caller may write.
	ReadOnly bool
	// NewApp makes the App one request runs against, printing into the buffers given.
	NewApp func(stdout, stderr io.Writer) *app.App
	// Caller says who a request comes from and what they may do. An error refuses the request.
	Caller func(*http.Request) (mcpsrv.Caller, error)
}

// Info is `GET /v1`: what is serving.
type Info struct {
	Schema   string `json:"schema"` // "berth.api/v1"
	Version  string `json:"version"`
	ReadOnly bool   `json:"read_only"`
	// Writes and Restarts are what this caller may do.
	Writes   bool `json:"writes"`
	Restarts bool `json:"restarts"`
}

// maxBody is the largest request body: arguments are names and short lists.
const maxBody = 1 << 20

// Specs are the tools by name, as the API serves them.
func Specs() map[string]mcpsrv.Spec {
	_, specs := mcpsrv.New(mcpsrv.Options{})
	out := map[string]mcpsrv.Spec{}
	for _, s := range specs {
		out[s.Name] = s
	}
	return out
}

// Handler is the API.
func Handler(o Options) http.Handler {
	_, list := mcpsrv.New(mcpsrv.Options{NewApp: o.NewApp, Version: o.Version})
	specs := map[string]mcpsrv.Spec{}
	for _, s := range list {
		specs[s.Name] = s
	}
	mux := http.NewServeMux()
	for _, r := range Routes {
		spec, ok := specs[r.Tool]
		if !ok {
			panic("api: route " + r.Path + " serves a tool that doesn't exist: " + r.Tool)
		}
		mux.HandleFunc(r.Method+" "+r.Path, o.tool(r, spec))
	}
	mux.HandleFunc("GET /"+Version, func(w http.ResponseWriter, req *http.Request) {
		by, err := o.caller(req)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, Info{Schema: "berth.api/v1", Version: o.Version, ReadOnly: o.ReadOnly, Writes: by.Writes, Restarts: by.Restarts})
	})
	mux.HandleFunc("GET /"+Version+"/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(OpenAPI())
	})
	mux.HandleFunc("GET /"+Version+"/orgs/{org}/logs/follow", o.follow)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, pattern := mux.Handler(req); pattern == "" {
			// Not one of ours: say so as the API says everything, in a document.
			fail(w, &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: fmt.Sprintf("no %s %s in this API", req.Method, req.URL.Path), Hint: "GET /v1/openapi.json lists the endpoints"})
			return
		}
		mux.ServeHTTP(w, req)
	})
}

// caller is who asks, limited by --read-only.
func (o Options) caller(req *http.Request) (mcpsrv.Caller, error) {
	by, err := o.Caller(req)
	if err != nil {
		return by, err
	}
	by.Via = "api"
	if o.ReadOnly {
		by.Writes, by.Restarts = false, false
	}
	return by, nil
}

// arguments gathers a route's arguments into one JSON object: the path's, the query's, then the
// body's. A body may not name an argument the path or the query gave.
func arguments(r Route, in reflect.Type, req *http.Request) (json.RawMessage, error) {
	usage := func(format string, a ...any) error {
		return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf(format, a...)}
	}
	args := map[string]any{}
	if req.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(nil, req.Body, maxBody))
		if err != nil {
			return nil, usage("the request body is too large or unreadable")
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &args); err != nil {
				return nil, usage("the request body isn't a JSON object: %v", err)
			}
		}
	}
	q := req.URL.Query()
	for _, a := range r.Args(in) {
		switch a.In {
		case "path":
			if _, dup := args[a.Name]; dup {
				return nil, usage("%s is in the path: leave it out of the body", a.Name)
			}
			args[a.Name] = req.PathValue(a.Name)
		case "query":
			vals, ok := q[a.Name]
			if !ok {
				continue
			}
			if _, dup := args[a.Name]; dup {
				return nil, usage("%s is given twice: in the query and in the body", a.Name)
			}
			switch a.Type.Kind() {
			case reflect.Slice:
				var all []string
				for _, v := range vals {
					all = append(all, strings.Split(v, ",")...)
				}
				args[a.Name] = all
			case reflect.Int:
				n, err := strconv.Atoi(vals[0])
				if err != nil {
					return nil, usage("%s must be a number, not %q", a.Name, vals[0])
				}
				args[a.Name] = n
			case reflect.Bool:
				b, err := strconv.ParseBool(vals[0])
				if err != nil {
					return nil, usage("%s must be true or false, not %q", a.Name, vals[0])
				}
				args[a.Name] = b
			default:
				args[a.Name] = vals[0]
			}
			q.Del(a.Name)
		}
	}
	for _, a := range r.Args(in) {
		q.Del(a.Name)
	}
	for name := range q {
		return nil, usage("%s %s has no query parameter %q", r.Method, r.Path, name)
	}
	return json.Marshal(args)
}

// tool serves one tool: as a document, or, when the client asks for an event stream, as the
// operation's progress followed by its result.
func (o Options) tool(r Route, spec mcpsrv.Spec) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		by, err := o.caller(req)
		if err != nil {
			fail(w, err)
			return
		}
		args, err := arguments(r, spec.In, req)
		if err != nil {
			fail(w, err)
			return
		}
		if !strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
			out, err := spec.Invoke(req.Context(), by, args)
			if err != nil {
				fail(w, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		s := newStream(w)
		by.Events = func(e ops.Event) { s.send(e.Type, e) }
		if out, err := spec.Invoke(req.Context(), by, args); err == nil {
			s.send("result", out) // after "done"; a failure's last event is "failed", with the error
		} else if !s.sent() {
			s.send("failed", ops.AsError(err).Doc()) // refused before the operation began
		}
	}
}

// follow is `GET /v1/orgs/{org}/logs/follow`: the container's log as events, until the client
// goes away.
func (o Options) follow(w http.ResponseWriter, req *http.Request) {
	if _, err := o.caller(req); err != nil {
		fail(w, err)
		return
	}
	s := newStream(w)
	p := ops.NewProgress(func(e ops.Event) { s.send(e.Type, e) }, "logs", req.PathValue("org"))
	a := o.NewApp(p.Writer("stdout"), p.Writer("stderr"))
	b, org, done, err := a.At(req.Context(), req.PathValue("org"))
	if err != nil {
		p.Done(err)
		return
	}
	defer done()
	err = b.Logs(req.Context(), org)
	if req.Context().Err() != nil {
		err = nil // the client left: that is how a follow ends
	}
	p.Done(err)
}

// stream writes server-sent events.
type stream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	n  int
}

func newStream(w http.ResponseWriter) *stream {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return &stream{w: w}
}

func (s *stream) send(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *stream) sent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n > 0
}

// Status is the HTTP status of an error's kind.
func Status(k ops.Kind) int {
	switch k {
	case ops.KindUsage:
		return http.StatusBadRequest
	case ops.KindNotFound:
		return http.StatusNotFound
	case ops.KindRefused:
		return http.StatusForbidden
	case ops.KindNotRunning, ops.KindState:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// ErrUnauthorized is a request with no credentials, or wrong ones.
var ErrUnauthorized = errors.New("unauthorized")

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnauthorized) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="berth"`)
		writeJSON(w, http.StatusUnauthorized, ops.ErrorDoc{Schema: "berth.error/v1", Kind: ops.KindRefused, Code: 1, Message: "this API needs a token (Authorization: Bearer …)"})
		return
	}
	e := ops.AsError(err)
	writeJSON(w, Status(e.Kind), e.Doc())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// Local is the caller on the Unix socket: whoever can open it is its owner, who could run berth
// anyway.
func Local(*http.Request) (mcpsrv.Caller, error) {
	return mcpsrv.Caller{Writes: true, Restarts: true}, nil
}
