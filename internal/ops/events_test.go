package ops

import (
	"fmt"
	"testing"
	"time"
)

// TestProgress: start, steps, one output event per line (a partial line waits), and done or failed.
func TestProgress(t *testing.T) {
	var got []Event
	p := NewProgress(func(e Event) { got = append(got, e) }, "up", "acme")
	p.now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	out, errw := p.Writer("stdout"), p.Writer("stderr")
	p.Step("container", "recreating the container")
	fmt.Fprint(out, "Container claude-acme  Creat")
	fmt.Fprint(out, "ed\r\nContainer claude-acme  Started\npartial")
	fmt.Fprintln(errw, "warning: slow")
	p.Step("ready", "waiting for the container")
	code := p.Done(fail(KindNotRunning, "berth up acme", "claude-acme is not running"))

	want := []string{
		"start||",
		"step|container|recreating the container",
		"output|stdout|Container claude-acme  Created",
		"output|stdout|Container claude-acme  Started",
		"output|stderr|warning: slow",
		"output|stdout|partial",
		"step|ready|waiting for the container",
		"failed||claude-acme is not running",
	}
	if len(got) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(got), len(want), got)
	}
	for i, e := range got {
		mid := e.Step
		if e.Type == EventOutput {
			mid = e.Stream
		}
		if s := e.Type + "|" + mid + "|" + e.Message; s != want[i] {
			t.Errorf("event %d: %s, want %s", i, s, want[i])
		}
		if e.Schema != "berth.event/v1" || e.Op != "up" || e.Org != "acme" || (i > 0 && e.Time != "2026-10-06T12:00:00Z") {
			t.Errorf("event %d: %+v", i, e)
		}
	}
	last := got[len(got)-1]
	if code != 1 || last.Error == nil || last.Error.Kind != KindNotRunning || last.Error.Hint != "berth up acme" {
		t.Errorf("failed: code %d, %+v", code, last.Error)
	}

	// Done with no error, and a nil Progress (nobody listens).
	got = nil
	if code := NewProgress(func(e Event) { got = append(got, e) }, "pull", "").Done(nil); code != 0 || len(got) != 2 || got[1].Type != EventDone || got[1].Error != nil {
		t.Errorf("done: code %d, %+v", code, got)
	}
	var none *Progress
	none.Step("x", "y")
}
