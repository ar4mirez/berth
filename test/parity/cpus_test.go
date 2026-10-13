package parity

import (
	"strings"
	"testing"
)

// An org asks for 4 CPUs unless its org.env says otherwise, and Docker refuses a limit above what
// it has ("range of CPUs is from 0.01 to 2.00"): a small VM (Colima's default is 2) then couldn't
// start an org at all. berth asks the engine first, starts the org with what there is, and says so.
func TestBerthUpCapsCPUs(t *testing.T) {
	owned := withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: "MANAGER=berth\n" + acmeEnv})
	has := func(n string) Rule {
		return Rule{Bin: "docker", Match: `^info --format \{\{\.NCPU\}\}$`, Stdout: n + "\n"}
	}
	composeUp := func(r Result) string {
		for _, c := range r.Calls {
			if strings.HasPrefix(c, `docker "compose"`) && strings.Contains(c, `"up" "-d"`) {
				return c
			}
		}
		return ""
	}
	raw := BerthUnowned(berthBin)
	for _, tc := range []struct {
		name, cpus string // CPUS added to org.env ("" for its own, 4)
		rules      []Rule
		want       string // the CPUS compose gets, "" for the org.env's own
	}{
		{"fewer than the default", "", []Rule{has("2")}, "2"},
		{"fewer than asked for", "CPUS=8\n", []Rule{has("4")}, "4"},
		{"as many as asked for", "CPUS=4\n", []Rule{has("4")}, ""},
		{"more than asked for", "", []Rule{has("16")}, ""},
		{"a fraction within what there is", "CPUS=0.5\n", []Rule{has("2")}, ""},
		{"the engine doesn't say", "", nil, ""},
		{"the engine can't be asked", "", []Rule{{Bin: "docker", Match: `^info `, Exit: 1}}, ""},
	} {
		files := withFile(owned, "state/orgs/acme/org.env", File{Content: "MANAGER=berth\n" + acmeEnv + tc.cpus})
		for _, cmd := range []string{"up", "restart"} {
			r := run(t, raw, Scenario{Args: []string{cmd, "acme"}, Files: files, Rules: running("acme", tc.rules...)})
			call := composeUp(r)
			got := ""
			if _, after, ok := strings.Cut(call, `[CPUS="`); ok {
				got, _, _ = strings.Cut(after, `"`)
			}
			said := strings.Contains(r.Stderr, "this Docker has "+tc.want+", so it starts with "+tc.want)
			if r.Exit != 0 || call == "" || got != tc.want || said != (tc.want != "") {
				t.Errorf("%s, %s: exit %d, CPUS %q (want %q), stderr %q\n%s", tc.name, cmd, r.Exit, got, tc.want, r.Stderr, call)
			}
		}
	}

	// Only starting an org asks: nothing else has a limit to set.
	r := run(t, raw, Scenario{Args: []string{"down", "acme"}, Files: owned, Rules: running("acme", has("2"))})
	if r.Exit != 0 || hasCall(r, `"info"`) || strings.Contains(r.Stderr, "CPUs") {
		t.Errorf("down: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}
}
