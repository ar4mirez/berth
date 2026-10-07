package tui

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ar4mirez/berth/internal/ops"
)

// The tabs of an org's page.
const (
	tabInfo     = "Info"
	tabFirewall = "Firewall"
	tabRepos    = "Repos"
	tabEnv      = "Env"
	tabBackups  = "Backups"
	tabLogs     = "Logs"
)

var tabs = []string{tabInfo, tabFirewall, tabRepos, tabEnv, tabBackups, tabLogs}

type overlay int

const (
	none    overlay = iota
	help            // the keys
	confirm         // an action that must be confirmed
	input           // one line to type (an entry to allow, a repo to add)
	result          // what an action printed, or why it didn't run
)

// refresh is how often the page on screen is read again.
const refresh = 3 * time.Second

// confirmFirst are the writes that remove something: they ask first, though nothing restarts.
var confirmFirst = map[string]bool{"repo rm": true, "fw deny": true}

type (
	listMsg struct {
		orgs     ops.Orgs
		hosts    ops.Hosts
		hostsErr error
	}
	detailMsg Detail
	logsMsg   struct {
		org   string
		lines []string
		err   error
	}
	doneMsg struct {
		action Action
		out    string
		err    error
	}
	handoffMsg struct{ err error }
	tickMsg    struct{}
)

// Model is the dashboard.
type Model struct {
	b   Backend
	ctx context.Context

	w, h int

	orgs     ops.Orgs
	hosts    ops.Hosts
	hostsErr error
	loaded   bool
	cur      int // the selected org in the list

	open    string // the org whose page is open ("" for the list)
	tab     int
	d       Detail
	dLoaded bool
	sel     map[string]int // the selected row of each tab
	logs    []string
	logsErr error

	over    overlay
	pending Action // the action a confirmation or an input is about
	ask     string // the confirmation's text, or the input's prompt
	typed   string
	// submit turns what was typed into the action to request.
	submit func(string) Action
	busy   string // the action running now
	// exec runs something with the terminal released (tea.Exec; the tests run it in place).
	exec   func(tea.ExecCommand, tea.ExecCallback) tea.Cmd
	text   string // the result overlay's text
	failed bool
}

// New is the dashboard on a backend.
func New(ctx context.Context, b Backend) *Model {
	return &Model{b: b, ctx: ctx, w: 100, h: 30, sel: map[string]int{}, exec: tea.Exec}
}

func (m *Model) Init() tea.Cmd { return tea.Batch(m.loadList(), tick()) }

func tick() tea.Cmd { return tea.Tick(refresh, func(time.Time) tea.Msg { return tickMsg{} }) }

func (m *Model) loadList() tea.Cmd {
	return func() tea.Msg {
		hosts, err := m.b.Hosts(m.ctx)
		return listMsg{orgs: m.b.Orgs(m.ctx), hosts: hosts, hostsErr: err}
	}
}

func (m *Model) loadDetail() tea.Cmd {
	org := m.open
	return func() tea.Msg { return detailMsg(m.b.Detail(m.ctx, org)) }
}

func (m *Model) loadLogs() tea.Cmd {
	org, n := m.open, max(m.h, 20)
	return func() tea.Msg {
		l, err := m.b.Logs(m.ctx, org, n)
		return logsMsg{org: org, lines: l.Lines, err: err}
	}
}

// reload reads the page on screen again.
func (m *Model) reload() tea.Cmd {
	switch {
	case m.open == "":
		return m.loadList()
	case tabs[m.tab] == tabLogs:
		return m.loadLogs()
	}
	return m.loadDetail()
}

// address is an org as commands take it: acme, or acme@box1.
func address(o ops.OrgStatus) string {
	if o.Host == "" || o.Host == "local" {
		return o.Name
	}
	return o.Name + "@" + o.Host
}

// selected is the org the cursor is on, or the open one.
func (m *Model) selected() string {
	if m.open != "" {
		return m.open
	}
	if m.cur < len(m.orgs.Orgs) {
		return address(m.orgs.Orgs[m.cur])
	}
	return ""
}

// request is the gate every action goes through: what the catalog says of its operation, against
// how berth was started. A write is refused under --read-only; an operation that restarts a
// container, or removes something, runs only once confirmed.
func (m *Model) request(a Action) tea.Cmd {
	if a.Org == "" {
		return nil
	}
	op, ok := ops.Lookup(a.Cmd, a.Sub)
	switch {
	case !ok:
		m.show(fmt.Sprintf("no operation %s %s in the catalog", a.Cmd, a.Sub), true)
	case op.Access == ops.Write && m.b.ReadOnly():
		m.show("read-only mode (--read-only): refusing to run "+a.Label(), true)
	case op.Restart != ops.Never:
		m.over, m.pending = confirm, a
		m.ask = fmt.Sprintf("%s\n\nThis %s.\nIt stops the work running in %s.", a.Label(), op.Note, a.Org)
	case confirmFirst[a.Cmd+" "+a.Sub]:
		m.over, m.pending = confirm, a
		m.ask = a.Label()
	default:
		return m.run(a)
	}
	return nil
}

// run does an action. Only request and a confirmed confirmation call it.
func (m *Model) run(a Action) tea.Cmd {
	m.over, m.busy = none, a.Label()
	return func() tea.Msg {
		out, err := m.b.Run(m.ctx, a)
		return doneMsg{action: a, out: out, err: err}
	}
}

// handoff gives the terminal to attach or shell, and comes back to the dashboard after.
func (m *Model) handOff(kind string) tea.Cmd {
	org := m.selected()
	if org == "" {
		return nil
	}
	if op, _ := ops.Lookup(kind, ""); op.Access == ops.Write && m.b.ReadOnly() {
		m.show("read-only mode (--read-only): refusing to run berth "+kind+" "+org, true)
		return nil
	}
	return m.exec(handoff{func() error { return m.b.Handoff(m.ctx, org, kind) }}, func(err error) tea.Msg { return handoffMsg{err} })
}

// handoff is what Bubble Tea runs while the terminal is someone else's: it has released it, and
// takes it back when Run returns. The process berth starts opens the terminal itself.
type handoff struct{ run func() error }

func (h handoff) Run() error        { return h.run() }
func (handoff) SetStdin(io.Reader)  {}
func (handoff) SetStdout(io.Writer) {}
func (handoff) SetStderr(io.Writer) {}

func (m *Model) show(text string, failed bool) {
	m.over, m.text, m.failed = result, ops.Respell(text), failed
}

func (m *Model) prompt(ask string, submit func(string) Action) {
	m.over, m.ask, m.typed, m.submit = input, ask, "", submit
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 && msg.Height > 0 { // a terminal that doesn't say keeps the default size
			m.w, m.h = msg.Width, msg.Height
		}
	case listMsg:
		m.orgs, m.hosts, m.hostsErr, m.loaded = msg.orgs, msg.hosts, msg.hostsErr, true
		m.cur = clamp(m.cur, len(m.orgs.Orgs))
	case detailMsg:
		if msg.Org == m.open {
			m.d, m.dLoaded = Detail(msg), true
		}
	case logsMsg:
		if msg.org == m.open {
			m.logs, m.logsErr = msg.lines, msg.err
		}
	case tickMsg:
		if m.busy != "" || m.over == input || m.over == confirm {
			return m, tick()
		}
		return m, tea.Batch(m.reload(), tick())
	case doneMsg:
		m.busy = ""
		switch {
		case msg.err != nil:
			e := ops.AsError(msg.err)
			m.show(strings.TrimLeft(msg.out+"\n"+e.Error(), "\n"), true)
		case msg.out != "":
			m.show(msg.out, false)
		}
		return m, m.reload()
	case handoffMsg:
		if msg.err != nil {
			m.show(msg.err.Error(), true)
		}
		return m, m.reload()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func clamp(i, n int) int {
	if i >= n {
		i = n - 1
	}
	return max(i, 0)
}

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.over {
	case confirm:
		// Only y confirms: Enter and everything else leave it undone.
		if s == "y" || s == "Y" {
			return m, m.run(m.pending)
		}
		m.over = none
		return m, nil
	case input:
		switch k.Type {
		case tea.KeyEnter:
			m.over = none
			if v := strings.TrimSpace(m.typed); v != "" {
				return m, m.request(m.submit(v))
			}
		case tea.KeyEsc:
			m.over = none
		case tea.KeyBackspace:
			if r := []rune(m.typed); len(r) > 0 {
				m.typed = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			m.typed += string(k.Runes)
		}
		return m, nil
	case help, result:
		m.over = none
		return m, nil
	}
	if m.busy != "" {
		return m, nil // one action at a time
	}
	switch s {
	case "q":
		return m, tea.Quit
	case "?":
		m.over = help
		return m, nil
	case "R":
		return m, m.reload()
	case "u":
		return m, m.request(Action{Cmd: "up", Org: m.selected()})
	case "d":
		return m, m.request(Action{Cmd: "down", Org: m.selected()})
	case "r":
		return m, m.request(Action{Cmd: "restart", Org: m.selected()})
	case "A":
		return m, m.handOff("attach")
	case "S":
		return m, m.handOff("shell")
	}
	if m.open == "" {
		return m.listKey(s)
	}
	return m.detailKey(s)
}

func (m *Model) listKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "up", "k":
		m.cur = clamp(m.cur-1, len(m.orgs.Orgs))
	case "down", "j":
		m.cur = clamp(m.cur+1, len(m.orgs.Orgs))
	case "enter", "right", "l":
		if o := m.selected(); o != "" {
			m.open, m.tab, m.dLoaded, m.logs, m.sel = o, 0, false, nil, map[string]int{}
			return m, m.loadDetail()
		}
	}
	return m, nil
}

// rows is how many rows the open tab has to move over.
func (m *Model) rows() int {
	switch tabs[m.tab] {
	case tabFirewall:
		return len(m.d.Firewall.Entries)
	case tabRepos:
		return len(m.d.Repos.Repos)
	}
	return 0
}

func (m *Model) detailKey(s string) (tea.Model, tea.Cmd) {
	t := tabs[m.tab]
	switch s {
	case "esc", "left", "h", "backspace":
		m.open = ""
		return m, m.loadList()
	case "tab", "]":
		m.tab = (m.tab + 1) % len(tabs)
		return m, m.reload()
	case "shift+tab", "[":
		m.tab = (m.tab + len(tabs) - 1) % len(tabs)
		return m, m.reload()
	case "1", "2", "3", "4", "5", "6":
		m.tab, _ = strconv.Atoi(s)
		m.tab--
		return m, m.reload()
	case "up", "k":
		m.sel[t] = clamp(m.sel[t]-1, m.rows())
	case "down", "j":
		m.sel[t] = clamp(m.sel[t]+1, m.rows())
	case "a":
		switch t {
		case tabFirewall:
			m.prompt("Allow (a domain, IP, CIDR or @preset; several separated by spaces):", func(v string) Action {
				return Action{Cmd: "fw", Sub: "allow", Org: m.open, Args: strings.Fields(v)}
			})
		case tabRepos:
			m.prompt("Add a repo (owner/repo, or a git URL):", func(v string) Action {
				return Action{Cmd: "repo", Sub: "add", Org: m.open, Args: strings.Fields(v)[:1]}
			})
		}
	case "x":
		i := clamp(m.sel[t], m.rows())
		switch {
		case t == tabFirewall && m.rows() > 0:
			return m, m.request(Action{Cmd: "fw", Sub: "deny", Org: m.open, Args: []string{m.d.Firewall.Entries[i]}})
		case t == tabRepos && m.rows() > 0:
			return m, m.request(Action{Cmd: "repo", Sub: "rm", Org: m.open, Args: []string{m.d.Repos.Repos[i].Dir}})
		}
	case "y":
		if t == tabRepos {
			return m, m.request(Action{Cmd: "repo", Sub: "sync", Org: m.open})
		}
	case "b":
		if t == tabBackups {
			return m, m.request(Action{Cmd: "backup", Org: m.open})
		}
	}
	return m, nil
}
