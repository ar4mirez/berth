package parity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	repoRoot string
	env      *Env
	berthBin string
)

// The harness needs bash and the usual userland (jq, awk, ...) that legacy ccenv uses. Locally a
// missing tool skips the package; in CI it fails.
var required = []string{"bash", "jq", "awk", "sed", "grep", "mktemp", "readlink", "go"}

func TestMain(m *testing.M) {
	os.Exit(setup(m))
}

func setup(m *testing.M) int {
	var err error
	if repoRoot, err = filepath.Abs("../.."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, bin := range required {
		if _, err := exec.LookPath(bin); err != nil {
			if os.Getenv("CI") != "" {
				fmt.Fprintf(os.Stderr, "parity: %s is required in CI\n", bin)
				return 1
			}
			fmt.Fprintf(os.Stderr, "parity: no %s here, skipping\n", bin)
			return 0
		}
	}
	dir, err := os.MkdirTemp("", "berth-parity-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if env, err = Setup(dir, repoRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	berthBin = filepath.Join(dir, "berth")
	build := exec.Command("go", "build", "-o", berthBin, "./cmd/berth")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building berth: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

func run(t *testing.T, tool Tool, s Scenario) Result {
	t.Helper()
	r, err := env.Run(context.Background(), t.TempDir(), tool, s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestLegacyDeterministic runs every scenario through legacy ccenv twice, in separate run dirs, and
// requires identical results. That proves the fakes and normalization leave only real behaviour to
// compare, and checks each scenario really exercises its command.
func TestLegacyDeterministic(t *testing.T) {
	legacy := Legacy(repoRoot)
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			a, b := run(t, legacy, sc.Scenario), run(t, legacy, sc.Scenario)
			if d := Diff("first", a, "second", b); d != "" {
				t.Fatalf("legacy is not deterministic under the harness:\n%s", d)
			}
			if a.Exit != sc.WantExit {
				t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", a.Exit, sc.WantExit, a.Stdout, a.Stderr)
			}
			all := a.Stdout + a.Stderr
			for _, w := range sc.WantOut {
				if !strings.Contains(all, w) {
					t.Errorf("output lacks %q:\n%s", w, all)
				}
			}
		})
	}
}

// TestSchedulingSideEffects spot-checks that the harness records what matters for the scenarios
// whose effects are outside stdout: unit files, cron input, compose env.
func TestSchedulingSideEffects(t *testing.T) {
	legacy := Legacy(repoRoot)
	byName := map[string]checked{}
	for _, s := range scenarios {
		byName[s.Name] = s
	}
	sys := run(t, legacy, byName["schedule via systemd"].Scenario)
	wantTree(t, sys, `home/.config/systemd/user/ccenv-backup.timer 0644 "[Unit]\nDescription=ccenv: nightly backup at 02:30`)
	wantTree(t, sys, `ExecStart=<SELF> backup --all --keep 7`)
	wantCall(t, sys, `systemctl "--user" "enable" "--now" "ccenv-backup.timer"`)

	cron := run(t, legacy, byName["schedule via cron"].Scenario)
	wantCall(t, cron, `crontab "-"  <stdin "MAILTO=\"\"\n0 * * * * /usr/bin/true\n0 3 * * * <SELF> backup --all --keep 14 >> <RUN>/state/backups/cron.log 2>&1  # ccenv-backup\n">`)

	// The bug above, as the harness sees it: an empty table goes to crontab.
	noTab := run(t, legacy, byName["schedule via cron, no crontab yet"].Scenario)
	wantCall(t, noTab, `crontab "-"  <stdin "">`)

	down := run(t, legacy, byName["down"].Scenario)
	wantCall(t, down, `docker "compose" "-f" "<ROOT>/compose.yml" "--env-file" "<RUN>/state/orgs/acme/org.env" "down"  [BIND_ADDR="100.64.0.7"]`)
	wantCall(t, down, `[ORG="acme"]  [ORG_DIR="<RUN>/state/orgs/acme"]`)

	initRes := run(t, legacy, byName["init"].Scenario)
	wantTree(t, initRes, `state/orgs/t-new/org.env 0600`)
	wantTree(t, initRes, `SSH_PORT=2203\nTTYD_PORT=7703`)
	wantTree(t, initRes, `state/orgs/t-new/config/secrets/ttyd_credential 0600 <random, matches`)
	wantCall(t, initRes, `ssh-keygen "-q" "-t" "ed25519" "-N" "" "-C" "claude-t-new@parity-host" "-f" "<RUN>/state/orgs/t-new/ssh/id_ed25519"`)
}

func wantTree(t *testing.T, r Result, sub string) {
	t.Helper()
	for _, l := range r.Tree {
		if strings.Contains(l, sub) {
			return
		}
	}
	t.Errorf("no file tree entry contains %q:\n%s", sub, strings.Join(r.Tree, "\n"))
}

func wantCall(t *testing.T, r Result, sub string) {
	t.Helper()
	for _, l := range r.Calls {
		if strings.Contains(l, sub) {
			return
		}
	}
	t.Errorf("no tool call contains %q:\n%s", sub, strings.Join(r.Calls, "\n"))
}

// TestHarnessSeesDifferences feeds the diff things that must not compare equal.
func TestHarnessSeesDifferences(t *testing.T) {
	legacy := Legacy(repoRoot)

	// berth vs legacy today: same exit codes for no-args and unknown commands, different help text.
	for _, args := range [][]string{nil, {"frobnicate"}} {
		l, b := run(t, legacy, Scenario{Args: args}), run(t, Berth(berthBin), Scenario{Args: args})
		d := Diff("ccenv", l, "berth", b)
		if !strings.Contains(d, "--- stdout") || strings.Contains(d, "--- exit code") {
			t.Errorf("%v: want a stdout difference and matching exit codes, got:\n%s", args, d)
		}
	}

	// A legacy that also touches a file and calls docker once more: tree and calls must differ.
	mutant := legacy
	mutant.Name = "mutant"
	mutant.Command = func(run string, args []string) []string {
		script := `"$0" "$@"; rc=$?; echo EXTRA=1 >> "` + filepath.Join(run, "state/orgs/acme/org.env") + `"; docker ps -q; exit $rc`
		return append([]string{"bash", "-c", script, filepath.Join(repoRoot, "legacy", "ccenv")}, args...)
	}
	s := Scenario{Args: []string{"password", "acme"}, Files: twoOrgs}
	d := Diff("ccenv", run(t, legacy, s), "mutant", run(t, mutant, s))
	for _, want := range []string{"--- file tree", `EXTRA=1`, "--- tool calls", `+ docker "ps" "-q"`} {
		if !strings.Contains(d, want) {
			t.Errorf("diff lacks %q:\n%s", want, d)
		}
	}
}

func TestNormalizer(t *testing.T) {
	n := normalizer(Tool{Replace: [][2]string{{"/src/legacy/ccenv", "<SELF>"}, {"/src/legacy", "<ROOT>"}}}, "/r/run", "/r/bin")
	in := "ccenv: bad\nberth: bad\nnot ccenv: here\n/src/legacy/ccenv: line 3\n/src/legacy/compose.yml /r/run/state /r/bin/docker " +
		"/tmp/tmp.AbCdEfGhIj orgs/.restore-Ab12Cd\n"
	want := "<tool>: bad\n<tool>: bad\nnot ccenv: here\n<SELF>: line 3\n<ROOT>/compose.yml <RUN>/state <FAKEBIN>/docker " +
		"<MKTEMP> orgs/.restore-XXXXXX\n"
	if got := n(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
