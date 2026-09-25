// Package parity runs legacy ccenv and berth on the same synthetic fixtures and diffs what they do:
// stdout, stderr (with the "ccenv:"/"berth:" prefix normalized), the exit code, every call to the
// fake host tools (argv and the compose env), and the resulting file tree with modes.
//
// Each run gets a fresh directory:
//
//	<run>/state   the state root: berth --home, ccenv's CCENV_ORGS=<run>/state/orgs and
//	              CCENV_BACKUP_DIR=<run>/state/backups
//	<run>/home    $HOME, so ~/.config, ~/.ssh, ~/.local/bin writes land in the snapshot too
//
// and the fakes (test/parity/fakebin) come first on PATH. Nothing outside <run> is touched, and the
// fixtures are synthetic only.
package parity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Fakes are the tools the harness replaces. The first six are the host tools ccenv drives; the
// rest would make runs slow or non-deterministic (sleep, random keys, the host's name, the clock).
var Fakes = []string{"docker", "tailscale", "gh", "ssh", "systemctl", "crontab",
	"loginctl", "journalctl", "rsync", "ssh-keygen", "hostname", "date", "sleep"}

// Rule scripts a fake's answer (see test/parity/fakebin).
type Rule struct {
	Bin    string `json:"bin"`
	Match  string `json:"match"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Exit   int    `json:"exit"`
	Once   bool   `json:"once"`
	Stdin  bool   `json:"stdin"`
}

// File is one fixture entry, at a path relative to the run dir ("state/orgs/acme/org.env").
type File struct {
	Content string
	Mode    fs.FileMode // default 0600 for files, 0700 for dirs
	Dir     bool
	Link    string // symlink target
}

// Scenario is one command line and the world it runs in.
type Scenario struct {
	Name  string
	Args  []string
	Stdin string
	Files map[string]File
	Rules []Rule
	// Env is extra environment for both tools (KEY=VALUE).
	Env []string
	// Mask are regexps replaced by <MASKED> in stdout and stderr: output that is random by design.
	Mask []string
	// Random names files whose content is random by design (e.g. a generated password): both sides
	// must match the regexp, instead of each other.
	Random map[string]string
}

// Tool is one side of the comparison.
type Tool struct {
	Name string
	// Command returns argv for the scenario's args, given the run dir.
	Command func(run string, args []string) []string
	// Env is extra environment for this tool.
	Env func(run string) []string
	// Replace maps tool-specific absolute paths to placeholders, longest first.
	Replace [][2]string
	// Prepare adjusts the fixture for this tool after it is written (optional).
	Prepare func(run string) error
	// Content adjusts a file's content before it is compared (optional); rel is relative to the run dir.
	Content func(rel, content string) string
	// SkipTree leaves paths out of the file tree (optional).
	SkipTree func(rel string) bool
	// Calls adjusts a normalized tool-call line before it is compared (optional).
	Calls func(string) string
}

// Result is what a run did, normalized.
type Result struct {
	Stdout, Stderr string
	Exit           int
	Calls          []string
	Tree           []string
}

// Env holds the prepared fakes; build it once per test binary with Setup.
type Env struct {
	FakeBin string // directory with one fake per name in Fakes
}

// Setup builds fakebin into dir and links it under every fake name.
func Setup(dir, repoRoot string) (*Env, error) {
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, err
	}
	exe := filepath.Join(dir, "fakebin")
	build := exec.Command("go", "build", "-o", exe, "./test/parity/fakebin")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building fakebin: %w\n%s", err, out)
	}
	for _, name := range Fakes {
		if err := os.Symlink(exe, filepath.Join(bin, name)); err != nil {
			return nil, err
		}
	}
	return &Env{FakeBin: bin}, nil
}

// Legacy is legacy/ccenv, run unmodified.
func Legacy(repoRoot string) Tool {
	dir := filepath.Join(repoRoot, "legacy")
	return Tool{
		Name:    "ccenv",
		Command: func(_ string, args []string) []string { return append([]string{filepath.Join(dir, "ccenv")}, args...) },
		Env: func(run string) []string {
			return []string{"CCENV_ORGS=" + filepath.Join(run, "state", "orgs"), "CCENV_BACKUP_DIR=" + filepath.Join(run, "state", "backups")}
		},
		Replace: [][2]string{{filepath.Join(dir, "ccenv"), "<SELF>"}, {dir, "<ROOT>"}},
	}
}

// managerLine is what makes a fixture org berth's own (see Berth).
const managerLine = "MANAGER=berth\n"

// Berth is a built berth binary, pointed at the run's state root. Its writing commands only act on
// orgs with MANAGER=berth, and legacy refuses exactly those, so the berth side's fixture gets
// MANAGER=berth as the first line of every org.env, and that line is dropped again before comparing.
// First, not last: it survives in-place rewrites and files without a final newline.
func Berth(bin string) Tool {
	t := BerthUnowned(bin)
	t.Prepare = func(run string) error {
		return filepath.WalkDir(filepath.Join(run, "state"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.Name() != "org.env" || !d.Type().IsRegular() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(p, append([]byte(managerLine), b...), 0o600) // keeps the fixture's mode
		})
	}
	t.Content = func(rel, c string) string {
		if filepath.Base(rel) == "org.env" {
			return strings.TrimPrefix(c, managerLine)
		}
		return c
	}
	// berth builds and runs its own image (plan, decision 5): berth/claude-env:<hash>, from its
	// embedded copy in <state>/berth/, passed to compose as CLAUDE_ENV_IMAGE*/IMAGE_TAG. Map that onto
	// ccenv's claude-env and <root>/image, and leave the copy out of the tree. TestBerthAssets checks
	// the real values.
	t.Replace = append(t.Replace, [2]string{"<RUN>/state/berth/compose.yml", "<ROOT>/compose.yml"},
		[2]string{"<RUN>/state/berth/image", "<ROOT>/image"}, [2]string{"<RUN>/state/compose.yml", "<ROOT>/compose.yml"})
	t.SkipTree = func(rel string) bool { return rel == "state/berth" || strings.HasPrefix(rel, "state/berth/") }
	t.Calls = func(c string) string {
		c = berthEnv.ReplaceAllString(c, "")
		return berthTag.ReplaceAllString(c, `"claude-env"`)
	}
	return t
}

var (
	berthEnv = regexp.MustCompile(`  \[(CLAUDE_ENV_IMAGE|IMAGE_TAG|CLAUDE_ENV_IMAGE_DIR)="[^"]*"\]`)
	berthTag = regexp.MustCompile(`"berth/claude-env:[0-9a-f]{12}"`)
)

// BerthUnowned is berth on the fixture as written: legacy-owned orgs (no MANAGER line).
func BerthUnowned(bin string) Tool {
	return Tool{
		Name: "berth",
		Command: func(run string, args []string) []string {
			return append([]string{bin, "--home", filepath.Join(run, "state")}, args...)
		},
		Env:     func(string) []string { return nil },
		Replace: [][2]string{{bin, "<SELF>"}},
	}
}

// Run executes s with tool in a fresh directory under base.
func (e *Env) Run(ctx context.Context, base string, tool Tool, s Scenario) (Result, error) {
	root, err := os.MkdirTemp(base, tool.Name+"-")
	if err != nil {
		return Result{}, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil { // macOS /tmp is a symlink
		return Result{}, err
	}
	run, fake := filepath.Join(root, "run"), filepath.Join(root, "fake")
	for _, d := range []string{filepath.Join(run, "state"), filepath.Join(run, "home"), fake} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Result{}, err
		}
	}
	if err := writeFixture(run, s.Files); err != nil {
		return Result{}, err
	}
	if tool.Prepare != nil {
		if err := tool.Prepare(run); err != nil {
			return Result{}, err
		}
	}
	rules, err := json.Marshal(s.Rules)
	if err != nil {
		return Result{}, err
	}
	rulesFile, logFile := filepath.Join(fake, "rules.json"), filepath.Join(fake, "calls.jsonl")
	if err := os.WriteFile(rulesFile, rules, 0o600); err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	argv := tool.Command(run, s.Args)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = filepath.Join(run, "home")
	cmd.Env = append([]string{
		"PATH=" + e.FakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + filepath.Join(run, "home"),
		"USER=parity", "LOGNAME=parity", "LC_ALL=C", "TZ=UTC", "TERM=dumb",
		// git config --global reads only the run's home; no system config.
		"GIT_CONFIG_GLOBAL=" + filepath.Join(run, "home", ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"PARITY_RULES=" + rulesFile, "PARITY_LOG=" + logFile,
	}, tool.Env(run)...)
	cmd.Env = append(cmd.Env, s.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(s.Stdin), &stdout, &stderr
	err = cmd.Run()
	exit := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
	} else if err != nil {
		return Result{}, fmt.Errorf("%s: %w", tool.Name, err)
	}
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("%s: timed out", tool.Name)
	}

	n := normalizer(tool, run, e.FakeBin)
	mask := func(x string) string {
		for _, m := range s.Mask {
			x = regexp.MustCompile(m).ReplaceAllString(x, "<MASKED>")
		}
		return x
	}
	res := Result{Stdout: mask(n(stdout.String())), Stderr: mask(n(stderr.String())), Exit: exit}
	if res.Calls, err = readCalls(logFile, n); err != nil {
		return Result{}, err
	}
	if tool.Calls != nil {
		for i, c := range res.Calls {
			res.Calls[i] = tool.Calls(c)
		}
	}
	if res.Tree, err = snapshot(run, s.Random, n, tool.Content, tool.SkipTree); err != nil {
		return Result{}, err
	}
	return res, nil
}

func writeFixture(run string, files map[string]File) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths) // parents before children
	for _, p := range paths {
		f, full := files[p], filepath.Join(run, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return err
		}
		switch {
		case f.Link != "":
			if err := os.Symlink(f.Link, full); err != nil {
				return err
			}
		case f.Dir:
			mode := f.Mode
			if mode == 0 {
				mode = 0o700
			}
			if err := os.MkdirAll(full, mode); err != nil {
				return err
			}
			if err := os.Chmod(full, mode); err != nil {
				return err
			}
		default:
			mode := f.Mode
			if mode == 0 {
				mode = 0o600
			}
			if err := os.WriteFile(full, []byte(f.Content), mode); err != nil {
				return err
			}
			if err := os.Chmod(full, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

var (
	mktempName  = regexp.MustCompile(`/tmp/tmp\.[A-Za-z0-9]{10}`)
	restoreName = regexp.MustCompile(`\.restore-[A-Za-z0-9]{6}`)
	toolPrefix  = regexp.MustCompile(`(?m)^(ccenv|berth): `)
	// A command hint ("run: ccenv login acme"): each tool names itself. Not "ccenv-backup",
	// ".ccenv-manifest.json" or "managed by ccenv;", which are wire names or plain words.
	toolHint = regexp.MustCompile(`\b(ccenv|berth) ([a-z])`)
	// env set's reserved-key message names the tool that manages those keys.
	toolManaged = regexp.MustCompile(`is managed by (ccenv|berth); edit`)
)

// normalizer maps what legitimately differs between runs and tools to placeholders.
func normalizer(tool Tool, run, fakeBin string) func(string) string {
	repl := append([][2]string{{run, "<RUN>"}, {fakeBin, "<FAKEBIN>"}}, tool.Replace...)
	return func(s string) string {
		for _, r := range repl {
			s = strings.ReplaceAll(s, r[0], r[1])
		}
		s = mktempName.ReplaceAllString(s, "<MKTEMP>")
		s = restoreName.ReplaceAllString(s, ".restore-XXXXXX")
		s = toolPrefix.ReplaceAllString(s, "<tool>: ")
		s = toolManaged.ReplaceAllString(s, "is managed by <tool>; edit")
		return toolHint.ReplaceAllString(s, "<tool> $2")
	}
}

func readCalls(logFile string, n func(string) string) ([]string, error) {
	b, err := os.ReadFile(logFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c struct {
			Bin     string            `json:"bin"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Stdin   *string           `json:"stdin"`
			Secrets map[string]string `json:"secrets"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("fake log: %w", err)
		}
		var b strings.Builder
		b.WriteString(c.Bin)
		for _, a := range c.Args {
			fmt.Fprintf(&b, " %q", a)
		}
		keys := make([]string, 0, len(c.Env))
		for k := range c.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  [%s=%q]", k, c.Env[k])
		}
		names := make([]string, 0, len(c.Secrets))
		for k := range c.Secrets {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			fmt.Fprintf(&b, "  {secret %s: %q}", k, c.Secrets[k])
		}
		if c.Stdin != nil {
			fmt.Fprintf(&b, "  <stdin %q>", *c.Stdin)
		}
		out = append(out, n(b.String()))
	}
	return out, nil
}

// snapshot lists every entry under run: path, type, mode, and content (or its hash, if large or
// binary). Files in random must match their regexp instead of being compared.
func snapshot(run string, random map[string]string, n func(string) string, content func(rel, c string) string, skip func(string) bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(run, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(run, p)
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		mode := fmt.Sprintf("%04o", info.Mode().Perm()|info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out = append(out, fmt.Sprintf("%s -> %s", rel, n(target)))
		case d.IsDir():
			out = append(out, fmt.Sprintf("%s/ %s", rel, mode))
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if content != nil {
				b = []byte(content(rel, string(b)))
			}
			out = append(out, fmt.Sprintf("%s %s %s", rel, mode, describe(rel, b, random, n)))
		}
		return nil
	})
	return out, err
}

func describe(rel string, b []byte, random map[string]string, n func(string) string) string {
	if pat, ok := random[rel]; ok {
		if regexp.MustCompile(pat).Match(b) {
			return fmt.Sprintf("<random, matches %s>", pat)
		}
		return fmt.Sprintf("<random, does NOT match %s: %q>", pat, b)
	}
	if len(b) <= 8192 && utf8.Valid(b) {
		return fmt.Sprintf("%q", n(string(b)))
	}
	return fmt.Sprintf("<%d bytes sha256:%x>", len(b), sha256.Sum256(b))
}

// Diff describes every way a and b differ ("" if they don't), labelled with the tools' names.
func Diff(aName string, a Result, bName string, b Result) string {
	var out strings.Builder
	section := func(what string, x, y []string) {
		if d := lineDiff(x, y); d != "" {
			fmt.Fprintf(&out, "--- %s (-%s +%s)\n%s", what, aName, bName, d)
		}
	}
	if a.Exit != b.Exit {
		fmt.Fprintf(&out, "--- exit code: %s %d, %s %d\n", aName, a.Exit, bName, b.Exit)
	}
	section("stdout", lines(a.Stdout), lines(b.Stdout))
	section("stderr", lines(a.Stderr), lines(b.Stderr))
	section("tool calls", a.Calls, b.Calls)
	section("file tree", a.Tree, b.Tree)
	return out.String()
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lineDiff is a small LCS diff: fine for command output and file lists.
func lineDiff(a, b []string) string {
	if slices.Equal(a, b) {
		return ""
	}
	m, n := len(a), len(b)
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < m || j < n {
		switch {
		case i < m && j < n && a[i] == b[j]:
			out.WriteString("  " + a[i] + "\n")
			i++
			j++
		case j < n && (i == m || lcs[i][j+1] >= lcs[i+1][j]):
			out.WriteString("+ " + b[j] + "\n")
			j++
		default:
			out.WriteString("- " + a[i] + "\n")
			i++
		}
	}
	return out.String()
}
