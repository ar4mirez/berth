package api

import (
	"io/fs"
	"path/filepath"
)

// SocketPath is where the API listens by default: $XDG_RUNTIME_DIR/berth.sock, which only its user
// can reach, or berth's own directory in the user's configuration.
func SocketPath(getenv func(string) string) string {
	if s := getenv("BERTH_SOCKET"); filepath.IsAbs(s) {
		return s // where `berth serve --socket` was told to listen, for its clients
	}
	if d := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		return filepath.Join(d, "berth.sock")
	}
	cfg := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		cfg = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(cfg, "berth", "berth.sock")
}

// ClientSocketPath is where a client finds the API (stat is the client machine's): SocketPath, unless nothing is there and berth's
// own directory has one. A system service (berth system service install --system) has no
// $XDG_RUNTIME_DIR, so it listens there (#63).
func ClientSocketPath(getenv func(string) string, stat func(string) (fs.FileInfo, error)) string {
	p := SocketPath(getenv)
	if _, err := stat(p); err == nil || getenv("BERTH_SOCKET") != "" {
		return p
	}
	cfg := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		cfg = filepath.Join(getenv("HOME"), ".config")
	}
	if alt := filepath.Join(cfg, "berth", "berth.sock"); alt != p {
		if fi, err := stat(alt); err == nil && fi.Mode()&fs.ModeSocket != 0 {
			return alt
		}
	}
	return p
}
