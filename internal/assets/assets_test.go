package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/ar4mirez/berth/internal/host/local"
)

// TestModesMatchGit: the executable table is what git records for image/ (100755 vs 100644).
func TestModesMatchGit(t *testing.T) {
	out, err := exec.Command("git", "-C", "../..", "ls-files", "-s", "image").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	fs, err := files()
	if err != nil {
		t.Fatal(err)
	}
	embedded := map[string]bool{}
	for _, f := range fs {
		embedded[f.path] = true
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l) // mode hash stage path
		if len(f) != 4 {
			continue
		}
		if !embedded[f[3]] {
			t.Errorf("%s is in git but not embedded", f[3])
		}
		if exe := f[0] == "100755"; exe != executable[f[3]] {
			t.Errorf("%s: git mode %s, executable table says %v", f[3], f[0], executable[f[3]])
		}
	}
}

func ino(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

func TestMaterialize(t *testing.T) {
	root := t.TempDir()
	fsys := local.FS{}
	if Materialized(fsys, root) {
		t.Fatal("nothing written yet")
	}
	set, err := Materialize(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^berth/claude-env:[0-9a-f]{12}$`).MatchString(set.Tag) ||
		set.Compose != filepath.Join(root, "berth", "compose.yml") || set.ImageDir != filepath.Join(root, "berth", "image") {
		t.Errorf("set = %+v", set)
	}
	if !Materialized(fsys, root) {
		t.Fatal("not materialized after Materialize")
	}
	for p, mode := range map[string]os.FileMode{"compose.yml": 0o644, "image/Dockerfile": 0o644, "image/entrypoint.sh": 0o755} {
		fi, err := os.Stat(filepath.Join(root, "berth", p))
		if err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v; want mode %o", p, fi, err, mode)
		}
	}

	// Idempotent: a second call rewrites nothing.
	stamp := filepath.Join(root, "berth", stampFile)
	before := ino(t, stamp)
	if again, err := Materialize(fsys, root); err != nil || again != set {
		t.Fatalf("second Materialize: %+v, %v", again, err)
	}
	if ino(t, stamp) != before {
		t.Error("the stamp was rewritten although nothing changed")
	}

	// A stale file and a changed one are fixed when the stamp doesn't match.
	stale := filepath.Join(root, "berth", "image", "old-script.sh")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "berth", "image", "Dockerfile"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stamp, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(fsys, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale image file kept")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "berth", "image", "Dockerfile")); string(b) == "tampered" {
		t.Error("changed file not restored")
	}
}

func TestMaterializeRefusesForeignDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "berth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "berth", "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(local.FS{}, root); err == nil || !strings.Contains(err.Error(), "isn't berth's") {
		t.Fatalf("got %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "berth")); len(entries) != 1 {
		t.Errorf("foreign dir changed: %v", entries)
	}
	// An empty dir is fine.
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "berth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(local.FS{}, empty); err != nil {
		t.Errorf("empty berth dir: %v", err)
	}
}

func TestOverrideAndTagStability(t *testing.T) {
	a, _ := Embedded("/srv/a")
	b, _ := Embedded("/srv/b")
	if a.Tag != b.Tag {
		t.Error("the tag must depend on the content only, not on where it's written")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o1, err := Override(local.FS{}, a, dir)
	if err != nil || o1.ImageDir != dir || o1.Tag == a.Tag || o1.Compose != a.Compose {
		t.Fatalf("override: %+v, %v", o1, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o2, _ := Override(local.FS{}, a, dir); o2.Tag == o1.Tag {
		t.Error("changing the override dir must change the tag")
	}
	if _, err := Override(local.FS{}, a, filepath.Join(dir, "missing")); err == nil {
		t.Error("missing override dir accepted")
	}
}
