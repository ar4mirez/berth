package ops

import (
	"strings"
	"testing"
)

// TestCheckMount: what a socket mount may be, before the host is asked (#216). The rules are the
// API's (docs/api.md, socket mounts): a name of [a-z0-9-]{1,32}, absolute paths, a target in a
// directory of its own under /run/berth-mounts/ with the socket's own name, mode rw, and no two
// mounts in one directory or one inside the other.
func TestCheckMount(t *testing.T) {
	ok := Mount{Name: "agent", Source: "/run/agent/agent.sock", Target: "/run/berth-mounts/agent/agent.sock", Mode: "rw"}
	with := func(f func(*Mount)) Mount { m := ok; f(&m); return m }
	other := Mount{Name: "db", Source: "/srv/db/db.sock", Target: "/run/berth-mounts/db/db.sock", Mode: "rw"}
	tests := []struct {
		name   string
		m      Mount
		others []Mount
		want   string // "" when it is allowed
	}{
		{"a socket in a directory of its own", ok, []Mount{other}, ""},
		{"deeper in the mounts", with(func(m *Mount) { m.Target = "/run/berth-mounts/a/b/agent.sock" }), nil, ""},
		{"setting it again", ok, []Mount{ok}, ""},
		{"a capital in the name", with(func(m *Mount) { m.Name = "Agent" }), nil, "lowercase letters"},
		{"a name too long", with(func(m *Mount) { m.Name = strings.Repeat("a", 33) }), nil, "up to 32"},
		{"a relative source", with(func(m *Mount) { m.Source = "agent.sock" }), nil, "an absolute path"},
		{"a source with ..", with(func(m *Mount) { m.Source = "/run/agent/../agent/agent.sock" }), nil, "written plainly"},
		{"a colon", with(func(m *Mount) { m.Source = "/run/a:b/agent.sock" }), nil, "no colon"},
		{"read-only", with(func(m *Mount) { m.Mode = "ro" }), nil, "mounted rw"},
		{"outside the mounts", with(func(m *Mount) { m.Target = "/home/node/agent.sock" }), nil, "under /run/berth-mounts/"},
		{"a look-alike prefix", with(func(m *Mount) { m.Target = "/run/berth-mountsx/a/agent.sock" }), nil, "under /run/berth-mounts/"},
		{"the mounts' own directory", with(func(m *Mount) { m.Target = "/run/berth-mounts/agent.sock" }), nil, "a directory of its own"},
		{"a different name inside", with(func(m *Mount) { m.Target = "/run/berth-mounts/agent/a.sock" }), nil, "keeps its name"},
		{"a socket in /", with(func(m *Mount) { m.Source = "/agent.sock" }), nil, "not in /"},
		{"another's directory", with(func(m *Mount) { m.Target = "/run/berth-mounts/db/agent.sock" }), []Mount{other}, "db already uses /run/berth-mounts/db"},
		{"inside another's", with(func(m *Mount) { m.Target = "/run/berth-mounts/db/x/agent.sock" }), []Mount{other}, "db already uses"},
		{"around another's", with(func(m *Mount) { m.Target = "/run/berth-mounts/db/agent.sock" }),
			[]Mount{{Name: "deep", Target: "/run/berth-mounts/db/x/y.sock"}}, "deep already uses"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckMount(tt.m, tt.others)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("got %v, want an error with %q", err, tt.want)
			case err != nil && AsError(err).Kind != KindUsage:
				t.Errorf("kind %s, want usage (400)", AsError(err).Kind)
			}
		})
	}
}

// TestEncodeMounts: the saved list is in name order, and never says whether a mount is applied,
// which only the running container can.
func TestEncodeMounts(t *testing.T) {
	b := EncodeMounts([]Mount{{Name: "b", Source: "/s/b/b.sock", Target: "/run/berth-mounts/b/b.sock", Mode: "rw", Applied: true}, {Name: "a", Source: "/s/a/a.sock", Target: "/run/berth-mounts/a/a.sock", Mode: "rw"}})
	if s := string(b); strings.Contains(s, "applied") || strings.Index(s, `"a"`) > strings.Index(s, `"b"`) {
		t.Errorf("saved:\n%s", s)
	}
}
