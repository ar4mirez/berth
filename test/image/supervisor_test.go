package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeClaude is `claude remote-control` as the supervisor sees it: one serves the folder at a time
// (the pid in FAKE_LOCK, as bridge-pointer.json has it), a second fails as Claude Code does, and
// SIGTERM makes it exit after FAKE_STOP, or never with FAKE_IGNORE_TERM.
const fakeClaude = `package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	lock := os.Getenv("FAKE_LOCK")
	if b, err := os.ReadFile(lock); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 && syscall.Kill(pid, 0) == nil {
			fmt.Println("Error: This folder is already served by a terminal ` + "`claude remote-control`" + ` on this device. Stop it first.")
			os.Exit(1)
		}
	}
	_ = os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o644)
	fmt.Println("Connected")
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	<-term
	if os.Getenv("FAKE_IGNORE_TERM") != "" {
		select {}
	}
	stop, _ := time.ParseDuration(os.Getenv("FAKE_STOP"))
	time.Sleep(stop)
	_ = os.Remove(lock)
}
`

// fakeRunuser is runuser as the supervisor uses it, killed as an older berth killed it (pkill -f
// "claude remote-control" matches its command line too): it passes SIGTERM on and returns at once,
// while the Claude it ran is still exiting.
const fakeRunuser = `#!/usr/bin/env bash
while [ "$1" != "--" ]; do shift; done; shift
"$@" & child=$!
trap 'kill -TERM $child; exit 0' TERM
wait $child
`

// TestSupervisorStartsOne: a restart, asked for the way berth does it or the way an older berth
// did (killing runuser too, then the retry at once), starts exactly one Remote Control, after the
// previous one has exited: never a second beside it that fails with "already served" (#187). One
// that ignores SIGTERM is killed after RC_STOP_WAIT.
func TestSupervisorStartsOne(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("rc-supervisor.sh runs in the Linux image (stat -c, date -Is)")
	}
	for _, tool := range []string{"bash", "pgrep", "pkill", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	bin := t.TempDir()
	// A name of its own: the supervisor's pgrep and pkill, and this test's, never match a real
	// Remote Control running on this machine.
	name := fmt.Sprintf("rcfake%d", os.Getpid())
	src := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(src, []byte(fakeClaude), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, name), src).CombinedOutput(); err != nil {
		t.Fatalf("building the fake claude: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(bin, "runuser"), []byte(fakeRunuser), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(dir, "rc-supervisor.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, kill string
		env        []string
		want       string
	}{
		{"berth stops claude only", "^" + name + " remote-control", []string{"FAKE_STOP=3s"}, ""},
		{"an older berth kills runuser too", name + " remote-control", []string{"FAKE_STOP=3s"}, "waiting for the previous remote-control to exit"},
		{"it ignores SIGTERM", name + " remote-control", []string{"FAKE_IGNORE_TERM=1", "RC_STOP_WAIT=2"}, "didn't exit in 2s, killing it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, work := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			logf, retry := filepath.Join(cfg, "remote-control.log"), filepath.Join(cfg, "rc-retry")
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			sup := exec.CommandContext(ctx, "bash", script)
			sup.Env = append(os.Environ(), append([]string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"ORG=t-rc", "CFG=" + cfg, "RC_LOG=" + logf, "RC_RETRY=" + retry, "RC_DIR=" + work, "RC_CLAUDE=" + name,
				"FAKE_LOCK=" + filepath.Join(work, "lock"),
			}, tc.env...)...)
			sup.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := sup.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = syscall.Kill(-sup.Process.Pid, syscall.SIGKILL)
				_ = exec.Command("pkill", "-KILL", "-f", "^"+name+" remote-control").Run()
				_ = sup.Wait()
			}()
			logged := func() string { b, _ := os.ReadFile(logf); return string(b) }
			waitFor := func(what string, n int) {
				t.Helper()
				for deadline := time.Now().Add(30 * time.Second); strings.Count(logged(), what) < n; {
					if time.Now().After(deadline) {
						t.Fatalf("no %d× %q in the log:\n%s", n, what, logged())
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			waitFor("Connected", 1)

			// The restart, as berth asks for it: the kill, then the retry.
			_ = exec.Command("pkill", "-f", tc.kill).Run()
			if err := os.WriteFile(retry, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			waitFor("Connected", 2)
			time.Sleep(3 * time.Second) // time for a second start, if there were one

			got := logged()
			if strings.Contains(got, "already served") {
				t.Errorf("a second Remote Control started beside the first:\n%s", got)
			}
			if n := strings.Count(got, "starting remote-control"); n != 2 {
				t.Errorf("%d starts, want 2:\n%s", n, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("the log lacks %q:\n%s", tc.want, got)
			}
		})
	}
}
