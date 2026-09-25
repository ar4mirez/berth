// Package scripts tests the host-side scripts in scripts/.
package scripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkout makes a fake legacy checkout: a ccenv file, and copies of this repo's image/ and
// compose.yml.
func checkout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ccenv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(dir, "image"), os.DirFS("../../image")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func drift(t *testing.T, legacy string) (string, int) {
	t.Helper()
	out, err := exec.Command("../../scripts/drift.sh", legacy).CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// TestDrift: drift.sh reports image/ and compose.yml drift both, even when image/ already differs
// (it used to stop after the first diff and hide compose.yml's), and its exit code says which.
func TestDrift(t *testing.T) {
	legacy := checkout(t)
	if out, code := drift(t, legacy); code != 0 || !strings.Contains(out, "drift: none") {
		t.Fatalf("identical checkout: exit %d\n%s", code, out)
	}

	appendTo(t, filepath.Join(legacy, "image", "entrypoint.sh"), "# legacy-only line\n")
	appendTo(t, filepath.Join(legacy, "compose.yml"), "# legacy-only compose line\n")
	out, code := drift(t, legacy)
	if code != 1 || !strings.Contains(out, "-# legacy-only line") || !strings.Contains(out, "-# legacy-only compose line") || strings.Contains(out, "drift: none") {
		t.Errorf("image/ and compose.yml drift: exit %d, want 1 and both diffs\n%s", code, out)
	}

	// Only compose.yml drifts.
	legacy = checkout(t)
	appendTo(t, filepath.Join(legacy, "compose.yml"), "# legacy-only compose line\n")
	if out, code := drift(t, legacy); code != 1 || !strings.Contains(out, "-# legacy-only compose line") {
		t.Errorf("compose.yml drift: exit %d\n%s", code, out)
	}

	// A diff that can't run is worse than drift: exit 2, and the other diff still runs.
	legacy = checkout(t)
	if err := os.Remove(filepath.Join(legacy, "compose.yml")); err != nil {
		t.Fatal(err)
	}
	appendTo(t, filepath.Join(legacy, "image", "entrypoint.sh"), "# legacy-only line\n")
	if out, code := drift(t, legacy); code != 2 || !strings.Contains(out, "-# legacy-only line") {
		t.Errorf("missing compose.yml: exit %d, want 2 with the image/ diff\n%s", code, out)
	}

	if out, code := drift(t, t.TempDir()); code != 2 || !strings.Contains(out, "drift: no legacy checkout at") {
		t.Errorf("not a checkout: exit %d\n%s", code, out)
	}
}
