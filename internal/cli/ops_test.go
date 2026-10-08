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
	for _, ccenv := range []bool{false, true} {
		root := newRoot(ccenv)
		live, gone := commands(root)
		seen := map[string]bool{}
		for _, p := range gone {
			seen[p[0]] = true // its operation is reached under another name
		}
		for _, p := range live {
			c, _, _ := root.Find(p)
			name, sub, _ := opOf(c, nil)
			seen[name] = true
			if len(p) == 1 {
				seen[p[0]] = true // a group
			}
			if !c.Runnable() {
				continue // a group: its subcommands declare access themselves
			}
			op, ok := ops.Catalog[name]
			if _, fixed := c.Annotations[subKey]; fixed && ok {
				op, ok = ops.Lookup(name, sub)
			} else if c.Annotations[opKey] == "" && len(p) == 2 {
				op, ok = ops.Lookup(p[0], p[1]) // a group's own verb: host add
			}
			if !ok {
				t.Errorf("%s: no operation %s %s in ops.Catalog (declare whether it writes and whether it restarts)", c.CommandPath(), name, sub)
				continue
			}
			switch ann := c.Annotations[accessKey]; {
			case op.Access == ops.Write && ann != accessWrite:
				t.Errorf("%s: the catalog says it writes, but the CLI wraps it in reads()", c.CommandPath())
			case op.Access == ops.Read && ann != accessRead:
				t.Errorf("%s: the catalog says it only reads, but the CLI wraps it in writes()", c.CommandPath())
			case op.Access == ops.BySub && ann != accessRead:
				// The command checks its writing subcommands itself (--read-only and ownership).
				t.Errorf("%s: a command whose access depends on its subcommand must be reads() at the top", c.CommandPath())
			}
		}
		for name := range ops.Catalog {
			if !seen[name] {
				t.Errorf("ops.Catalog has %q, which isn't a command (ccenv's spellings: %v)", name, ccenv)
			}
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
		{[]string{"info", "acme"}, true},
		{[]string{"org", "use", "acme"}, false},
		{[]string{"system", "completion"}, false},
		{[]string{"fw", "allow", "acme", "x.example"}, false},
		{[]string{"fw", "show", "acme"}, true},
		{[]string{"fw", "acme", "allow", "x.example"}, false},
		{[]string{"repo", "policy", "acme"}, true},
		{[]string{"repo", "policy", "acme", "warn"}, false},
		{[]string{"backup", "schedule", "status"}, true},
		{[]string{"backup", "schedule", "off"}, false},
		{[]string{"org", "password", "show", "acme"}, false},
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

// TestHostAPIOpsAreServed: every operation berth sends to a registered host's own berth (#148) is
// one the API serves, and none is one that depends on the machine berth runs on.
func TestHostAPIOpsAreServed(t *testing.T) {
	for key := range hostAPIOps {
		name, sub, _ := strings.Cut(key, " ")
		if !daemonCan(name, sub) {
			t.Errorf("%q goes to a host's berth, which has no endpoint for it", key)
		}
		switch name {
		case "up", "down", "restart", "info", "ls", "backup", "restore", "init", "destroy", "migrate":
			t.Errorf("%q depends on this machine (the lease, the registry, the backup key): it must keep the ssh path", key)
		}
	}
}
