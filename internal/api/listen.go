package api

import "path/filepath"

// SocketPath is where the API listens by default: $XDG_RUNTIME_DIR/berth.sock, which only its user
// can reach, or berth's own directory in the user's configuration.
func SocketPath(getenv func(string) string) string {
	if d := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		return filepath.Join(d, "berth.sock")
	}
	cfg := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		cfg = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(cfg, "berth", "berth.sock")
}
