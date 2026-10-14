package ops

import (
	"errors"
	"testing"
)

// The audit's failure for an org that has never started names its cause, which is how ListRepos
// tells it from any other: the command keeps ccenv's exit 2, a list has nothing to refuse.
func TestNeverStartedIsNamed(t *testing.T) {
	e := quiet(KindState, 2, "berth up acme", "acme has no quarantine folder yet: it has never started")
	if errors.Is(e, ErrNeverStarted) {
		t.Error("an error with no cause counts as never started")
	}
	e.Err = ErrNeverStarted
	if !errors.Is(e, ErrNeverStarted) || e.ExitCode() != 2 || !e.Silent() {
		t.Errorf("the named error: %v, exit %d, silent %v", e, e.ExitCode(), e.Silent())
	}
}
