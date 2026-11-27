package image

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	run := func(stdin string, args ...string) (string, string, int) {
		t.Helper()
		c := exec.Command(script, args...)
		c.Env = append(os.Environ(), "BERTH_PROC_DIR="+state, "BERTH_PROC_BACKOFF_MAX=2")
		c.Stdin = strings.NewReader(stdin)
		var out, errb strings.Builder
		c.Stdout, c.Stderr = &out, &errb
		err := c.Run()
		code := 0
		if e, ok := err.(*exec.ExitError); ok {
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
		for _, n := range []string{"web", "flaky", "once", "stubborn"} {
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
	// Every one, as a list.
	out, _, _ = run("", "status")
	var all []procState
	if json.Unmarshal([]byte(out), &all) != nil || len(all) != 3 || all[0].Name != "flaky" || all[2].Name != "web" {
		t.Errorf("status of all: %q", out)
	}
}
