package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/ops"
)

// A terminal, for what needs one (#167, phase 4): signing an org in. The command line gives
// `berth account signin` the operator's terminal; a browser has none, so the API runs the command
// on a pseudo-terminal of its own and passes what it prints and what is typed. It runs berth's own
// sign-in commands and nothing else: it is not a shell.

// Terminal is a command running on a terminal of its own.
type Terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	// Wait is the command's exit code, once it has ended.
	Wait() (int, error)
}

// TerminalCommand is one of the commands a terminal may run.
type TerminalCommand struct {
	// Args are berth's arguments before the org.
	Args []string
	// About is what it does.
	About string
}

// TerminalCommands are the commands a terminal may run, by the name a caller asks with: the
// sign-ins, each of which shows a link to open and waits for a code.
var TerminalCommands = map[string]TerminalCommand{
	"signin": {Args: []string{"account", "signin"}, About: "Sign an org in to its Claude account: its long-lived token, then the login Remote Control uses"},
	"login":  {Args: []string{"account", "login"}, About: "Sign an org in for Remote Control and claude.ai/code, when it has its token already"},
	"gh":     {Args: []string{"account", "gh"}, About: "Sign an org's gh CLI in to GitHub, with a one-time code"},
}

// TerminalRequest is the body of POST /v1/terminal.
type TerminalRequest struct {
	Command string `json:"command"`
	Org     string `json:"org"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

// TerminalInput is the body of POST /v1/terminal/{id}/input: what was typed, and optionally the
// terminal's new size.
type TerminalInput struct {
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// maxTerminals is how many terminals one server runs at once: a sign-in is one person's.
const maxTerminals = 4

var orgAddress = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(@[a-z0-9][a-z0-9-]*)?$`)

type terminals struct {
	mu   sync.Mutex
	open map[string]Terminal
}

func (t *terminals) add(term Terminal) (string, bool) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", false
	}
	id := hex.EncodeToString(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.open) >= maxTerminals {
		return "", false
	}
	if t.open == nil {
		t.open = map[string]Terminal{}
	}
	t.open[id] = term
	return id, true
}

func (t *terminals) get(id string) Terminal {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.open[id]
}

func (t *terminals) remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.open, id)
}

// admin is the caller of a terminal endpoint: one that may do everything, or the refusal.
func (o Options) admin(w http.ResponseWriter, req *http.Request) bool {
	by, err := o.caller(req)
	switch {
	case err != nil:
		fail(w, err)
	case o.Terminal == nil:
		fail(w, &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: "this server has no terminal"})
	case !by.Admin:
		fail(w, &ops.Error{Kind: ops.KindRefused, Code: 1, Msg: "a terminal signs an org in, which is the operator's to do: it needs the admin scope, and this caller may not: refused"})
	default:
		return true
	}
	return false
}

// terminal is POST /v1/terminal: one of berth's sign-in commands on a terminal of its own, as
// events: `session` (its id, to type into), `output` (what it prints, as text), then `exit` (its
// exit code). It ends when the command does, or when the caller goes away.
func (o Options) terminal(ts *terminals) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !o.admin(w, req) {
			return
		}
		var in TerminalRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody)).Decode(&in); err != nil {
			fail(w, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: `the body is {"command": "signin", "org": "acme"}`})
			return
		}
		cmd, ok := TerminalCommands[in.Command]
		if !ok {
			names := make([]string, 0, len(TerminalCommands))
			for n := range TerminalCommands {
				names = append(names, n)
			}
			sort.Strings(names)
			fail(w, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "a terminal runs berth's sign-ins, not " + jsonString(in.Command), Hint: "command is one of: " + joinWords(names)})
			return
		}
		if !orgAddress.MatchString(in.Org) {
			fail(w, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "org is an org's name, as in acme, or acme@box1"})
			return
		}
		audit := func(outcome string) {
			a := o.NewApp(io.Discard, io.Discard)
			_ = a.Audit(req.Context(), app.AuditEntry{Via: "api", Tool: "terminal " + in.Command, Outcome: outcome, Args: map[string]any{"org": in.Org}})
		}
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		term, err := o.Terminal(ctx, append(append([]string{}, cmd.Args...), in.Org), in.Cols, in.Rows)
		if err != nil {
			audit("failed")
			fail(w, err)
			return
		}
		defer func() { _ = term.Close() }()
		id, ok := ts.add(term)
		if !ok {
			audit("refused")
			fail(w, &ops.Error{Kind: ops.KindState, Code: 1, Msg: "there are as many terminals open as this server runs at once", Hint: "close one, or wait for a sign-in to end"})
			return
		}
		defer ts.remove(id)
		audit("started")
		s := newStream(w)
		s.send("session", map[string]string{"id": id})
		// The caller going away ends the command: nobody is left to read it.
		go func() {
			<-ctx.Done()
			_ = term.Close()
		}()
		buf, carry := make([]byte, 8192), []byte(nil)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				// Whole characters only: one split across two reads waits for its other half.
				carry = append(carry, buf[:n]...)
				data := carry
				cut := len(data)
				for cut > 0 && cut > len(data)-utf8.UTFMax && !utf8.Valid(data[:cut]) {
					cut--
				}
				if !utf8.Valid(data[:cut]) {
					cut = len(data) // not text at all: sent as it is, with what isn't replaced
				}
				s.send("output", map[string]string{"data": string(data[:cut])})
				carry = append([]byte(nil), data[cut:]...)
			}
			if err != nil {
				break
			}
		}
		code, _ := term.Wait()
		s.send("exit", code)
	}
}

// terminalInput is POST /v1/terminal/{id}/input: what is typed into a terminal, and its size when
// that changed.
func (o Options) terminalInput(ts *terminals) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !o.admin(w, req) {
			return
		}
		term := ts.get(req.PathValue("id"))
		if term == nil {
			fail(w, &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: "no such terminal: it ended, or never was"})
			return
		}
		var in TerminalInput
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&in); err != nil {
			fail(w, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: `the body is {"data": "what was typed"}`})
			return
		}
		if in.Cols > 0 && in.Rows > 0 {
			_ = term.Resize(in.Cols, in.Rows)
		}
		if in.Data != "" {
			if _, err := io.WriteString(term, in.Data); err != nil {
				fail(w, &ops.Error{Kind: ops.KindState, Code: 1, Msg: "the terminal has ended"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func joinWords(w []string) string {
	out := ""
	for i, s := range w {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
