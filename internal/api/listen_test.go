package api

import (
	"io/fs"
	"testing"
	"time"
)

type sockInfo struct{ mode fs.FileMode }

func (s sockInfo) Name() string       { return "berth.sock" }
func (s sockInfo) Size() int64        { return 0 }
func (s sockInfo) Mode() fs.FileMode  { return s.mode }
func (s sockInfo) ModTime() time.Time { return time.Time{} }
func (s sockInfo) IsDir() bool        { return false }
func (s sockInfo) Sys() any           { return nil }

// TestClientSocketPath: a client looks where berth serve listens, and in berth's config directory
// when nothing is there, as a system service's socket is (#63). BERTH_SOCKET is taken as given.
func TestClientSocketPath(t *testing.T) {
	env := map[string]string{"HOME": "/home/ops", "XDG_RUNTIME_DIR": "/run/user/1000"}
	getenv := func(k string) string { return env[k] }
	at := func(socks map[string]fs.FileMode) func(string) (fs.FileInfo, error) {
		return func(p string) (fs.FileInfo, error) {
			if m, ok := socks[p]; ok {
				return sockInfo{m}, nil
			}
			return nil, fs.ErrNotExist
		}
	}
	const runtime, config = "/run/user/1000/berth.sock", "/home/ops/.config/berth/berth.sock"
	for _, c := range []struct {
		name  string
		socks map[string]fs.FileMode
		want  string
	}{
		{"neither", nil, runtime},
		{"the runtime one", map[string]fs.FileMode{runtime: fs.ModeSocket}, runtime},
		{"both", map[string]fs.FileMode{runtime: fs.ModeSocket, config: fs.ModeSocket}, runtime},
		{"the config one", map[string]fs.FileMode{config: fs.ModeSocket}, config},
		{"a file, not a socket", map[string]fs.FileMode{config: 0}, runtime},
	} {
		if got := ClientSocketPath(getenv, at(c.socks)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	env["BERTH_SOCKET"] = "/srv/berth.sock"
	if got := ClientSocketPath(getenv, at(map[string]fs.FileMode{config: fs.ModeSocket})); got != "/srv/berth.sock" {
		t.Errorf("BERTH_SOCKET: %s", got)
	}
}
