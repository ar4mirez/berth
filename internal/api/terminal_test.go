package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/host/local"
	"github.com/ar4mirez/berth/internal/mcpsrv"
)

// fakeTerm is a terminal that prints a prompt, echoes what is typed, and ends on "done".
type fakeTerm struct {
	out    *io.PipeReader
	w      *io.PipeWriter
	mu     sync.Mutex
	typed  []string
	sizes  [][2]int
	closed bool
	end    chan int
	once   sync.Once
}

func newFakeTerm() *fakeTerm {
	r, w := io.Pipe()
	return &fakeTerm{out: r, w: w, end: make(chan int, 1)}
}

func (f *fakeTerm) Read(b []byte) (int, error) { return f.out.Read(b) }
func (f *fakeTerm) Write(b []byte) (int, error) {
	f.mu.Lock()
	f.typed = append(f.typed, string(b))
	f.mu.Unlock()
	if strings.Contains(string(b), "done") {
		go func() { _, _ = io.WriteString(f.w, "bye\r\n"); f.finish(0) }()
		return len(b), nil
	}
	go func() { _, _ = io.WriteString(f.w, "got "+string(b)) }()
	return len(b), nil
}
func (f *fakeTerm) Resize(c, r int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, [2]int{c, r})
	return nil
}
func (f *fakeTerm) finish(code int) { f.once.Do(func() { f.end <- code; _ = f.w.Close() }) }
func (f *fakeTerm) Wait() (int, error) {
	c := <-f.end
	f.end <- c
	return c, nil
}
func (f *fakeTerm) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.finish(130)
	return nil
}

func terminalServer(t *testing.T, by mcpsrv.Caller) (*httptest.Server, *[]*fakeTerm, *[][]string, string) {
	t.Helper()
	home := t.TempDir()
	st := config.State{Home: config.Home{Path: home, Source: config.SourceFlag}}
	var mu sync.Mutex
	terms, asked := []*fakeTerm{}, [][]string{}
	o := Options{
		Version: "test", Caller: func(*http.Request) (mcpsrv.Caller, error) { return by, nil },
		NewApp: func(stdout, stderr io.Writer) *app.App {
			return app.New(st, host.WithEngine(local.New(), ""), strings.NewReader(""), stdout, stderr, func(string) string { return "" })
		},
		Terminal: func(_ context.Context, args []string, cols, rows int) (Terminal, error) {
			mu.Lock()
			defer mu.Unlock()
			if slices.Contains(args, "t-broken") {
				return nil, errors.New("no terminal to be had")
			}
			f := newFakeTerm()
			f.sizes = append(f.sizes, [2]int{cols, rows})
			terms, asked = append(terms, f), append(asked, args)
			go func() { _, _ = io.WriteString(f.w, "Open https://example.com/device and paste the code: caf\xc3") }()
			go func() { time.Sleep(30 * time.Millisecond); _, _ = io.WriteString(f.w, "\xa9\r\n") }()
			return f, nil
		},
	}
	srv := httptest.NewServer(Handler(o))
	t.Cleanup(srv.Close)
	return srv, &terms, &asked, home
}

// events reads a terminal's stream: each event's name and data, until it ends.
func readEvents(t *testing.T, body io.Reader, on func(event, data string)) {
	t.Helper()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	event := ""
	for sc.Scan() {
		switch l := sc.Text(); {
		case strings.HasPrefix(l, "event: "):
			event = l[7:]
		case strings.HasPrefix(l, "data: "):
			on(event, l[6:])
		}
	}
}

// post is a request answered whole: its status, and its body.
func post(t *testing.T, url, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestTerminal: a sign-in runs on a terminal of its own, as events; what is typed reaches it; a
// character split across two reads arrives whole; and it ends with the command's exit code.
func TestTerminal(t *testing.T) {
	srv, terms, asked, home := terminalServer(t, mcpsrv.Caller{Writes: true, Restarts: true, Admin: true})
	resp, err := http.Post(srv.URL+"/v1/terminal", "application/json", strings.NewReader(`{"command":"signin","org":"acme@box1","cols":120,"rows":30}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	defer func() { _ = resp.Body.Close() }()
	var out strings.Builder
	var id, exit string
	typed := false
	readEvents(t, resp.Body, func(event, data string) {
		switch event {
		case "session":
			var s struct{ ID string }
			_ = json.Unmarshal([]byte(data), &s)
			id = s.ID
		case "output":
			var o struct{ Data string }
			if err := json.Unmarshal([]byte(data), &o); err != nil {
				t.Errorf("output %q: %v", data, err)
			}
			out.WriteString(o.Data)
			if !typed && strings.Contains(out.String(), "café") {
				typed = true
				if status, body := post(t, srv.URL+"/v1/terminal/"+id+"/input", `{"data":"ABCD-1234\r","cols":100,"rows":40}`); status != 200 {
					t.Errorf("input: %d %s", status, body)
				}
				if status, body := post(t, srv.URL+"/v1/terminal/"+id+"/input", `{"data":"done\r"}`); status != 200 {
					t.Errorf("input: %d %s", status, body)
				}
			}
		case "exit":
			exit = data
		}
	})
	got := out.String()
	if len(id) != 32 || exit != "0" || !strings.Contains(got, "paste the code: café\r\n") || strings.Contains(got, "�") ||
		!strings.Contains(got, "got ABCD-1234\r") || !strings.Contains(got, "bye") {
		t.Errorf("session %q, exit %q, output %q", id, exit, got)
	}
	f := (*terms)[0]
	if want := []string{"account", "signin", "acme@box1"}; !slices.Equal((*asked)[0], want) {
		t.Errorf("it ran berth %v, want %v", (*asked)[0], want)
	}
	if !slices.Equal(f.sizes, [][2]int{{120, 30}, {100, 40}}) || !slices.Equal(f.typed, []string{"ABCD-1234\r", "done\r"}) {
		t.Errorf("sizes %v, typed %v", f.sizes, f.typed)
	}
	// Once it has ended, there is nothing to type into.
	if status, body := post(t, srv.URL+"/v1/terminal/"+id+"/input", `{"data":"x"}`); status != 404 || !strings.Contains(body, "no such terminal") {
		t.Errorf("input after the end: %d %s", status, body)
	}
	// What was typed is not in the audit log: that a sign-in started is.
	log, _ := io.ReadAll(mustOpen(t, home+"/audit.log"))
	if !strings.Contains(string(log), `"tool":"terminal signin"`) || !strings.Contains(string(log), `"outcome":"started"`) || strings.Contains(string(log), "ABCD-1234") {
		t.Errorf("the audit log: %s", log)
	}

	// It runs berth's sign-ins and nothing else, for an org named as one.
	for body, want := range map[string]string{
		`{"command":"shell","org":"acme"}`:            "not \\\"shell\\\"",
		`{"command":"signin; rm -rf /","org":"acme"}`: "berth's sign-ins",
		`{"command":"signin","org":"--help"}`:         "org is an org's name",
		`{"command":"signin","org":"acme; id"}`:       "org is an org's name",
		`{"command":"signin"}`:                        "org is an org's name",
		`not json`:                                    "the body is",
		`{"command":"gh","org":"t-broken"}`:           "no terminal to be had",
	} {
		if status, answer := post(t, srv.URL+"/v1/terminal", body); status < 400 || !strings.Contains(answer, want) || !strings.Contains(answer, "berth.error/v1") {
			t.Errorf("%s: %d %s (want %q)", body, status, answer, want)
		}
	}
	if len(*terms) != 1 {
		t.Errorf("a refused request started a terminal: %d", len(*terms))
	}
}

// TestTerminalNeedsAdmin: a caller that may restart, and no more, gets no terminal: signing an org
// in is the operator's; and a caller that goes away ends the command.
func TestTerminalNeedsAdmin(t *testing.T) {
	srv, terms, _, _ := terminalServer(t, mcpsrv.Caller{Writes: true, Restarts: true})
	for _, c := range [][2]string{{"/v1/terminal", `{"command":"signin","org":"acme"}`}, {"/v1/terminal/abc/input", `{"data":"x"}`}} {
		if status, body := post(t, srv.URL+c[0], c[1]); status != 403 || !strings.Contains(body, "admin scope") {
			t.Errorf("%s without the admin scope: %d %s", c[0], status, body)
		}
	}
	if len(*terms) != 0 {
		t.Error("a terminal was started for a caller that may not")
	}

	srv, terms, _, _ = terminalServer(t, mcpsrv.Caller{Admin: true})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/terminal", strings.NewReader(`{"command":"login","org":"acme"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_, _ = resp.Body.Read(buf)
	cancel()
	_ = resp.Body.Close()
	f := (*terms)[0]
	for i := 0; ; i++ {
		f.mu.Lock()
		closed := f.closed
		f.mu.Unlock()
		if closed {
			break
		}
		if i > 200 {
			t.Fatal("the caller went away, and the command was left running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustOpen(t *testing.T, name string) io.Reader {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
