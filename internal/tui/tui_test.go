package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/ar4mirez/berth/internal/ops"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii) // snapshots are plain text
	ops.BerthSpellings = true               // as the CLI sets it for berth's command line
	os.Exit(m.Run())
}

// fake is a fixed Backend: two orgs here, one on a host, and a host that can't be reached.
type fake struct {
	readOnly bool
	ran      []string // every action Run was asked for
	handoffs []string
	fail     error
}

func ip(n int) *int { return &n }

func (f *fake) ReadOnly() bool { return f.readOnly }

func (f *fake) Orgs(context.Context) ops.Orgs {
	return ops.Orgs{MultiHost: true, Orgs: []ops.OrgStatus{
		{Name: "acme", Manager: "berth", State: "up", SSHPort: ip(2201), TTYDPort: ip(7701), Token: true, Remote: "on", Host: "local"},
		{Name: "globex", Manager: "berth", State: "down", SSHPort: ip(2202), TTYDPort: ip(7702), Remote: "-", Host: "local"},
		{Name: "initech", Manager: "berth", State: "up", SSHPort: ip(2201), TTYDPort: ip(7701), Token: true, Remote: "login-needed", Host: "box1"},
	}, Unreachable: []ops.HostError{{Host: "box2", Error: "ssh: connection refused"}}}
}

func (f *fake) Hosts(context.Context) (ops.Hosts, error) {
	return ops.Hosts{Hosts: []ops.HostStatus{
		{Name: "local", Kind: "local", Reachable: true, Engine: "docker", Docker: "29.0.0", Orgs: ip(2)},
		{Name: "box1", Kind: "ssh", Address: "ops@box1.example:22", Reachable: true, Engine: "docker", Docker: "28.1.0", Orgs: ip(1)},
		{Name: "box2", Kind: "ssh", Address: "ops@box2.example:22", Error: "ssh: connection refused"},
	}}, nil
}

func (f *fake) Detail(_ context.Context, org string) Detail {
	live := "on 159"
	return Detail{Org: org, Errs: map[string]error{},
		Info: ops.Info{Org: org, Container: "claude-" + org, State: "running", RemoteURL: "https://claude.ai/code?environment=env_x",
			Address: "100.64.0.7", SSHPort: ip(2201), TTYDPort: ip(7701), User: "node", SSHHost: "100.64.0.7", GitPublicKey: "ssh-ed25519 AAAAC3Nza… claude-acme"},
		Firewall: ops.Firewall{Org: org, Entries: []string{"mode on", "@python", "pypi.org", "10.0.0.0/8"}, Live: &live},
		Repos: ops.Repos{Org: org, Repos: []ops.Repo{
			{Dir: "app", Repo: "github.com/acme/app", Branch: "main", State: "cloned"},
			{Dir: "notes", Local: true, State: "cloned"},
			{Dir: "api", Repo: "github.com/acme/api", Branch: "develop", State: "missing"},
		}, RepoAudit: ops.RepoAudit{Policy: "enforce", Unregistered: []string{"scratch"}}},
		Env:      ops.Env{Org: org, Vars: []ops.EnvVar{{Name: "OPENROUTER_API_KEY", Present: true}, {Name: "SENTRY_DSN"}}},
		Packages: ops.Packages{Packages: []string{"libpq-dev"}},
		Backups: ops.Backups{Dir: "/state/backups", Backups: []ops.Backup{
			{File: "acme-20261006-023000.tar.zst.age", Org: "acme", Stamp: "20261006-023000", Modified: "2026-10-06T02:31:10Z", Size: 734003200, Encryption: "age"},
		}},
	}
}

func (f *fake) Logs(context.Context, string, int) (ops.Logs, error) {
	return ops.Logs{Lines: []string{"firewall: ON (159 allowlisted networks)", "claude-env[acme]: ready", "remote-control: capacity 0/8"}}, nil
}

func (f *fake) Run(_ context.Context, a Action) (string, error) {
	f.ran = append(f.ran, a.Label())
	return "done: " + a.Label(), f.fail
}

func (f *fake) Handoff(_ context.Context, org, kind string) error {
	f.handoffs = append(f.handoffs, kind+" "+org)
	return nil
}

// dash is a dashboard on f, sized and with its list loaded.
func dash(t *testing.T, f *fake, w, h int) *Model {
	t.Helper()
	m := New(context.Background(), f)
	m.exec = func(c tea.ExecCommand, done tea.ExecCallback) tea.Cmd { return func() tea.Msg { return done(c.Run()) } }
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(m.loadList()())
	return m
}

// press sends keys, and runs what each one starts to its end (a refresh tick is never started by
// a key).
func press(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.Msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		}
		_, cmd := m.Update(msg)
		for cmd != nil {
			next := cmd()
			if _, quit := next.(tea.QuitMsg); quit || next == nil {
				break
			}
			_, cmd = m.Update(next)
		}
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	// Trailing spaces are padding; the file keeps none.
	lines := strings.Split(got, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	got = strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	p := filepath.Join("testdata", name+".txt")
	if os.Getenv("BERTH_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (BERTH_UPDATE_GOLDEN=1 go test ./internal/tui writes it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs (BERTH_UPDATE_GOLDEN=1 rewrites it)\n--- got\n%s--- want\n%s", name, got, want)
	}
}

// TestViews: a snapshot of each main view.
func TestViews(t *testing.T) {
	for name, keys := range map[string][]string{
		"list":            nil,
		"list-second":     {"down"},
		"org-info":        {"enter"},
		"org-firewall":    {"enter", "2", "down"},
		"org-repos":       {"enter", "3", "down", "down"},
		"org-env":         {"enter", "4"},
		"org-backups":     {"enter", "5"},
		"org-logs":        {"enter", "6"},
		"help":            {"?"},
		"confirm-restart": {"r"},
		"confirm-deny":    {"enter", "2", "down", "down", "x"},
		"input-allow":     {"enter", "2", "a", "p", "y", "p", "i", ".", "o", "r", "g"},
		"result":          {"enter", "5", "b"},
		"host-org":        {"down", "down", "enter"},
	} {
		m := dash(t, &fake{}, 100, 24)
		press(m, keys...)
		golden(t, name, m.View())
	}
	// A small terminal, and one too small to draw in.
	m := dash(t, &fake{}, 60, 12)
	golden(t, "small", m.View())
	m = dash(t, &fake{}, 30, 8)
	if v := m.View(); !strings.Contains(v, "needs 40×10 or more") {
		t.Errorf("a tiny terminal: %q", v)
	}
	// No line is wider than the terminal, whatever the view.
	m = dash(t, &fake{}, 50, 14)
	for _, keys := range [][]string{nil, {"enter"}, {"3"}, {"?"}} {
		press(m, keys...)
		for _, l := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(l) > 50 {
				t.Errorf("a line %d wide in a terminal of 50: %q", lipgloss.Width(l), l)
			}
		}
		if n := strings.Count(m.View(), "\n") + 1; n != 14 {
			t.Errorf("%d lines in a terminal of 14", n)
		}
	}
}

// TestRestartsNeedConfirmation: an action whose operation restarts a container, in the catalog's
// words, never runs without a "y"; the confirmation says what stops; and the ones that remove
// something ask too.
func TestRestartsNeedConfirmation(t *testing.T) {
	for key, cmd := range map[string]string{"u": "up", "d": "down", "r": "restart"} {
		op, ok := ops.Lookup(cmd, "")
		if !ok || op.Restart == ops.Never {
			t.Fatalf("%s isn't a restart in the catalog", cmd)
		}
		for _, answer := range []string{"enter", "n", "esc", " ", "q", "u"} {
			f := &fake{}
			m := dash(t, f, 100, 24)
			press(m, key)
			if m.over != confirm || !strings.Contains(m.View(), "stops the work running in acme") || !strings.Contains(m.View(), op.Note) {
				t.Fatalf("%s: no confirmation naming what stops:\n%s", cmd, m.View())
			}
			press(m, answer)
			if len(f.ran) != 0 || m.over != none {
				t.Errorf("%s then %q: ran %v", cmd, answer, f.ran)
			}
		}
		f := &fake{}
		m := dash(t, f, 100, 24)
		press(m, key, "y")
		if len(f.ran) != 1 || f.ran[0] != "berth "+cmd+" acme" {
			t.Errorf("%s then y: ran %v", cmd, f.ran)
		}
	}
	// From an org's page too, and for the org on a host.
	f := &fake{}
	m := dash(t, f, 100, 24)
	press(m, "down", "down", "enter", "r")
	if len(f.ran) != 0 || !strings.Contains(m.View(), "berth restart initech@box1") {
		t.Errorf("restart on a page: ran %v\n%s", f.ran, m.View())
	}
	press(m, "y")
	if len(f.ran) != 1 || f.ran[0] != "berth restart initech@box1" {
		t.Errorf("confirmed: ran %v", f.ran)
	}
	// Removing asks; adding doesn't.
	f = &fake{}
	m = dash(t, f, 100, 24)
	press(m, "enter", "2", "down", "x")
	if len(f.ran) != 0 || m.over != confirm {
		t.Errorf("fw deny ran without a confirmation: %v", f.ran)
	}
	press(m, "y", "esc")
	press(m, "3", "x", "n", "y")
	press(m, "esc", "2", "a", "@", "g", "o", "enter")
	want := []string{"berth fw deny acme @python", "berth repo sync acme", "berth fw allow acme @go"}
	if strings.Join(f.ran, "|") != strings.Join(want, "|") {
		t.Errorf("ran %v, want %v", f.ran, want)
	}
}

// TestReadOnly: with --read-only no write runs and none is offered a confirmation, attach and
// shell included (they act as the org); reading goes on.
func TestReadOnly(t *testing.T) {
	f := &fake{readOnly: true}
	m := dash(t, f, 100, 24)
	if !strings.Contains(m.View(), "read-only") {
		t.Error("the header doesn't say read-only")
	}
	for _, keys := range [][]string{{"u"}, {"d"}, {"r"}, {"A"}, {"S"}, {"enter", "2", "x"}, {"3", "y"}, {"5", "b"}} {
		press(m, keys...)
		if m.over != result || !m.failed || !strings.Contains(m.View(), "read-only mode (--read-only): refusing to run berth ") {
			t.Errorf("%v under --read-only:\n%s", keys, m.View())
		}
		press(m, "y") // closes the message; confirms nothing
	}
	press(m, "2", "a", "x", ".", "o", "r", "g", "enter")
	if len(f.ran) != 0 || len(f.handoffs) != 0 {
		t.Errorf("under --read-only: ran %v, handed off %v", f.ran, f.handoffs)
	}
	press(m, "y", "6")
	if !strings.Contains(m.View(), "claude-env[acme]: ready") {
		t.Errorf("reading stopped:\n%s", m.View())
	}
}

// TestHandoffAndFailure: attach and shell hand the terminal over for the selected org; a failed
// action shows berth's message and hint, respelled.
func TestHandoffAndFailure(t *testing.T) {
	f := &fake{}
	m := dash(t, f, 100, 24)
	press(m, "A", "down", "S")
	if strings.Join(f.handoffs, "|") != "attach acme|shell globex" {
		t.Errorf("handoffs: %v", f.handoffs)
	}
	f.fail = &ops.Error{Kind: ops.KindState, Code: 1, Msg: "claude-globex is not running", Hint: "berth up globex, then berth login globex"}
	press(m, "enter", "3", "y")
	if !m.failed || !strings.Contains(m.View(), "claude-globex is not running (berth up globex, then berth account login globex)") {
		t.Errorf("a failed action:\n%s", m.View())
	}
	f.fail = errors.New("boom")
	press(m, "y", "y")
	if !strings.Contains(m.View(), "boom") {
		t.Errorf("a plain error:\n%s", m.View())
	}
	// Every action the dashboard can ask for is an operation of the catalog.
	for _, a := range []Action{{Cmd: "up"}, {Cmd: "down"}, {Cmd: "restart"}, {Cmd: "fw", Sub: "allow"}, {Cmd: "fw", Sub: "deny"},
		{Cmd: "repo", Sub: "add"}, {Cmd: "repo", Sub: "rm"}, {Cmd: "repo", Sub: "sync"}, {Cmd: "backup"}} {
		if op, ok := ops.Lookup(a.Cmd, a.Sub); !ok || op.Access != ops.Write {
			t.Errorf("%s %s isn't a write in the catalog", a.Cmd, a.Sub)
		}
	}
}
