package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// inventory extracts ccenv's CLI surface from legacy/ccenv, so PARITY.md can't silently fall behind
// when legacy grows a command, subcommand, flag or alias (as it did with `ccenv env`).
//
// Items are "<command> <token>" (or just "<command>"), taken from:
//   - the top-level dispatch (`case "$sub" in` at the bottom);
//   - flags in case patterns (`--as)`, `-o|--output)`) and in comparisons (`= --force ]`);
//   - alias groups, any case pattern of plain words joined by | (`deny|remove|rm)`);
//   - the subcommand lists in the bash completion script.
//
// Each token is attributed to the command whose function it appears in.
func inventory(t *testing.T, src string) map[string][]string {
	t.Helper()
	lines := strings.Split(src, "\n")
	items := map[string][]string{} // command -> tokens
	add := func(cmd, tok string) {
		if tok == cmd {
			tok = "" // a command isn't its own alias
		}
		if tok != "" && !contains(items[cmd], tok) {
			items[cmd] = append(items[cmd], tok)
		} else if _, ok := items[cmd]; !ok {
			items[cmd] = nil
		}
	}

	// Top-level dispatch: everything after `sub="${1:-help}"`.
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, `sub="${1:-help}"`) {
			start = i
		}
	}
	if start < 0 {
		t.Fatal("legacy/ccenv: dispatch not found")
	}
	dispatchPat := regexp.MustCompile(`^  ([a-z][a-z-]*(?:\|-{0,2}[a-z][a-z-]*)*)\)`)
	fnOf := map[string]string{} // function name -> command
	callRe := regexp.MustCompile(`\bcmd_([a-z_]+)\b`)
	for _, l := range lines[start:] {
		m := dispatchPat.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		alts := strings.Split(m[1], "|")
		cmd := alts[0]
		add(cmd, "")
		for _, a := range alts[1:] {
			add(cmd, a) // help|-h|--help
		}
		for _, c := range callRe.FindAllStringSubmatch(l, -1) {
			// cmd_repo belongs to `repo`, even though `clone` (dispatched earlier) calls it too.
			if own := strings.ReplaceAll(c[1], "_", "-"); own == cmd {
				fnOf[c[1]] = cmd
			} else if _, seen := fnOf[c[1]]; !seen {
				fnOf[c[1]] = cmd
			}
		}
		scanTokens(l, func(tok string) { add(cmd, tok) }) // inline commands (up, attach, run...)
	}

	// Function bodies: cmd_x() { ... } up to a line that is exactly "}".
	fnStart := regexp.MustCompile(`^cmd_([a-z_]+)\(\) \{`)
	for i := 0; i < start; i++ {
		m := fnStart.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		cmd, ok := fnOf[m[1]]
		if !ok || cmd == "completion" { // the completion script is scanned separately below
			continue
		}
		for j := i; j < start && (j == i || lines[j] != "}"); j++ {
			scanTokens(lines[j], func(tok string) { add(cmd, tok) })
		}
	}

	// Subcommands from the completion script: `  <cmd>) COMPREPLY=($(compgen -W "a b c" ...`.
	compRe := regexp.MustCompile(`(?:^|\s)([a-z-]+)\)\s+COMPREPLY=\(\$\(compgen -W "([^"$]+)"`)
	for _, l := range lines {
		for _, m := range compRe.FindAllStringSubmatch(l, -1) {
			if _, known := items[m[1]]; known {
				for _, w := range strings.Fields(m[2]) {
					add(m[1], w)
				}
			}
		}
	}
	for _, toks := range items {
		sort.Strings(toks)
	}
	return items
}

var (
	// A case pattern at the start of a line, after ";;", or right after `case … in`: alternatives that are flags or plain words.
	casePat = regexp.MustCompile(`(?:^\s*|;;\s*|\bin\s+)((?:-{1,2}[A-Za-z][A-Za-z-]*|[a-z][a-z-]*)(?:\|(?:-{1,2}[A-Za-z][A-Za-z-]*|[a-z][a-z-]*))*)\)`)
	// [ "$x" = --flag ] and [ "${2:-}" != "--flag" ]
	cmpFlag = regexp.MustCompile(`!?=\s*"?(--[a-z][a-z-]*)"?\s*\]`)
	// A table cell separator: a | not preceded by a backslash.
	unescapedPipe = regexp.MustCompile(`(?:^|[^\\])\|`)
)

// scanTokens reports flags anywhere in case patterns and comparisons, and word alternatives of
// alias groups (a pattern with at least one |).
func scanTokens(line string, f func(string)) {
	for _, m := range casePat.FindAllStringSubmatch(line, -1) {
		alts := strings.Split(m[1], "|")
		for _, a := range alts {
			if strings.HasPrefix(a, "-") || len(alts) > 1 {
				f(a)
			}
		}
	}
	for _, m := range cmpFlag.FindAllStringSubmatch(line, -1) {
		f(m[1])
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// parityRows returns the first cell of every table row in PARITY.md, without backticks.
func parityRows(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "PARITY.md"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "| `") {
			continue
		}
		// The first cell ends at the first unescaped |; "\|" is a literal pipe inside it.
		cell := unescapedPipe.Split(strings.TrimPrefix(l, "|"), 2)[0]
		cell = strings.ReplaceAll(strings.ReplaceAll(cell, `\|`, "|"), "`", "")
		rows = append(rows, strings.TrimSpace(cell))
	}
	return rows
}

// TestParityInventory: every command, and every flag, alias and subcommand of it, has a row in
// PARITY.md whose item starts with the command and mentions the token.
func TestParityInventory(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "legacy", "ccenv"))
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory(t, string(src))
	if len(inv) < 30 {
		t.Fatalf("only %d commands found in legacy/ccenv; the extractor is broken", len(inv))
	}
	rows := parityRows(t)
	word := func(s, tok string) bool { // tok as a whole word (or |-separated alternative) in s
		return regexp.MustCompile(`(^|[\s|/(\[])` + regexp.QuoteMeta(tok) + `($|[\s|/)\]=,])`).MatchString(s)
	}
	cmds := make([]string, 0, len(inv))
	for c := range inv {
		cmds = append(cmds, c)
	}
	sort.Strings(cmds)
	for _, cmd := range cmds {
		var mine []string
		for _, r := range rows {
			if r == cmd || strings.HasPrefix(r, cmd+" ") {
				mine = append(mine, r)
			}
		}
		if len(mine) == 0 {
			t.Errorf("PARITY.md: no row for `%s`", cmd)
			continue
		}
		for _, tok := range inv[cmd] {
			found := false
			for _, r := range mine {
				if word(strings.TrimPrefix(r, cmd), tok) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("PARITY.md: no `%s` row mentions %s", cmd, tok)
			}
		}
	}
}

// TestInventoryExtractor pins the extractor on a small script, so a broken regexp can't make the
// completeness test vacuous.
func TestInventoryExtractor(t *testing.T) {
	src := `cmd_widget() {
  while [ $# -gt 0 ]; do case "$1" in
    --size) s="$2"; shift 2;; --color|-c) c="$2"; shift 2 ;; *) die x ;; esac; done
  case "${2:-show}" in
    show|list) echo ;;
    spin) id=$(id -u) ;;
  esac
  [ "${3:-}" = --force ] && echo
}
_c() {
  case x in
    3) case $cmd in widget) COMPREPLY=($(compgen -W "show spin" -- "$cur")) ;; esac ;;
  esac
}
sub="${1:-help}"; shift || true
case "$sub" in
  widget) cmd_widget "$@" ;;
  up)     need_org "${1:-}"; docker compose up ;;
  help|-h|--help) usage ;;
esac
`
	got := inventory(t, src)
	want := map[string][]string{
		"widget": {"--color", "--force", "--size", "-c", "list", "show", "spin"},
		"up":     nil,
		"help":   {"--help", "-h"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for cmd, toks := range want {
		if strings.Join(got[cmd], " ") != strings.Join(toks, " ") {
			t.Errorf("%s: got %v, want %v", cmd, got[cmd], toks)
		}
	}
}
