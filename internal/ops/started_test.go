package ops

import (
	"context"
	"errors"
	"testing"
)

// inspectOut is a System whose `docker inspect` prints out, or fails when err is set.
type inspectOut struct {
	System
	out string
	err error
}

func (s inspectOut) Capture(context.Context, bool, ...string) (string, error) { return s.out, s.err }

func TestStartedAtEngineLayouts(t *testing.T) {
	for _, c := range []struct {
		name, out string
		err       error
		want      string
	}{
		{"docker", "2026-01-02T03:04:05.123456789Z", nil, "2026-01-02T03:04:05.123456789Z"},
		{"docker offset", "2026-01-02T05:04:05+02:00", nil, "2026-01-02T03:04:05Z"},
		{"podman", "2026-01-02 03:04:05.123456789 +0000 UTC", nil, "2026-01-02T03:04:05.123456789Z"},
		{"podman offset", "2026-01-02 05:04:05.5 +0200 CEST", nil, "2026-01-02T03:04:05.5Z"},
		{"never started", "0001-01-01T00:00:00Z", nil, ""},
		{"podman never started", "0001-01-01 00:00:00 +0000 UTC", nil, ""},
		{"not a time", "running", nil, ""},
		{"engine can't say", "", errors.New("unsupported"), ""},
	} {
		got := StartedAt(context.Background(), inspectOut{out: c.out, err: c.err}, "t-acme")
		if c.want == "" {
			if got != nil {
				t.Errorf("%s: %q, want null", c.name, *got)
			}
			continue
		}
		if got == nil || *got != c.want {
			t.Errorf("%s: %v, want %q", c.name, got, c.want)
		}
	}
}
