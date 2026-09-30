package parity

import (
	"strings"
	"testing"
)

// destroyFixture: twoOrgs, with backups of globex (2 archives and a copy restore --force set aside),
// of acme, and of an org whose name starts like globex's; and globex as the default org.
var destroyFixture = merge(twoOrgs, map[string]File{
	"state/backups": {Dir: true},
	"state/backups/globex-20260101-000000.tar.zst.age":       {Content: "x"},
	"state/backups/globex-20260102-000000.tar.zst":           {Content: "x"},
	"state/backups/globex-dev-20260101-000000.tar.zst.age":   {Content: "x"},
	"state/backups/acme-20260101-000000.tar.zst.age":         {Content: "x"},
	"state/backups/.replaced":                                {Dir: true},
	"state/backups/.replaced/globex-20260103-040506":         {Dir: true},
	"state/backups/.replaced/globex-20260103-040506/org.env": {Content: "x"},
	"home/.config/berth":                                     {Dir: true},
	"home/.config/berth/context":                             {Content: "globex\n"},
})

func treeHas(tree []string, prefix string) bool {
	for _, l := range tree {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// TestBerthDestroy: destroy (berth-only, PARITY.md) takes the container down, removes the org's
// directory and its backups, forgets it as the default org, and records it as destroyed (TestDestroyedRecord), which
// ls reports. It removes nothing unconfirmed, and --keep-backups keeps the backups.
func TestBerthDestroy(t *testing.T) {
	r := run(t, Berth(berthBin), Scenario{Args: []string{"destroy", "globex", "--yes"}, Files: destroyFixture})
	if r.Exit != 0 {
		t.Fatalf("destroy: exit %d\n%s%s", r.Exit, r.Stdout, r.Stderr)
	}
	for _, want := range []string{"This permanently removes globex:", "  - backup <RUN>/state/backups/.replaced/globex-20260103-040506\n", "Destroyed globex, and 3 backup(s).\n"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if !strings.Contains(strings.Join(r.Calls, "\n"), `"--env-file" "<RUN>/state/orgs/globex/org.env" "down" "--volumes" "--remove-orphans"`) {
		t.Errorf("no compose down --volumes:\n%s", strings.Join(r.Calls, "\n"))
	}
	for _, gone := range []string{"state/orgs/globex", "state/backups/globex-2026", "state/backups/.replaced/globex-", "home/.config/berth/context"} {
		if treeHas(r.Tree, gone) {
			t.Errorf("%s is still there:\n%s", gone, strings.Join(r.Tree, "\n"))
		}
	}
	for _, kept := range []string{"state/orgs/acme/org.env", "state/backups/acme-20260101-000000.tar.zst.age", "state/backups/globex-dev-20260101-000000.tar.zst.age"} {
		if !treeHas(r.Tree, kept) {
			t.Errorf("%s was removed", kept)
		}
	}
	// ls reports it, and an org that exists again isn't reported.
	destroyed := withFile(withoutFile(twoOrgs, "state/orgs/globex/org.env"), "state/berth/destroyed", File{Content: "globex 2026-09-30T20:00:00Z\nacme 2026-01-01T00:00:00Z\n"})
	r = run(t, Berth(berthBin), Scenario{Args: []string{"--output", "json", "ls"}, Files: destroyed})
	if !strings.Contains(r.Stdout, `"destroyed": [
    {
      "name": "globex",
      "host": "local",
      "at": "2026-09-30T20:00:00Z"
    }
  ]`) {
		t.Errorf("ls:\n%s%s", r.Stdout, r.Stderr)
	}

	// Unconfirmed (no terminal, no --yes): nothing changes.
	r = run(t, Berth(berthBin), Scenario{Args: []string{"destroy", "globex"}, Files: destroyFixture})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "nothing was removed: confirm in a terminal, or pass --yes") || !treeHas(r.Tree, "state/orgs/globex/org.env") {
		t.Errorf("unconfirmed: exit %d\n%s", r.Exit, r.Stderr)
	}
	if strings.Contains(strings.Join(r.Calls, "\n"), `"down"`) {
		t.Errorf("unconfirmed destroy ran compose down")
	}

	// --keep-backups.
	r = run(t, Berth(berthBin), Scenario{Args: []string{"destroy", "--keep-backups", "globex", "-y"}, Files: destroyFixture})
	if r.Exit != 0 || treeHas(r.Tree, "state/orgs/globex") || !treeHas(r.Tree, "state/backups/globex-20260101-000000.tar.zst.age") ||
		!strings.Contains(r.Stdout, "Destroyed globex.\n") {
		t.Errorf("--keep-backups: exit %d\n%s%s", r.Exit, r.Stdout, r.Stderr)
	}

	// Refusals: ccenv's orgs, unknown orgs, and --read-only.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"destroy", "nope", "--yes"}, "unknown org 'nope'"},
		{[]string{"destroy", "--yes"}, "missing <org>"},
		{[]string{"destroy", "globex", "acme", "--yes"}, "usage: <tool> destroy <org> [--yes] [--keep-backups]"},
		{[]string{"--read-only", "destroy", "globex", "--yes"}, "read-only"},
	} {
		r = run(t, Berth(berthBin), Scenario{Args: c.args, Files: destroyFixture})
		if r.Exit == 0 || !strings.Contains(r.Stderr, c.want) || !treeHas(r.Tree, "state/orgs/globex/org.env") {
			t.Errorf("%v: exit %d, stderr %q", c.args, r.Exit, r.Stderr)
		}
	}
	r = run(t, BerthUnowned(berthBin), Scenario{Args: []string{"destroy", "globex", "--yes"}, Files: destroyFixture})
	if r.Exit == 0 || !strings.Contains(r.Stderr, "managed by ccenv") {
		t.Errorf("ccenv's org: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}
