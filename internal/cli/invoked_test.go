package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInvokedPathKeepsTheLink: the scheduled backup job runs berth as it was invoked, the
// ~/.local/bin/berth link, not the versioned binary it points to, so upgrades don't break it.
func TestInvokedPathKeepsTheLink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "opt", "0.0.1", "berth")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "berth")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	saved := os.Args[0]
	t.Cleanup(func() { os.Args[0] = saved })
	for _, arg0 := range []string{"berth", link} {
		os.Args[0] = arg0
		if got := invokedPath(); got != link {
			t.Errorf("invoked as %q: %q, want the link %s", arg0, got, link)
		}
	}
	os.Args[0] = "no-such-berth"
	if got := invokedPath(); got != "" {
		t.Errorf("not on PATH: %q, want \"\"", got)
	}
}
