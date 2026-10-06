package ops

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
)

type exits int

func (e exits) Error() string { return "exit" }
func (e exits) ExitCode() int { return int(e) }

// TestAsError: every failure becomes a typed one, with the text berth printed before unchanged.
func TestAsError(t *testing.T) {
	typed := fail(KindNotFound, "run: berth init acme", "unknown org '%s'", "acme")
	for _, c := range []struct {
		name  string
		err   error
		kind  Kind
		code  int
		quiet bool
		text  string
	}{
		{"typed", typed, KindNotFound, 1, false, "unknown org 'acme' (run: berth init acme)"},
		{"typed, wrapped", fmt.Errorf("host box1: %w", typed), KindNotFound, 1, false, "unknown org 'acme' (run: berth init acme)"},
		{"no hint", fail(KindUsage, "", "missing <org>"), KindUsage, 1, false, "missing <org>"},
		{"ccenv's silent exit", &Exit{Code: 2}, KindFailed, 2, true, "the command ended with exit status 2 and no message"},
		{"a command's own exit", exits(7), KindCommand, 7, true, "exit"},
		{"read-only", config.State{ReadOnly: true}.Writable("run up"), KindRefused, 1, false, ""},
		{"usage", errors.New("usage: berth fw <org> [show]"), KindUsage, 1, false, "usage: berth fw <org> [show]"},
		{"anything else", errors.New("boom"), KindFailed, 1, false, "boom"},
	} {
		e := AsError(c.err)
		if e.Kind != c.kind || e.Code != c.code || e.Quiet != c.quiet || (c.text != "" && e.Error() != c.text) {
			t.Errorf("%s: %+v (%q)", c.name, e, e.Error())
		}
		if d := e.Doc(); d.Schema != "berth.error/v1" || d.Kind != c.kind || d.Code != c.code || d.Message != e.Msg || d.Hint != e.Hint {
			t.Errorf("%s: doc %+v", c.name, d)
		}
	}
	if AsError(nil) != nil {
		t.Error("AsError(nil) isn't nil")
	}
	q := quiet(KindState, 2, "berth up acme", "acme has never started")
	if !q.Silent() || q.ExitCode() != 2 || q.Error() != "acme has never started (berth up acme)" {
		t.Errorf("quiet: %+v", q)
	}
}
