package tui

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/ar4mirez/berth/internal/api"
	"github.com/ar4mirez/berth/internal/mcpsrv"
	"github.com/ar4mirez/berth/internal/ops"
)

// The fake's operations are the real ones: the tools, as the dashboard lists them.
func (f *fake) Operations() []Operation {
	_, specs := mcpsrv.New(mcpsrv.Options{})
	return operationsOf(specs)
}

func (f *fake) Invoke(_ context.Context, name string, args map[string]any) (string, error) {
	b, _ := json.Marshal(args)
	f.invoked = append(f.invoked, name+" "+string(b))
	return "ran " + name, f.fail
}

// open shows the form of the operation named, from wherever the dashboard is.
func open(t *testing.T, m *Model, name string) {
	t.Helper()
	if m.page != pageOps {
		press(m, "o")
	}
	m.op = nil
	i := slices.IndexFunc(m.opsList, func(o Operation) bool { return o.Name == name })
	if i < 0 {
		t.Fatalf("the dashboard lists no operation %s", name)
	}
	m.opsCur = i
	press(m, "enter")
}

// set types a value into the argument named.
func set(t *testing.T, m *Model, field, value string) {
	t.Helper()
	i := slices.IndexFunc(m.op.form(), func(f mcpsrv.Field) bool { return f.Name == field })
	if i < 0 {
		t.Fatalf("%s has no argument %s", m.op.Name, field)
	}
	m.opCur = i
	press(m, "enter")
	m.typed = "" // the argument's own value is offered to edit: start from nothing
	press(m, strings.Split(value, "")...)
	press(m, "enter")
}

// TestBothDashboardsDoTheSame (#167): the terminal dashboard lists every operation the API serves,
// which is what the web UI lists, each with what it does and what it needs; and every one that
// restarts a container or removes an org takes the confirmation.
func TestBothDashboardsDoTheSame(t *testing.T) {
	listed := map[string]Operation{}
	for _, o := range NewBackend(nil, false, "", nil).Operations() {
		listed[o.Name] = o
	}
	specs := api.Specs()
	if len(listed) != len(specs) || len(specs) < 40 {
		t.Fatalf("the dashboard lists %d operations, the API serves %d", len(listed), len(specs))
	}
	routed := map[string]bool{}
	for _, r := range api.Routes {
		routed[r.Tool] = true
	}
	for name, s := range specs {
		o, ok := listed[name]
		switch {
		case !ok:
			t.Errorf("%s is in the API, and not in the dashboard", name)
		case !routed[name]:
			t.Errorf("%s is in the dashboard, and the API has no route for it: the web UI wouldn't list it", name)
		case o.About == "" || o.Title() == "":
			t.Errorf("%s: no title or description: %+v", name, o)
		case o.Access != s.Access():
			t.Errorf("%s needs %q in the dashboard, %q in the API", name, o.Access, s.Access())
		case s.Op.Restart != ops.Never && !o.confirms():
			t.Errorf("%s can stop a container (it needs %q), and takes no confirmation", name, o.Access)
		}
	}
}

// TestOperationViews: the list and a form, and neither wider or longer than the terminal.
func TestOperationViews(t *testing.T) {
	m := dash(t, &fake{}, 100, 24)
	press(m, "o")
	golden(t, "operations", m.View())
	open(t, m, "repo_add")
	set(t, m, "repo", "acme/widgets")
	press(m, "down", "down", "down", "enter") // no_clone, a switch
	golden(t, "operation-form", m.View())

	m = dash(t, &fake{}, 50, 14)
	press(m, "o")
	for i := 0; i < len(m.opsList); i++ {
		for _, l := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(l) > 50 {
				t.Fatalf("a line %d wide in a terminal of 50: %q", lipgloss.Width(l), l)
			}
		}
		if n := strings.Count(m.View(), "\n") + 1; n != 14 {
			t.Fatalf("%d lines in a terminal of 14", n)
		}
		if !strings.Contains(m.View(), "▸ "+m.opsList[i].Title()) {
			t.Fatalf("the cursor is on %s, which isn't on screen:\n%s", m.opsList[i].Title(), m.View())
		}
		press(m, "down")
	}
	press(m, "esc")
	if m.page != pageMain {
		t.Error("esc didn't leave the list of operations")
	}
}

// TestOperationsRun: a form's values go to the tool as its types; a required one left empty runs
// nothing; the org starts as the one the dashboard is on.
func TestOperationsRun(t *testing.T) {
	f := &fake{}
	m := dash(t, f, 100, 24)
	open(t, m, "firewall_allow")
	if m.opValues["org"] != "acme" {
		t.Errorf("the org starts as %q, not the selected one", m.opValues["org"])
	}
	press(m, "r")
	if len(f.invoked) != 0 || !strings.Contains(m.View(), "entries is required") {
		t.Errorf("without its required argument: ran %v\n%s", f.invoked, m.View())
	}
	press(m, "enter") // close what it said
	set(t, m, "entries", "pypi.org, @go")
	press(m, "r")
	if want := []string{`firewall_allow {"entries":["pypi.org","@go"],"org":"acme"}`}; !slices.Equal(f.invoked, want) {
		t.Errorf("ran %v, want %v", f.invoked, want)
	}
	if !strings.Contains(m.View(), "ran firewall_allow") {
		t.Errorf("what it answered isn't shown:\n%s", m.View())
	}

	// A number, a switch, and an argument left out.
	f = &fake{}
	m = dash(t, f, 100, 24)
	open(t, m, "org_logs")
	set(t, m, "lines", "many")
	press(m, "r")
	if len(f.invoked) != 0 || !strings.Contains(m.View(), "lines must be a number") {
		t.Errorf("a number that isn't one: ran %v\n%s", f.invoked, m.View())
	}
	press(m, "enter")
	set(t, m, "lines", "5")
	press(m, "r", "enter")
	open(t, m, "backup_create")
	set(t, m, "orgs", "acme globex")
	press(m, "r", "enter")
	if want := []string{`org_logs {"lines":5,"org":"acme"}`, `backup_create {"orgs":["acme","globex"]}`}; !slices.Equal(f.invoked, want) {
		t.Errorf("ran %v, want %v", f.invoked, want)
	}
}

// TestOperationsConfirm: by name too, what restarts a container runs only after a "y", with the
// org's name sent as the confirmation; what can't be undone only after the name is typed; and
// --read-only runs no write.
func TestOperationsConfirm(t *testing.T) {
	for _, answer := range []string{"enter", "n", "esc", " ", "q", "r"} {
		f := &fake{}
		m := dash(t, f, 100, 24)
		open(t, m, "org_restart")
		press(m, "r")
		if v := m.View(); !strings.Contains(v, "berth restart acme") || !strings.Contains(v, "It stops the work running in acme.") {
			t.Fatalf("the confirmation:\n%s", v)
		}
		press(m, answer)
		if len(f.invoked) != 0 {
			t.Errorf("%q ran it: %v", answer, f.invoked)
		}
	}
	f := &fake{}
	m := dash(t, f, 100, 24)
	open(t, m, "org_restart")
	press(m, "r", "y")
	if want := []string{`org_restart {"confirm":"acme","org":"acme"}`}; !slices.Equal(f.invoked, want) {
		t.Errorf("confirmed: ran %v, want %v", f.invoked, want)
	}

	// Destroying an org: its name, typed.
	for _, typed := range []string{"", "y", "acm", "globex"} {
		f := &fake{}
		m := dash(t, f, 100, 24)
		open(t, m, "org_destroy")
		press(m, "r")
		if v := m.View(); !strings.Contains(v, "It can't be undone.") || !strings.Contains(v, "Type acme to run it:") {
			t.Fatalf("the confirmation:\n%s", v)
		}
		press(m, strings.Split(typed, "")...)
		press(m, "enter")
		if len(f.invoked) != 0 {
			t.Errorf("typing %q destroyed it: %v", typed, f.invoked)
		}
	}
	f = &fake{}
	m = dash(t, f, 100, 24)
	open(t, m, "org_destroy")
	press(m, "r", "a", "c", "m", "e", "enter")
	if want := []string{`org_destroy {"confirm":"acme","org":"acme"}`}; !slices.Equal(f.invoked, want) {
		t.Errorf("its name typed: ran %v, want %v", f.invoked, want)
	}

	// --read-only: every operation that isn't a read is refused, and nothing is asked or run.
	f = &fake{readOnly: true}
	m = dash(t, f, 100, 24)
	press(m, "o")
	for _, o := range m.opsList {
		open(t, m, o.Name)
		for _, fl := range o.form() {
			if fl.Required && m.opValues[fl.Name] == "" {
				m.opValues[fl.Name] = "x"
			}
		}
		press(m, "r")
		refused := strings.Contains(m.View(), "read-only mode (--read-only): refusing to run")
		if (o.Access != "read") != refused {
			t.Errorf("%s (%s), read-only: refused %v\n%s", o.Name, o.Access, refused, m.View())
		}
		press(m, "enter")
	}
	for _, ran := range f.invoked {
		name, _, _ := strings.Cut(ran, " ")
		if i := slices.IndexFunc(m.opsList, func(o Operation) bool { return o.Name == name }); m.opsList[i].Access != "read" {
			t.Errorf("read-only ran %s", ran)
		}
	}
	if len(f.invoked) == 0 {
		t.Error("read-only ran no read either")
	}
}

// TestKeysAreTools: each action with a key is one of the tools, with its arguments: there is one
// way an operation runs, whichever dashboard asks.
func TestKeysAreTools(t *testing.T) {
	specs := api.Specs()
	for _, c := range []struct {
		act  Action
		tool string
		args string
	}{
		{Action{Cmd: "up", Org: "acme"}, "org_up", `{"confirm":"acme","org":"acme"}`},
		{Action{Cmd: "down", Org: "acme@box1"}, "org_down", `{"confirm":"acme@box1","org":"acme@box1"}`},
		{Action{Cmd: "restart", Org: "acme"}, "org_restart", `{"confirm":"acme","org":"acme"}`},
		{Action{Cmd: "fw", Sub: "allow", Org: "acme", Args: []string{"pypi.org", "@go"}}, "firewall_allow", `{"entries":["pypi.org","@go"],"org":"acme"}`},
		{Action{Cmd: "fw", Sub: "deny", Org: "acme", Args: []string{"pypi.org"}}, "firewall_deny", `{"entries":["pypi.org"],"org":"acme"}`},
		{Action{Cmd: "repo", Sub: "add", Org: "acme", Args: []string{"acme/widgets"}}, "repo_add", `{"org":"acme","repo":"acme/widgets"}`},
		{Action{Cmd: "repo", Sub: "rm", Org: "acme", Args: []string{"app"}}, "repo_remove", `{"dir":"app","org":"acme"}`},
		{Action{Cmd: "repo", Sub: "sync", Org: "acme"}, "repo_sync", `{"org":"acme"}`},
		{Action{Cmd: "backup", Org: "acme"}, "backup_create", `{"orgs":["acme"]}`},
	} {
		name, args, err := toolOf(c.act)
		b, _ := json.Marshal(args)
		if err != nil || name != c.tool || string(b) != c.args {
			t.Errorf("%s: %s %s (%v), want %s %s", c.act.Label(), name, b, err, c.tool, c.args)
			continue
		}
		s, ok := specs[name]
		if !ok || s.Cmd != c.act.Cmd || s.Sub != c.act.Sub {
			t.Errorf("%s is the tool %s, whose operation is %q %q", c.act.Label(), name, s.Cmd, s.Sub)
		}
	}
	if _, _, err := toolOf(Action{Cmd: "destroy", Org: "acme"}); err == nil {
		t.Error("an action with no key was given a tool")
	}
}

// TestResultText: what a tool printed is what the dashboard shows; a document otherwise.
func TestResultText(t *testing.T) {
	for want, out := range map[string]any{
		"Cloned widget":               mcpsrv.Changed{Org: "acme", Output: "Cloned widget"},
		"allowed: pypi.org":           mcpsrv.FirewallChanged{Output: "allowed: pypi.org"},
		"":                            mcpsrv.Changed{Org: "acme"},
		"\"lines\": [\n    \"ready\"": map[string]any{"org": "acme", "lines": []string{"ready"}},
	} {
		if got := resultText(out); (want == "" && got != "") || !strings.Contains(got, want) {
			t.Errorf("%+v: %q, want %q", out, got, want)
		}
	}
	if got := resultText(mcpsrv.FirewallChanged{}); !strings.Contains(got, `"firewall"`) {
		t.Errorf("a change that printed nothing shows what it answered: %q", got)
	}
}

// TestOperationsConfirmWithoutAnOrg: an operation that is confirmed and takes no org says its
// first required argument again: the backup to restore.
func TestOperationsConfirmWithoutAnOrg(t *testing.T) {
	const file = "acme-20261006-023000.tar.zst.age"
	f := &fake{}
	m := dash(t, f, 100, 24)
	open(t, m, "backup_restore")
	set(t, m, "file", file)
	press(m, "r")
	if v := m.View(); !strings.Contains(v, "berth backup restore "+file) || !strings.Contains(v, "It can stop an org, or replace one.") {
		t.Fatalf("the confirmation:\n%s", v)
	}
	press(m, "n")
	if len(f.invoked) != 0 {
		t.Errorf("unconfirmed, it ran: %v", f.invoked)
	}
	press(m, "r", "y")
	if want := []string{`backup_restore {"confirm":"` + file + `","file":"` + file + `"}`}; !slices.Equal(f.invoked, want) {
		t.Errorf("confirmed: ran %v, want %v", f.invoked, want)
	}
}

// TestSecretsAreNotShown: an argument that is a secret is typed unseen, and the form says only
// that it is set; it still reaches the tool as typed.
func TestSecretsAreNotShown(t *testing.T) {
	const secret = "sk-or-PARITY-SECRET"
	f := &fake{}
	m := dash(t, f, 100, 24)
	open(t, m, "env_set")
	i := slices.IndexFunc(m.op.form(), func(fl mcpsrv.Field) bool { return fl.Name == "value" })
	if i < 0 || !m.op.form()[i].Secret {
		t.Fatalf("env_set's value isn't a secret argument: %+v", m.op.form())
	}
	set(t, m, "name", "OPENROUTER_API_KEY")
	m.opCur = i
	press(m, "enter")
	press(m, strings.Split(secret, "")...)
	if v := m.View(); strings.Contains(v, secret) || !strings.Contains(v, strings.Repeat("•", len(secret))) {
		t.Errorf("the secret shows while it is typed:\n%s", v)
	}
	press(m, "enter")
	if v := m.View(); strings.Contains(v, secret) || !strings.Contains(v, "(set, not shown)") {
		t.Errorf("the form shows the secret:\n%s", v)
	}
	// Opened again, it starts empty: what was typed isn't offered back.
	press(m, "enter")
	if m.typed != "" {
		t.Errorf("the secret is offered to edit: %q", m.typed)
	}
	press(m, "esc")
	press(m, "r")
	if v := m.View(); strings.Contains(v, secret) {
		t.Errorf("the confirmation shows the secret:\n%s", v)
	}
	press(m, "a", "c", "m", "e", "enter")
	want := `env_set {"confirm":"acme","name":"OPENROUTER_API_KEY","org":"acme","value":"` + secret + `"}`
	if !slices.Equal(f.invoked, []string{want}) {
		t.Errorf("ran %v, want %v", f.invoked, want)
	}
	if v := m.View(); strings.Contains(v, secret) {
		t.Errorf("the result shows the secret:\n%s", v)
	}
}
