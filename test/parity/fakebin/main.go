// Command fakebin stands in for the host tools legacy ccenv and berth call (docker, tailscale, gh,
// ssh, systemctl, crontab, ...). The parity harness installs it under each tool's name on PATH.
// Every call is appended to $PARITY_LOG as one JSON line (tool, argv, the env vars the tools care
// about, and stdin when a rule asks for it), and answered from the rules in $PARITY_RULES.
//
// A call no rule matches exits 0 with no output, except for a few tools with deterministic
// built-in behaviour (date, hostname, ssh-keygen, sleep).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Rule scripts one kind of call. It is duplicated in the harness (test/parity/harness.go).
type Rule struct {
	Bin string `json:"bin"`
	// Match is a regexp over the arguments joined by single spaces; "" matches any call.
	Match  string `json:"match"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Exit   int    `json:"exit"`
	// Once: the rule answers its first matching call only.
	Once bool `json:"once"`
	// Stdin: read stdin to EOF and record it in the log.
	Stdin bool `json:"stdin"`
	// Dst: files to write (path -> content; a path ending in / is a dir) into the dir docker mounts
	// at /dst: what the restore engine would extract.
	Dst map[string]string `json:"dst"`
}

// Call is one logged invocation.
type Call struct {
	Bin   string            `json:"bin"`
	Args  []string          `json:"args"`
	Env   map[string]string `json:"env,omitempty"`
	Stdin *string           `json:"stdin,omitempty"`
	// Secrets are the files in a dir docker mounts at /secrets (the backup engine's passphrase and
	// recipients), which the tools delete before exiting.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// loggedEnv are the variables ccenv's and berth's compose() set, which matter to the comparison.
var loggedEnv = []string{"ORG", "ORG_DIR", "BIND_ADDR", "HOST_UID", "HOST_GID",
	"CLAUDE_ENV_IMAGE", "IMAGE_TAG", "CLAUDE_ENV_IMAGE_DIR"} // berth's image, for compose

// fixedNow is the only time the fakes ever report.
var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func main() {
	bin := filepath.Base(os.Args[0])
	args := os.Args[1:]
	code, err := run(bin, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakebin %s: %v\n", bin, err)
		os.Exit(99)
	}
	os.Exit(code)
}

func run(bin string, args []string) (int, error) {
	rules, err := loadRules()
	if err != nil {
		return 0, err
	}
	call := Call{Bin: bin, Args: args}
	for _, k := range loggedEnv {
		if v, ok := os.LookupEnv(k); ok {
			if call.Env == nil {
				call.Env = map[string]string{}
			}
			call.Env[k] = v
		}
	}
	if bin == "docker" {
		for _, a := range args {
			if dir, ok := strings.CutSuffix(a, ":/secrets:ro"); ok {
				call.Secrets = readSecrets(dir)
			}
		}
	}
	idx, rule, err := pick(rules, bin, strings.Join(args, " "))
	if err != nil {
		return 0, err
	}
	// crontab's "-" means "install the table from stdin": record what would be installed.
	if (rule != nil && rule.Stdin) || (rule == nil && bin == "crontab" && slices.Contains(args, "-")) {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 0, err
		}
		s := string(b)
		call.Stdin = &s
	}
	if err := appendLog(call); err != nil {
		return 0, err
	}
	if rule != nil {
		if rule.Once {
			if err := markUsed(idx); err != nil {
				return 0, err
			}
		}
		if err := extract(rule.Dst, args); err != nil {
			return 0, err
		}
		// <SCHEDULE> is the tool's backup schedule name (ccenv-backup, berth-backup).
		sched := strings.NewReplacer("<SCHEDULE>", os.Getenv("PARITY_SCHEDULE"))
		_, _ = io.WriteString(os.Stdout, sched.Replace(rule.Stdout))
		_, _ = io.WriteString(os.Stderr, sched.Replace(rule.Stderr))
		return rule.Exit, nil
	}
	return builtin(bin, args)
}

// extract writes files into the dir mounted at /dst ("-v <dir>:/dst").
func extract(files map[string]string, args []string) error {
	if len(files) == 0 {
		return nil
	}
	var dst string
	for _, a := range args {
		if d, ok := strings.CutSuffix(a, ":/dst"); ok {
			dst = d
		}
	}
	if dst == "" {
		return fmt.Errorf("rule has dst files, but nothing is mounted at /dst")
	}
	for name, content := range files {
		p := filepath.Join(dst, name)
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(p, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// readSecrets is every file in dir, by name, with its mode.
func readSecrets(dir string) map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		mode := "?"
		if fi, err := e.Info(); err == nil {
			mode = fmt.Sprintf("%04o", fi.Mode().Perm())
		}
		out[e.Name()] = mode + " " + string(b)
	}
	return out
}

func loadRules() ([]Rule, error) {
	p := os.Getenv("PARITY_RULES")
	if p == "" {
		return nil, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	return rules, json.Unmarshal(b, &rules)
}

// pick returns the first rule for bin whose Match matches, skipping used Once rules.
func pick(rules []Rule, bin, joined string) (int, *Rule, error) {
	used, err := usedRules()
	if err != nil {
		return 0, nil, err
	}
	for i := range rules {
		r := &rules[i]
		if r.Bin != bin || (r.Once && used[i]) {
			continue
		}
		ok, err := regexp.MatchString(r.Match, joined)
		if err != nil {
			return 0, nil, fmt.Errorf("rule %d: %w", i, err)
		}
		if ok {
			return i, r, nil
		}
	}
	return 0, nil, nil
}

func stateFile() string { return os.Getenv("PARITY_LOG") + ".used" }

func usedRules() (map[int]bool, error) {
	used := map[int]bool{}
	b, err := os.ReadFile(stateFile())
	if os.IsNotExist(err) {
		return used, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range strings.Fields(string(b)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, err
		}
		used[n] = true
	}
	return used, nil
}

func markUsed(i int) error { return appendFile(stateFile(), strconv.Itoa(i)+"\n") }

func appendLog(c Call) error {
	if os.Getenv("PARITY_LOG") == "" {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return appendFile(os.Getenv("PARITY_LOG"), string(b)+"\n")
}

func appendFile(p, s string) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(s)
	return errors.Join(err, f.Close())
}

// builtin is the default behaviour when no rule matches.
func builtin(bin string, args []string) (int, error) {
	switch bin {
	case "date":
		fmt.Println(formatDate(args))
	case "hostname":
		fmt.Println("parity-host")
	case "ssh-keygen":
		return sshKeygen(args)
	}
	return 0, nil // sleep, systemctl, docker, ...: succeed quietly
}

// formatDate supports what the scripts use: +FORMAT with %Y %m %d %H %M %S %s %F %T, and -Is.
func formatDate(args []string) string {
	for _, a := range args {
		switch {
		case a == "-Is" || a == "--iso-8601=seconds":
			return fixedNow.Format("2006-01-02T15:04:05-07:00")
		case strings.HasPrefix(a, "+"):
			r := strings.NewReplacer("%Y", "2006", "%m", "01", "%d", "02", "%H", "15", "%M", "04", "%S", "05",
				"%F", "2006-01-02", "%T", "15:04:05")
			out := fixedNow.Format(r.Replace(a[1:]))
			return strings.ReplaceAll(out, "%s", strconv.FormatInt(fixedNow.Unix(), 10))
		}
	}
	return fixedNow.Format("Mon Jan _2 15:04:05 MST 2006")
}

// sshKeygen writes a fixed, obviously fake key pair to -f FILE with -C COMMENT.
func sshKeygen(args []string) (int, error) {
	var file, comment string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f":
			i++
			if i < len(args) {
				file = args[i]
			}
		case "-C":
			i++
			if i < len(args) {
				comment = args[i]
			}
		case "-t", "-N", "-b":
			i++
		}
	}
	if file == "" {
		return 0, nil
	}
	if err := os.WriteFile(file, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nFAKE PARITY KEY\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		return 1, err
	}
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFAKEPARITYKEY " + comment + "\n"
	return 0, os.WriteFile(file+".pub", []byte(pub), 0o644)
}
