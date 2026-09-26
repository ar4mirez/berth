package cli

import (
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/ops"
)

// TestCatalogCoversEveryCommand keeps internal/ops complete and consistent with the CLI: every
// runnable command has an entry, its access agrees with the --read-only guard's annotation, and
// every operation that may restart a container says when (the TUI, MCP and API show that note).
func TestCatalogCoversEveryCommand(t *testing.T) {
	root := NewRoot()
	seen := map[string]bool{}
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			continue
		}
		seen[c.Name()] = true
		if !c.Runnable() {
			continue // a group (secrets): its subcommands declare access themselves
		}
		op, ok := ops.Catalog[c.Name()]
		if !ok {
			t.Errorf("%s: missing from ops.Catalog (declare whether it writes and whether it restarts)", c.Name())
			continue
		}
		switch ann := c.Annotations[accessKey]; {
		case op.Access == ops.Write && ann != accessWrite:
			t.Errorf("%s: the catalog says it writes, but the CLI wraps it in reads()", c.Name())
		case op.Access == ops.Read && ann != accessRead:
			t.Errorf("%s: the catalog says it only reads, but the CLI wraps it in writes()", c.Name())
		case op.Access == ops.BySub && ann != accessRead:
			// The command checks its writing subcommands itself (--read-only and ownership).
			t.Errorf("%s: a command whose access depends on its subcommand must be reads() at the top", c.Name())
		}
	}
	for name := range ops.Catalog {
		if !seen[name] {
			t.Errorf("ops.Catalog has %q, which isn't a command", name)
		}
	}
	var check func(path string, op ops.Op)
	check = func(path string, op ops.Op) {
		if op.Restart != ops.Never && op.Note == "" {
			t.Errorf("%s: may restart a container but has no Note saying when", path)
		}
		if op.Restart != ops.Never && op.Access != ops.Write {
			t.Errorf("%s: restarts a container but isn't a write", path)
		}
		for sub, s := range op.Subs {
			check(path+" "+sub, s)
		}
	}
	for name, op := range ops.Catalog {
		check(name, op)
	}
}

// TestOutputJSONOnlyWhereSupported: --output json works for the operations that return data and is
// refused, before anything runs, for the rest; other formats are refused.
func TestOutputJSONOnlyWhereSupported(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"ls"}, true},
		{[]string{"info", "acme"}, false},
		{[]string{"fw", "acme", "allow", "x.example"}, false},
		{[]string{"repo", "policy", "acme"}, false},
		{[]string{"schedule", "status"}, false},
	} {
		_, errOut, code := run(t, append([]string{"--home", home, "--output", "json"}, tc.args...)...)
		refused := code != 0 && contains(errOut, "--output json isn't available")
		if tc.ok == refused {
			t.Errorf("%v: code %d, stderr %q (want supported=%v)", tc.args, code, errOut, tc.ok)
		}
	}
	if _, errOut, code := run(t, "--home", home, "--output", "yaml", "ls"); code != 1 || !contains(errOut, "--output must be text or json") {
		t.Errorf("--output yaml: code %d, stderr %q", code, errOut)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
