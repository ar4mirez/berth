package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ar4mirez/berth/internal/docgen"
)

// TestCommandReferenceIsCurrent: docs/reference/commands is exactly what the commands' help
// generates (#56). When it fails: go run ./tools/gendocs, and commit the result.
func TestCommandReferenceIsCurrent(t *testing.T) {
	fresh := t.TempDir()
	if err := docgen.Markdown(NewRoot(), fresh); err != nil {
		t.Fatal(err)
	}
	committed := filepath.Join("..", "..", "docs", docgen.RefDir)
	list := func(dir string) []string {
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		return names
	}
	want, got := list(fresh), list(committed)
	if !slices.Equal(want, got) {
		t.Fatalf("docs/%s is stale (run: go run ./tools/gendocs)\ngenerated: %v\ncommitted: %v", docgen.RefDir, want, got)
	}
	for _, n := range want {
		a, _ := os.ReadFile(filepath.Join(fresh, n))
		b, _ := os.ReadFile(filepath.Join(committed, n))
		if string(a) != string(b) {
			t.Errorf("docs/%s/%s is stale (run: go run ./tools/gendocs)", docgen.RefDir, n)
		}
	}
}

// TestManPages: every command that has a page also gets a man page.
func TestManPages(t *testing.T) {
	dir := t.TempDir()
	if err := docgen.Man(NewRoot(), dir, "test"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"berth.1", "berth-up.1", "berth-org-up.1", "berth-host-add.1"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("no %s: %v", n, err)
		}
	}
}
