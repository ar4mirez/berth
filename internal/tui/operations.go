package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ar4mirez/berth/internal/mcpsrv"
	"github.com/ar4mirez/berth/internal/ops"
)

// Every operation, by name (#167): the tools the API serves and the web UI lists, here as a page
// of the terminal dashboard. What has no key of its own is run from it, so both dashboards do the
// same things: an operation is in both the day it is a tool.

// Operation is one of them: what it is, what it needs, and its arguments.
type Operation struct {
	// Name is the tool's, and Command the command it is ("fw allow").
	Name, Command string
	// About is what it does.
	About string
	// Access is what it needs: "read", "write", "restart" or "admin".
	Access string
	// Note says what a restart does, for the confirmation.
	Note   string
	Fields []mcpsrv.Field
}

// Title is the operation as a person would name it.
func (o Operation) Title() string {
	if o.Command != "" {
		return ops.Respell("berth " + o.Command)[len("berth "):]
	}
	return o.Name
}

// confirms is whether it takes the org's name again: it restarts a container, or removes an org.
func (o Operation) confirms() bool {
	for _, f := range o.Fields {
		if f.Name == "confirm" {
			return true
		}
	}
	return false
}

// form are the fields a person fills: every one but the confirmation.
func (o Operation) form() []mcpsrv.Field {
	var out []mcpsrv.Field
	for _, f := range o.Fields {
		if f.Name != "confirm" {
			out = append(out, f)
		}
	}
	return out
}

// operationsOf are the tools as operations, by name.
func operationsOf(specs []mcpsrv.Spec) []Operation {
	out := make([]Operation, 0, len(specs))
	for _, s := range specs {
		out = append(out, Operation{Name: s.Name, Command: strings.TrimSpace(s.Cmd + " " + s.Sub), About: s.About,
			Access: s.Access(), Note: s.Op.Note, Fields: s.Fields()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title() < out[j].Title() })
	return out
}

func (b *appBackend) specs() []mcpsrv.Spec {
	b.once.Do(func() {
		_, b.tools = mcpsrv.New(mcpsrv.Options{NewApp: b.newApp})
	})
	return b.tools
}

func (b *appBackend) Operations() []Operation { return operationsOf(b.specs()) }

// Invoke runs a tool as the dashboard's user: whoever started berth tui, who may do what berth
// does unless it was started with --read-only. The tool's own gate and audit apply.
func (b *appBackend) Invoke(ctx context.Context, name string, args map[string]any) (string, error) {
	for _, s := range b.specs() {
		if s.Name != name {
			continue
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		may := !b.readOnly
		out, err := s.Invoke(ctx, mcpsrv.Caller{Via: "tui", Writes: may, Restarts: may, Admin: may}, raw)
		if err != nil {
			return "", err
		}
		return resultText(out), nil
	}
	return "", fmt.Errorf("the dashboard has no operation %q", name)
}

// resultText is what a tool answered, for a person: what the command printed, when it printed
// something, and the document otherwise.
func resultText(out any) string {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Sprint(out)
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) == nil {
		// What the command printed is the answer, when it printed something.
		if text, _ := doc["output"].(string); text != "" {
			return text
		}
		// It printed nothing, and answered with nothing else: it is done.
		if _, isText := doc["output"].(string); isText && len(doc) <= 2 {
			if _, hasOrg := doc["org"]; hasOrg || len(doc) == 1 {
				return ""
			}
		}
	}
	return string(b)
}

// --- the page ----------------------------------------------------------------------------------

type opDoneMsg struct {
	label string
	out   string
	err   error
}

// openOperations shows the list, with the cursor where it was.
func (m *Model) openOperations() {
	if m.opsList == nil {
		m.opsList = m.b.Operations()
	}
	m.page, m.op = pageOps, nil
}

// openOperation shows one operation's form. Its org starts as the one the dashboard is on.
func (m *Model) openOperation(o Operation) {
	m.op, m.opCur, m.opValues = &o, 0, map[string]string{}
	for _, f := range o.form() {
		if f.Name == "org" {
			m.opValues["org"] = m.selected()
		}
	}
}

func (m *Model) opsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if m.op == nil {
		switch s {
		case "q":
			return m, tea.Quit
		case "?":
			m.over = help
		case "esc", "left", "h", "backspace", "o":
			m.page = pageMain
		case "up", "k":
			m.opsCur = clamp(m.opsCur-1, len(m.opsList))
		case "down", "j":
			m.opsCur = clamp(m.opsCur+1, len(m.opsList))
		case "pgup":
			m.opsCur = clamp(m.opsCur-10, len(m.opsList))
		case "pgdown":
			m.opsCur = clamp(m.opsCur+10, len(m.opsList))
		case "enter", "right", "l":
			if m.opsCur < len(m.opsList) {
				m.openOperation(m.opsList[m.opsCur])
			}
		}
		return m, nil
	}
	fields := m.op.form()
	switch s {
	case "q":
		return m, tea.Quit
	case "?":
		m.over = help
	case "esc", "left", "h", "backspace":
		m.op = nil
	case "up", "k":
		m.opCur = clamp(m.opCur-1, len(fields))
	case "down", "j":
		m.opCur = clamp(m.opCur+1, len(fields))
	case "enter", " ":
		if m.opCur >= len(fields) {
			break
		}
		f := fields[m.opCur]
		if f.Kind == "bool" {
			if m.opValues[f.Name] == "" {
				m.opValues[f.Name] = "yes"
			} else {
				delete(m.opValues, f.Name)
			}
			break
		}
		m.over, m.ask, m.typed = input, f.Name+": "+f.Help, m.opValues[f.Name]
		m.submitText = func(v string) tea.Cmd {
			if v == "" {
				delete(m.opValues, f.Name)
			} else {
				m.opValues[f.Name] = v
			}
			return nil
		}
	case "r":
		return m, m.requestOperation()
	}
	return m, nil
}

// operationArgs are the form's values as the tool takes them. A required one left empty is an
// error that names it.
func operationArgs(o Operation, values map[string]string) (map[string]any, error) {
	args := map[string]any{}
	for _, f := range o.form() {
		v := strings.TrimSpace(values[f.Name])
		if v == "" {
			if f.Required {
				return nil, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: f.Name + " is required", Hint: f.Help}
			}
			continue
		}
		switch f.Kind {
		case "bool":
			args[f.Name] = true
		case "int":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("%s must be a number, not %q", f.Name, v)}
			}
			args[f.Name] = n
		case "list":
			args[f.Name] = strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
		default:
			args[f.Name] = v
		}
	}
	return args, nil
}

// requestOperation is the gate for an operation run by name, as request is for one with a key: a
// write is refused under --read-only; one that restarts a container runs only after a "y"; and one
// that can't be undone only after the org's name is typed, as berth destroy asks on the command line.
func (m *Model) requestOperation() tea.Cmd {
	o := *m.op
	label := "berth " + o.Title()
	if o.Access != "read" && m.b.ReadOnly() {
		m.show("read-only mode (--read-only): refusing to run "+label, true)
		return nil
	}
	args, err := operationArgs(o, m.opValues)
	if err != nil {
		m.show(ops.AsError(err).Error(), true)
		return nil
	}
	org, _ := args["org"].(string)
	if org != "" {
		label += " " + org
	}
	run := func() tea.Cmd {
		m.over, m.busy = none, label
		return func() tea.Msg {
			out, err := m.b.Invoke(m.ctx, o.Name, args)
			return opDoneMsg{label: label, out: out, err: err}
		}
	}
	switch {
	case !o.confirms():
		return run()
	case o.Access == "admin":
		args["confirm"] = org
		m.over, m.typed = input, ""
		m.ask = fmt.Sprintf("%s\n\n%s\nIt can't be undone.\n\nType %s to run it:", label, o.About, org)
		m.submitText = func(v string) tea.Cmd {
			if v != org {
				m.show("nothing was run: that isn't "+org, true)
				return nil
			}
			return run()
		}
	default:
		args["confirm"] = org
		m.over, m.confirmRun = confirm, run
		m.ask = fmt.Sprintf("%s\n\nThis %s.\nIt stops the work running in %s.", label, o.Note, org)
	}
	return nil
}

// --- what it shows ------------------------------------------------------------------------------

// opsView is the list of operations, or the open one's form.
func (m *Model) opsView() (body, keys string) {
	if m.op == nil {
		return m.opsListView(), "↑↓ select · enter open · esc back · ? help · q quit"
	}
	o := *m.op
	var b strings.Builder
	b.WriteString(headStyle.Render("berth "+o.Title()) + "  " + faintStyle.Render("needs: "+o.Access) + "\n")
	b.WriteString(wrap(o.About, m.w-2) + "\n\n")
	fields := o.form()
	if len(fields) == 0 {
		b.WriteString(faintStyle.Render("  it takes no arguments") + "\n")
	}
	rows := [][]string{{"ARGUMENT", "VALUE", ""}}
	for _, f := range fields {
		v := m.opValues[f.Name]
		switch {
		case f.Kind == "bool":
			v = yes(v != "", "[x]", "[ ]")
		case v == "":
			v = "-"
		}
		rows = append(rows, []string{f.Name, v, yes(f.Required, "required", "")})
	}
	if len(fields) > 0 {
		b.WriteString(table(rows, clamp(m.opCur, len(fields))) + "\n\n")
		b.WriteString(faintStyle.Render(wrap("  "+fields[clamp(m.opCur, len(fields))].Help, m.w-2)))
	}
	return b.String(), "↑↓ select · enter set · r run · esc back · ? help"
}

func (m *Model) opsListView() string {
	rows := [][]string{{"OPERATION", "NEEDS", "WHAT IT DOES"}}
	// The rows that fit, around the cursor: the list is longer than most terminals.
	room := max(m.h-6, 3)
	first := 0
	if m.opsCur >= room {
		first = m.opsCur - room + 1
	}
	last := min(first+room, len(m.opsList))
	for _, o := range m.opsList[first:last] {
		about, _, _ := strings.Cut(o.About, ". ")
		rows = append(rows, []string{o.Title(), o.Access, strings.TrimSuffix(about, ".")})
	}
	return headStyle.Render(fmt.Sprintf("Every operation (%d)", len(m.opsList))) + "\n\n" + table(rows, m.opsCur-first)
}

// wrap breaks text at spaces so no line is wider than width.
func wrap(text string, width int) string {
	width = max(width, 20)
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len([]rune(line))+1+len([]rune(w)) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
