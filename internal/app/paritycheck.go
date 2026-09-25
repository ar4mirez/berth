package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
)

// parityNorm maps what legitimately differs between the two tools, as the parity harness does: the
// error prefix, command hints ("run: ccenv up acme"), and env set's reserved-key message.
var parityNorm = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?m)^(ccenv|berth): `), "<tool>: "},
	{regexp.MustCompile(`is managed by (ccenv|berth); edit`), "is managed by <tool>; edit"},
	{regexp.MustCompile(`\b(ccenv|berth) ([a-z])`), "<tool> $2"},
}

func normalizeTools(s string) string {
	for _, n := range parityNorm {
		s = n.re.ReplaceAllString(s, n.with)
	}
	return s
}

// parityRun is one command's result, normalized.
type parityRun struct {
	stdout, stderr string
	exit           int
}

func (r parityRun) String() string {
	return fmt.Sprintf("exit %d\n--- stdout\n%s--- stderr\n%s", r.exit, r.stdout, r.stderr)
}

// ParityCheck is `berth parity-check [--legacy PATH] [org...]`: run every command that only reads
// through ccenv and through `berth --read-only` against the same state root, and report any
// difference. It's the pre-cutover check in docs/plan.md ("ls, info, whoami and repo ls produce
// identical output under both binaries for every live org"), widened to all read commands. It
// changes nothing itself; note that ccenv's `fw show` and `repo ls` create a missing firewall.txt
// or repos.txt (PARITY.md, legacy quirk 5), as they do whenever ccenv runs them.
func (a *App) ParityCheck(ctx context.Context, args []string) error {
	legacy := ""
	var orgs []string
	for i := 0; i < len(args); i++ {
		switch x := args[i]; {
		case x == "--legacy":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", x)
			}
			i++
			legacy = args[i]
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		default:
			orgs = append(orgs, x)
		}
	}
	home := a.State.Home.Path
	if legacy == "" {
		if p := path.Join(home, "ccenv"); a.isFile(p) {
			legacy = p
		} else if p, err := a.capture(ctx, true, "sh", "-c", "command -v ccenv"); err == nil && p != "" {
			legacy = p
		} else {
			return errors.New("can't find ccenv: pass --legacy <path to the ccenv script>")
		}
	}
	if a.Self == "" {
		return fmt.Errorf("can't tell where the %s binary is", Tool)
	}
	if len(orgs) == 0 {
		for _, o := range a.OrgNames() {
			if a.env(o, "MANAGER") == "berth" {
				fmt.Fprintf(a.Stdout, "skip %s: MANAGER=berth (ccenv refuses it)\n", o)
				continue
			}
			orgs = append(orgs, o)
		}
		if len(orgs) == 0 {
			return fmt.Errorf("no ccenv orgs under %s (is --home the legacy checkout?)", home)
		}
	}
	fmt.Fprintf(a.Stdout, "Comparing %s with %s --read-only --home %s\n\n", legacy, a.Self, home)

	checks := [][]string{{"ls"}, {"whoami"}}
	for _, o := range orgs {
		checks = append(checks, []string{"info", o}, []string{"whoami", o}, []string{"repo", "ls", o}, []string{"repo", "audit", o},
			[]string{"repo", "policy", o}, []string{"fw", o, "show"}, []string{"env", o, "ls"}, []string{"remote", o, "status"})
	}
	differ := 0
	for _, c := range checks {
		// Container state can change between the two runs (a Remote Control restart, say): a
		// difference counts only if a second pair of runs still shows it.
		var l, b parityRun
		same := false
		for range 2 {
			l = a.parityRun(ctx, legacy, []string{"CCENV_ORGS=" + path.Join(home, "orgs")}, c)
			b = a.parityRun(ctx, a.Self, nil, append([]string{"--read-only", "--home", home}, c...))
			if same = l == b; same {
				break
			}
		}
		if same {
			fmt.Fprintf(a.Stdout, "ok    %s\n", strings.Join(c, " "))
			continue
		}
		differ++
		fmt.Fprintf(a.Stdout, "DIFF  %s\n%s\n", strings.Join(c, " "), indent(lineDiff("ccenv", l.String(), "berth", b.String())))
	}
	fmt.Fprintf(a.Stdout, "\n%d checks, %d differ\n", len(checks), differ)
	if differ > 0 {
		return &Exit{Code: 1}
	}
	return nil
}

// parityRun runs one tool with a command line and normalizes what it printed.
func (a *App) parityRun(ctx context.Context, bin string, env, args []string) parityRun {
	var out, errb bytes.Buffer
	err := a.Host.Exec.Run(ctx, host.Cmd{Args: append([]string{bin}, args...), Env: env, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb})
	r := parityRun{stdout: normalizeTools(out.String()), stderr: normalizeTools(errb.String())}
	var ec interface{ ExitCode() int }
	switch {
	case err == nil:
	case errors.As(err, &ec):
		r.exit = ec.ExitCode()
	default:
		r.exit, r.stderr = -1, r.stderr+err.Error()+"\n"
	}
	return r
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(s, "\n") {
		if l != "" {
			b.WriteString("      " + l)
		}
	}
	return b.String()
}

// lineDiff is a minimal unified-style line diff (longest common subsequence): "-" lines are only in
// a, "+" lines only in b.
func lineDiff(aName, a, bName, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", aName, bName)
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			out.WriteString("  " + x[i] + "\n")
			i++
			j++
		case j < len(y) && (i == len(x) || lcs[i][j+1] >= lcs[i+1][j]):
			out.WriteString("+ " + y[j] + "\n")
			j++
		default:
			out.WriteString("- " + x[i] + "\n")
			i++
		}
	}
	return out.String()
}
