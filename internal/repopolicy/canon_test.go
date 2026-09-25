package repopolicy

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The three implementations of docs/repo-policy.md. Bash and node are the things under test
// here; this Go test is the runner.
var (
	policySh = filepath.Join("..", "..", "image", "repo-policy.sh")
	guardJS  = filepath.Join("..", "..", "image", "repo-guard.js")
)

type canonCase struct {
	line              int
	input, host, want string
}

func loadCases(t *testing.T) []canonCase {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "canon.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var cases []canonCase
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("canon.tsv:%d: want 3 tab-separated columns, got %d", n, len(cols))
		}
		c := canonCase{line: n}
		for i, dst := range []*string{&c.input, &c.host, &c.want} {
			v := cols[i]
			if strings.HasPrefix(v, `"`) {
				if v, err = strconv.Unquote(v); err != nil {
					t.Fatalf("canon.tsv:%d: column %d: %v", n, i+1, err)
				}
			} else if v == "-" && i > 0 {
				v = ""
			}
			*dst = v
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 50 {
		t.Fatalf("only %d cases in canon.tsv; the table didn't load properly", len(cases))
	}
	return cases
}

// need returns whether bin is installed. Locally a missing one is skipped with a note; in CI
// ($CI set) it fails, so the three-way check can't quietly become a two-way one.
func need(t *testing.T, bin string) bool {
	t.Helper()
	if _, err := exec.LookPath(bin); err == nil {
		return true
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("%s is required in CI for the three-language repo-policy check", bin)
	}
	t.Logf("no %s here: skipping that implementation", bin)
	return false
}

// nulJoin packs strings for the bash/node batch runners (inputs are never NUL).
func nulJoin(ss []string) []byte {
	var b bytes.Buffer
	for _, s := range ss {
		b.WriteString(s)
		b.WriteByte(0)
	}
	return b.Bytes()
}

func nulSplit(t *testing.T, out []byte, want int) []string {
	t.Helper()
	parts := strings.Split(string(out), "\x00")
	if len(parts) != want+1 || parts[want] != "" {
		t.Fatalf("got %d results for %d inputs:\n%q", len(parts)-1, want, out)
	}
	return parts[:want]
}

func runBatch(t *testing.T, cmd *exec.Cmd, stdin []byte, want int) []string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(stdin), &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", cmd.Args[0], err, errb.String())
	}
	if errb.Len() > 0 {
		t.Errorf("%s wrote to stderr:\n%s", cmd.Args[0], errb.String())
	}
	return nulSplit(t, out.Bytes(), want)
}

// bashCanon runs repo_canon for each (input, host) pair, under the same set -euo pipefail as its
// callers: if it ever returned non-zero, the batch would stop short and the test would fail.
func bashCanon(t *testing.T, pairs []string) []string {
	script := `set -euo pipefail; . "$1"
while IFS= read -r -d '' u && IFS= read -r -d '' h; do
  c=$(repo_canon "$u" "$h"); printf '%s\0' "$c"
done`
	return runBatch(t, exec.Command("bash", "-c", script, "bash", policySh), nulJoin(pairs), len(pairs)/2)
}

func nodeCanon(t *testing.T, pairs []string) []string {
	script := `const { canon } = require(process.argv[1]);
const p = require('fs').readFileSync(0, 'utf8').split('\0');
const out = [];
for (let i = 0; i + 1 < p.length; i += 2) out.push(canon(p[i], p[i + 1]) + '\0');
process.stdout.write(out.join(''));`
	abs, err := filepath.Abs(guardJS)
	if err != nil {
		t.Fatal(err)
	}
	return runBatch(t, exec.Command("node", "-e", script, abs), nulJoin(pairs), len(pairs)/2)
}

func TestCanonAgreement(t *testing.T) {
	cases := loadCases(t)
	pairs := make([]string, 0, 2*len(cases))
	for _, c := range cases {
		pairs = append(pairs, c.input, c.host)
	}
	impls := map[string][]string{}
	if need(t, "bash") {
		impls["bash"] = bashCanon(t, pairs)
	}
	if need(t, "node") {
		impls["node"] = nodeCanon(t, pairs)
	}
	for i, c := range cases {
		got := map[string]string{"go": Canon(c.input, c.host)}
		for name, res := range impls {
			got[name] = res[i]
		}
		for name, g := range got {
			if g != c.want {
				t.Errorf("canon.tsv:%d: %s: canon(%q, %q) = %q, want %q", c.line, name, c.input, c.host, g, c.want)
			}
		}
	}
}

// TestCanonStable: a canonical form is its own canonical form, so re-canonicalizing a registered
// value (as the guards do) never changes what it matches.
func TestCanonStable(t *testing.T) {
	for _, c := range loadCases(t) {
		if c.want == "" {
			continue
		}
		if again := Canon(c.want, ""); again != c.want {
			t.Errorf("canon.tsv:%d: Canon(%q) = %q; a canonical form must map to itself", c.line, c.want, again)
		}
	}
}

// A synthetic repos.txt with every line shape the readers must agree on.
const reposTxt = "# Repos allowed in the 't-policy' container.\n" +
	"# <dir under /workspace>  <clone url>  [branch]\n" +
	"\n" +
	"app git@github.com:acme/app.git\n" +
	"api\tgit@github.com:acme/api.git\tdevelop\n" +
	"   indented   https://gitlab.com/globex/tool   \n" +
	"notes local\n" +
	"odd ssh://github.com:2222/acme/odd\n" + // no canonical form: dir registered, no repo allowed
	"crlf git@github.com:acme/crlf.git\r\n" +
	"crlfspace git@github.com:acme/cs.git\r \n" + // CR not at the end: part of the URL
	"lonely\n" + // no URL: skipped
	"#hidden git@github.com:acme/hidden.git\n" +
	"nbsp\u00a0git@github.com:acme/nbsp.git\n" + // NBSP isn't a separator: one field, skipped
	"last git@github.com:initech/last.git" // no trailing newline

func TestEntriesAgreement(t *testing.T) {
	want := []Entry{
		{Dir: "app", URL: "git@github.com:acme/app.git", Canon: "github.com/acme/app"},
		{Dir: "api", URL: "git@github.com:acme/api.git", Branch: "develop", Canon: "github.com/acme/api"},
		{Dir: "indented", URL: "https://gitlab.com/globex/tool", Canon: "gitlab.com/globex/tool"},
		{Dir: "notes", URL: "local", Canon: "local/notes"},
		{Dir: "odd", URL: "ssh://github.com:2222/acme/odd", Canon: ""},
		{Dir: "crlf", URL: "git@github.com:acme/crlf.git", Canon: "github.com/acme/crlf"},
		{Dir: "crlfspace", URL: "git@github.com:acme/cs.git\r", Canon: ""},
		{Dir: "last", URL: "git@github.com:initech/last.git", Canon: "github.com/initech/last"},
	}
	got := Entries([]byte(reposTxt))
	if len(got) != len(want) {
		t.Fatalf("Go Entries: %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Go entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	type dc struct{ dir, canon string }
	wantDC := make([]dc, len(want))
	for i, e := range want {
		wantDC[i] = dc{e.Dir, e.Canon}
	}

	file := filepath.Join(t.TempDir(), "repos.txt")
	if err := os.WriteFile(file, []byte(reposTxt), 0o600); err != nil {
		t.Fatal(err)
	}
	check := func(name, out string) {
		t.Helper()
		var gotDC []dc
		for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			d, c, _ := strings.Cut(l, " ")
			gotDC = append(gotDC, dc{d, c})
		}
		if len(gotDC) != len(wantDC) {
			t.Fatalf("%s: %d entries, want %d:\n%s", name, len(gotDC), len(wantDC), out)
		}
		for i := range wantDC {
			if gotDC[i] != wantDC[i] {
				t.Errorf("%s entry %d = %+v, want %+v", name, i, gotDC[i], wantDC[i])
			}
		}
	}
	if need(t, "bash") {
		// repo_entries prints "dir canon url branch", with "-" for no canonical form.
		script := `set -euo pipefail; REPOS_FILE="$2"; . "$1"; repo_entries | awk '{ print $1, ($2 == "-" ? "" : $2) }'`
		out, err := exec.Command("bash", "-c", script, "bash", policySh, file).Output()
		if err != nil {
			t.Fatal(err)
		}
		check("bash", string(out))
	}
	if need(t, "node") {
		abs, _ := filepath.Abs(guardJS)
		script := `const { entries } = require(process.argv[1]);
for (const e of entries(process.argv[2])) console.log(e.dir + ' ' + e.canon);`
		out, err := exec.Command("node", "-e", script, abs, file).Output()
		if err != nil {
			t.Fatal(err)
		}
		check("node", string(out))
	}
}

// An entry with no canonical form must not let an unparseable reference through: in every
// implementation, "" (and bash's "-" placeholder) allows nothing.
func TestNothingAllowsTheEmptyCanon(t *testing.T) {
	es := Entries([]byte(reposTxt))
	for _, c := range []string{"", "-"} {
		if Allowed(es, c) {
			t.Errorf("Go: Allowed(%q) = true", c)
		}
	}
	if !Allowed(es, "github.com/acme/app") || Allowed(es, "github.com/acme/other") {
		t.Error("Go: Allowed doesn't match registered repos")
	}
	file := filepath.Join(t.TempDir(), "repos.txt")
	if err := os.WriteFile(file, []byte(reposTxt), 0o600); err != nil {
		t.Fatal(err)
	}
	if need(t, "bash") {
		script := `set -euo pipefail; REPOS_FILE="$2"; . "$1"
for c in "" - github.com/acme/other; do if repo_allowed_canon "$c"; then echo "allowed: [$c]"; fi; done
repo_allowed_canon github.com/acme/app || echo "not allowed: registered repo"
repo_allowed_dir odd || echo "not allowed: dir of an entry without a canonical form"`
		out, err := exec.Command("bash", "-c", script, "bash", policySh, file).CombinedOutput()
		if err != nil || len(out) > 0 {
			t.Errorf("bash: %v\n%s", err, out)
		}
	}
	if need(t, "node") {
		abs, _ := filepath.Abs(guardJS)
		// The same Set the hook builds in main().
		script := `const { entries, canon } = require(process.argv[1]);
const allowed = new Set(entries(process.argv[2]).map(e => e.canon).filter(Boolean));
for (const u of ['ssh://github.com:2222/acme/odd', 'file:///x', 'https://github.com/x@github.com/acme/app'])
  if (allowed.has(canon(u))) console.log('allowed: ' + u);
if (!allowed.has(canon('git@github.com:acme/app.git'))) console.log('not allowed: registered repo');`
		out, err := exec.Command("node", "-e", script, abs, file).CombinedOutput()
		if err != nil || len(out) > 0 {
			t.Errorf("node: %v\n%s", err, out)
		}
	}
}
