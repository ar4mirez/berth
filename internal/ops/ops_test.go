package ops

import (
	"sort"
	"testing"
)

// leaves walks the catalog down to the operations themselves.
func leaves(prefix string, op Op, visit func(name string, op Op)) {
	if op.Access != BySub {
		visit(prefix, op)
		return
	}
	for sub, o := range op.Subs {
		leaves(prefix+" "+sub, o, visit)
	}
}

// TestReadsReturnDataOrSayWhyNot: every reading operation returns data (`--output json`) or
// states why it doesn't, and nothing that writes claims to return data (#54).
func TestReadsReturnDataOrSayWhyNot(t *testing.T) {
	var names []string
	for name := range Catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	n := 0
	for _, name := range names {
		leaves(name, Catalog[name], func(path string, op Op) {
			n++
			switch {
			case op.Access == Read && op.JSON == (op.NoJSON != ""):
				t.Errorf("%q reads: it must return JSON or say why not (NoJSON), not both or neither", path)
			case op.Access == Write && (op.JSON || op.NoJSON != ""):
				t.Errorf("%q writes: JSON and NoJSON are for reading operations", path)
			case op.Access != Read && op.Access != Write:
				t.Errorf("%q declares no access", path)
			}
		})
	}
	if n < 60 {
		t.Fatalf("only %d operations walked", n)
	}
}

// TestLookup: a nested subcommand is a path.
func TestLookup(t *testing.T) {
	for _, c := range []struct {
		cmd, sub string
		ok, json bool
	}{
		{"ls", "", true, true},
		{"repo", "policy/", true, true},
		{"repo", "policy/<mode>", true, false},
		{"use", "", true, true},
		{"use", "<org>", true, false},
		{"logs", "", true, false},
		{"fw", "nope", false, false},
		{"nope", "", false, false},
	} {
		op, ok := Lookup(c.cmd, c.sub)
		if ok != c.ok || (ok && op.JSON != c.json) {
			t.Errorf("Lookup(%q, %q) = %+v, %v", c.cmd, c.sub, op, ok)
		}
	}
}
