package image

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// procState is what berth-proc prints for one process (berth.process/v1 without schema and org).
type procState struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`
	PID       *int     `json:"pid"`
	StartedAt *string  `json:"started_at"`
	ExitCode  *int     `json:"exit_code"`
	Restarts  int      `json:"restarts"`
	Argv      []string `json:"argv"`
	EnvNames  []string `json:"env_names"`
}

// TestBerthProc: image/berth-proc, the supervisor of an org's processes (#216), run as it runs in
// the container, by a user that isn't root. A process starts with its variables, in its directory;
// its state is read back with its variables by name only; a second of the same name is refused; a
// failure is started again with a growing wait, and `never` isn't; a stop ends its whole process
// group, SIGKILL after its grace when SIGTERM isn't enough, and forgets it.
func TestBerthProc(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getuid() == 0 {
		t.Skip("berth-proc runs in the Linux image, as the container's user")
	}
	for _, tool := range []string{"bash", "jq", "setsid", "tail"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join(dir, "berth-proc"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	work := t.TempDir()
	logMax := "10000000"
	run := func(stdin string, args ...string) (string, string, int) {
		t.Helper()
		c := exec.Command(script, args...)
		c.Env = append(os.Environ(), "BERTH_PROC_DIR="+state, "BERTH_PROC_BACKOFF_MAX=2", "BERTH_PROC_LOG_MAX="+logMax)
		c.Stdin = strings.NewReader(stdin)
		var out, errb strings.Builder
		c.Stdout, c.Stderr = &out, &errb
		err := c.Run()
		code := 0
		var e *exec.ExitError
		if errors.As(err, &e) {
			code = e.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), errb.String(), code
	}
	status := func(name string) procState {
		t.Helper()
		out, errs, code := run("", "status", name)
		var p procState
		if code != 0 || json.Unmarshal([]byte(out), &p) != nil {
			t.Fatalf("status %s: %d %q %q", name, code, out, errs)
		}
		return p
	}
	waitFor := func(name string, ok func(procState) bool) procState {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); ; {
			p := status(name)
			if ok(p) {
				return p
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never got there: %+v", name, p)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Cleanup(func() {
		for _, n := range []string{"web", "flaky", "once", "stubborn", "chatty", "stale", "leaver"} {
			_, _, _ = run("", "stop", n)
		}
	})

	// Started with its variables and directory; shown with their names, never their values.
	spec := `{"argv":["sh","-c","echo \"$GREETING from $(pwd)\"; exec sleep 60"],"cwd":"` + work + `","env":{"GREETING":"s3cret-hello"},"stop_grace_seconds":5}`
	out, errs, code := run(spec, "start", "web")
	if code != 0 || strings.Contains(out, "s3cret-hello") || strings.Contains(errs, "s3cret-hello") {
		t.Fatalf("start: %d %q %q", code, out, errs)
	}
	p := waitFor("web", func(p procState) bool { return p.State == "running" && p.PID != nil })
	if p.StartedAt == nil || p.Restarts != 0 || len(p.EnvNames) != 1 || p.EnvNames[0] != "GREETING" || p.Argv[0] != "sh" {
		t.Errorf("running: %+v", p)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if logs, _, _ := run("", "logs", "web"); strings.Contains(logs, "s3cret-hello from "+work) {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("logs: %q", logs)
		}
	}
	if b, err := os.ReadFile(filepath.Join(state, "web", "state.json")); err != nil || strings.Contains(string(b), "s3cret-hello") {
		t.Errorf("state.json has the value, or isn't there: %v %s", err, b)
	}
	if fi, err := os.Stat(filepath.Join(state, "web")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the process's directory: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(state, "web", "spec.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("spec.json, which has the values: %v %v", fi, err)
	}

	// The same name again, and specs it doesn't take.
	if _, errs, code := run(`{"argv":["true"]}`, "start", "web"); code != 3 || !strings.Contains(errs, "exists") {
		t.Errorf("a second web: %d %q", code, errs)
	}
	for name, bad := range map[string]string{
		"a shell string":   `{"argv":"sleep 60"}`,
		"no command":       `{"argv":[]}`,
		"a bad var name":   `{"argv":["true"],"env":{"lower":"x"}}`,
		"a restart":        `{"argv":["true"],"restart":"always"}`,
		"a grace too long": `{"argv":["true"],"stop_grace_seconds":301}`,
		"a relative cwd":   `{"argv":["true"],"cwd":"tmp"}`,
		"an option argv0":  `{"argv":["-i","true"]}`,
		"a NAME=value one": `{"argv":["FOO=bar","true"]}`,
	} {
		if _, errs, code := run(bad, "start", "bad"); code != 2 {
			t.Errorf("%s: %d %q", name, code, errs)
		}
	}
	if _, _, code := run(`{"argv":["true"]}`, "start", "Bad_Name"); code != 2 {
		t.Errorf("a bad name: %d", code)
	}
	if _, _, code := run("", "status", "nope"); code != 4 {
		t.Errorf("status of no such process: %d", code)
	}

	// on-failure starts it again, waiting longer each time; never leaves it exited.
	run(`{"argv":["sh","-c","exit 3"],"cwd":"`+work+`"}`, "start", "flaky")
	p = waitFor("flaky", func(p procState) bool { return p.Restarts >= 2 })
	if p.ExitCode == nil || *p.ExitCode != 3 {
		t.Errorf("flaky: %+v", p)
	}
	run(`{"argv":["sh","-c","exit 3"],"cwd":"`+work+`","restart":"never"}`, "start", "once")
	p = waitFor("once", func(p procState) bool { return p.State == "exited" })
	if p.ExitCode == nil || *p.ExitCode != 3 || p.Restarts != 0 || p.PID != nil {
		t.Errorf("once: %+v", p)
	}

	// A stop: SIGTERM to the whole group (a child of the process too), then forgotten.
	run(`{"argv":["sh","-c","sleep 61 & wait"],"cwd":"`+work+`"}`, "start", "family")
	fam := waitFor("family", func(p procState) bool { return p.State == "running" && p.PID != nil })
	out, _, code = run("", "stop", "family")
	if code != 0 || !strings.Contains(out, `"exited"`) {
		t.Errorf("stop: %d %q", code, out)
	}
	if syscall.Kill(-*fam.PID, 0) == nil {
		t.Error("the process group outlived the stop")
	}
	if _, _, code := run("", "status", "family"); code != 4 {
		t.Errorf("a stopped process is forgotten: %d", code)
	}
	// One that ignores SIGTERM is killed after its grace.
	run(`{"argv":["sh","-c","trap '' TERM; while :; do sleep 0.2; done"],"cwd":"`+work+`","stop_grace_seconds":1}`, "start", "stubborn")
	stub := waitFor("stubborn", func(p procState) bool { return p.State == "running" && p.PID != nil })
	start := time.Now()
	out, _, _ = run("", "stop", "stubborn")
	if took := time.Since(start); took < time.Second || took > 10*time.Second || !strings.Contains(out, `"exit_code": 137`) {
		t.Errorf("stubborn: stopped in %v: %q", took, out)
	}
	if syscall.Kill(-*stub.PID, 0) == nil {
		t.Error("the stubborn group outlived its SIGKILL")
	}
	// The log is capped while the process runs: at the cap, log becomes log.1 and a new log starts.
	logMax = "4096"
	// Lines, then output with no newline at all (a progress bar's), NULs in it too.
	run(`{"argv":["sh","-c","i=0; while [ $i -lt 3000 ]; do echo line-$i; i=$((i+1)); done; head -c 9000 /dev/zero | tr '\\0' x; printf 'nul\\0byte last-bytes'; exec sleep 60"],"cwd":"`+work+`"}`, "start", "chatty")
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if b, _ := os.ReadFile(filepath.Join(state, "chatty", "log")); strings.HasSuffix(string(b), "xnul\x00byte last-bytes") {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("chatty's log: %q", b)
		}
	}
	for _, f := range []string{"log", "log.1"} {
		if fi, err := os.Stat(filepath.Join(state, "chatty", f)); err != nil || fi.Size() > 4096 || fi.Size() == 0 {
			t.Errorf("chatty's %s, past its cap: %v %v", f, fi, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(state, "chatty", "log.1")); strings.Contains(string(b), "last-bytes") {
		t.Errorf("the newest output is in log.1: %q", b)
	}
	run("", "stop", "chatty")
	logMax = "10000000"

	// What the process leaves in its group when it ends is ended too: before it starts again, and
	// at a stop.
	pids := filepath.Join(work, "leaver.pids")
	run(`{"argv":["bash","-c","sleep 60 & echo $! >> `+pids+`; exit 1"],"cwd":"`+work+`","stop_grace_seconds":2}`, "start", "leaver")
	waitFor("leaver", func(p procState) bool { return p.Restarts >= 2 })
	gone := func(pid string) bool {
		n, err := strconv.Atoi(pid)
		if err != nil {
			t.Fatalf("a pid: %q", pid)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if b, err := os.ReadFile("/proc/" + pid + "/stat"); err != nil || strings.Contains(string(b), ") Z ") {
				return true
			}
		}
		return syscall.Kill(n, 0) != nil
	}
	b, _ := os.ReadFile(pids)
	if first := strings.Fields(string(b)); len(first) == 0 || !gone(first[0]) {
		t.Errorf("a restart left the last run's sleep running: %q", b)
	}
	run("", "stop", "leaver")
	b, _ = os.ReadFile(pids)
	for _, pid := range strings.Fields(string(b)) {
		if !gone(pid) {
			t.Errorf("a stop left sleep %s running", pid)
		}
	}

	// A supervisor.pid that outlived its supervisor (a pid reused since) is never signalled.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	sd := filepath.Join(state, "stale")
	if err := os.Mkdir(sd, 0o700); err != nil {
		t.Fatal(err)
	}
	for f, b := range map[string]string{"spec.json": `{"argv":["true"],"stop_grace_seconds":1}`, "state.json": `{"name":"stale","state":"running"}`,
		"supervisor.pid": strconv.Itoa(other.Process.Pid) + "\n"} {
		if err := os.WriteFile(filepath.Join(sd, f), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, errs, code := run("", "stop", "stale"); code != 0 || syscall.Kill(other.Process.Pid, 0) != nil {
		t.Errorf("stop of a stale process: %d %q; the unrelated process lives: %v", code, errs, syscall.Kill(other.Process.Pid, 0) == nil)
	}
	if _, err := os.Stat(sd); !os.IsNotExist(err) {
		t.Errorf("a stale process is forgotten: %v", err)
	}

	// Every one, as a list.
	out, _, _ = run("", "status")
	var all []procState
	if json.Unmarshal([]byte(out), &all) != nil || len(all) != 3 || all[0].Name != "flaky" || all[2].Name != "web" {
		t.Errorf("status of all: %q", out)
	}
}
