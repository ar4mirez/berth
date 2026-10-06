package parity

import (
	"encoding/json"
	"strings"
	"testing"
)

// event is a berth.event/v1 line, as the tests read it.
type event struct {
	Schema, Time, Op, Org, Type, Step, Stream, Message string
	Error                                              *struct {
		Schema  string
		Kind    string
		Code    int
		Message string
		Hint    string
	}
}

// events runs a long operation under --output json and decodes its stdout: one event per line,
// and nothing else anywhere.
func events(t *testing.T, sc Scenario) ([]event, Result) {
	t.Helper()
	sc.Args = append([]string{"--output", "json"}, sc.Args...)
	r := run(t, Berth(berthBin), sc)
	if r.Stderr != "" {
		t.Errorf("%v: stderr %q (everything is an event on stdout)", sc.Args, r.Stderr)
	}
	var evs []event
	for _, l := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
		var e event
		dec := json.NewDecoder(strings.NewReader(l))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("%v: not an event: %q (%v)", sc.Args, l, err)
		}
		if e.Schema != "berth.event/v1" || e.Time == "" || e.Op == "" {
			t.Errorf("%v: %+v", sc.Args, e)
		}
		evs = append(evs, e)
	}
	return evs, r
}

// shape is the events without their times: type, then the step or stream, then the message.
func shape(evs []event) []string {
	var out []string
	for _, e := range evs {
		mid := e.Step
		if e.Type == "output" {
			mid = e.Stream
		}
		out = append(out, e.Type+"|"+mid+"|"+e.Message)
	}
	return out
}

// TestBerthProgressEvents: under --output json a long operation reports its progress as events
// (#54, docs/json.md): start, its steps, each line it or its commands print, and done or failed
// with the typed error. The text output of the same command is ccenv's (the parity scenarios).
func TestBerthProgressEvents(t *testing.T) {
	// up: the steps, compose's output and the container's last log lines, then done.
	evs, r := events(t, Scenario{Args: []string{"up", "acme"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `^compose .* up -d`, Stdout: " Container claude-acme  Started\n", Stderr: "time=now level=warning msg=slow\n"},
		Rule{Bin: "docker", Match: `^logs claude-acme$`, Stdout: "firewall: ON (12 allowlisted networks)\nclaude-env[acme]: ready\n"})})
	got := strings.Join(shape(evs), "\n")
	for _, want := range []string{
		"start||\nstep|image|getting the image\n",
		"step|container|recreating claude-acme\n",
		"output|stdout| Container claude-acme  Started",
		"output|stderr|time=now level=warning msg=slow",
		"step|ready|waiting for the container to start\noutput|stdout|firewall: ON (12 allowlisted networks)\noutput|stdout|claude-env[acme]: ready\ndone||",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("up: events lack %q:\n%s", want, got)
		}
	}
	if r.Exit != 0 || evs[0].Op != "up" || evs[0].Org != "acme" {
		t.Errorf("up: exit %d, first event %+v", r.Exit, evs[0])
	}

	// A failure is the last event, with the typed error and the command's exit code.
	evs, r = events(t, Scenario{Args: []string{"restart", "acme"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `^compose .* up -d`, Stderr: "no space left on device\n", Exit: 17})})
	last := evs[len(evs)-1]
	if r.Exit != 17 || last.Type != "failed" || last.Error == nil || last.Error.Kind != "command" || last.Error.Code != 17 {
		t.Errorf("restart, compose fails: exit %d, last %+v %+v\n%s", r.Exit, last, last.Error, strings.Join(shape(evs), "\n"))
	}
	if !strings.Contains(strings.Join(shape(evs), "\n"), "output|stderr|no space left on device") {
		t.Errorf("restart: compose's stderr isn't an event:\n%s", strings.Join(shape(evs), "\n"))
	}
	evs, r = events(t, Scenario{Args: []string{"up", "nope"}, Files: twoOrgs})
	if last = evs[len(evs)-1]; r.Exit != 1 || len(evs) != 2 || last.Type != "failed" || last.Error.Kind != "not-found" || last.Error.Hint != "run: <tool> init nope" {
		t.Errorf("up, unknown org: exit %d\n%s", r.Exit, strings.Join(shape(evs), "\n"))
	}

	// A stream: each log line is an event.
	evs, r = events(t, Scenario{Args: []string{"logs", "acme"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `logs -f$`, Stdout: "claude-acme  | one\nclaude-acme  | two\n"})})
	if got := strings.Join(shape(evs), "\n"); r.Exit != 0 || got != "start||\noutput|stdout|claude-acme  | one\noutput|stdout|claude-acme  | two\ndone||" {
		t.Errorf("logs: exit %d\n%s", r.Exit, got)
	}
	evs, _ = events(t, Scenario{Args: []string{"remote", "acme", "logs"}, Files: twoOrgs, Rules: running("acme",
		Rule{Bin: "docker", Match: `sh -c sed `, Stdout: "=== starting remote-control\n"})})
	if got := strings.Join(shape(evs), "\n"); got != "start||\noutput|stdout|=== starting remote-control\ndone||" || evs[0].Op != "remote logs" || evs[0].Org != "acme" {
		t.Errorf("remote logs: %+v\n%s", evs[0], got)
	}

	// backup: a step per org. The backup itself can't share stdout with the events.
	evs, r = events(t, Scenario{Args: []string{"backup", "--all", "--no-encrypt"}, Files: twoOrgs})
	if got := strings.Join(shape(evs), "\n"); r.Exit != 0 || !strings.Contains(got, "step|org|acme\n") || !strings.Contains(got, "step|org|globex\n") || !strings.HasSuffix(got, "done||") {
		t.Errorf("backup: exit %d\n%s", r.Exit, got)
	}
	evs, r = events(t, Scenario{Args: []string{"backup", "acme", "--no-encrypt", "-o", "-"}, Files: twoOrgs})
	if last = evs[len(evs)-1]; r.Exit != 1 || last.Type != "failed" || !strings.Contains(last.Message, "can't be combined with --output json") {
		t.Errorf("backup -o -: exit %d\n%s", r.Exit, strings.Join(shape(evs), "\n"))
	}
}
