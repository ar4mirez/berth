package parity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The state root's lock (#46): commands running at the same time on one state root must not pick
// the same ports or lose each other's edits. These run the real berth binary in parallel
// processes against one fixture (the harness's per-run dirs can't share state).

func lockFixture(t *testing.T) (state, home string, environ []string) {
	t.Helper()
	root := t.TempDir()
	state, home = filepath.Join(root, "state"), filepath.Join(root, "home")
	files := merge(twoOrgs, map[string]File{"state/orgs/globex/org.env": {Content: "MANAGER=berth\n" + globexEnv}})
	if err := writeFixture(root, files); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	environ = append(os.Environ(), "PATH="+env.FakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home, "PARITY_LOG="+filepath.Join(root, "calls.jsonl"), "PARITY_RULES=")
	return state, home, environ
}

// parallel runs n berth command lines at once and fails on any error.
func parallel(t *testing.T, environ []string, argv func(i int) []string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(berthBin, argv(i)...)
			cmd.Env = environ
			if out, err := cmd.CombinedOutput(); err != nil {
				errs[i] = fmt.Sprintf("%v: %v\n%s", argv(i), err, out)
			}
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != "" {
			t.Error(e)
		}
	}
}

func TestBerthLockParallelInit(t *testing.T) {
	state, _, environ := lockFixture(t)
	const n = 8
	parallel(t, environ, func(i int) []string { return []string{"--home", state, "init", fmt.Sprintf("t-lock%d", i)} }, n)
	seen := map[string]string{}
	for i := range n {
		b, err := os.ReadFile(filepath.Join(state, "orgs", fmt.Sprintf("t-lock%d", i), "org.env"))
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "SSH_PORT=") || strings.HasPrefix(l, "TTYD_PORT=") {
				if other, dup := seen[l]; dup {
					t.Errorf("t-lock%d and %s both got %s", i, other, l)
				}
				seen[l] = fmt.Sprintf("t-lock%d", i)
			}
		}
	}
	if len(seen) != 2*n {
		t.Errorf("got %d distinct ports, want %d", len(seen), 2*n)
	}
}

func TestBerthLockParallelFwAllow(t *testing.T) {
	state, _, environ := lockFixture(t)
	const n = 10
	parallel(t, environ, func(i int) []string {
		return []string{"--home", state, "fw", "globex", "allow", fmt.Sprintf("h%d.example.com", i)}
	}, n)
	b, err := os.ReadFile(filepath.Join(state, "orgs", "globex", "config", "firewall.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if !strings.Contains(string(b), fmt.Sprintf("\nh%d.example.com\n", i)) {
			t.Errorf("h%d.example.com was lost:\n%s", i, b)
		}
	}
	// The lock lives in berth's own, git-ignored directory.
	if _, err := os.Stat(filepath.Join(state, "berth", ".lock")); err != nil {
		t.Errorf("no lock file: %v", err)
	}
	if gi, err := os.ReadFile(filepath.Join(state, "berth", ".gitignore")); err != nil || !strings.Contains(string(gi), "\n*\n") {
		t.Errorf(".gitignore next to the lock: %q, %v", gi, err)
	}
}
