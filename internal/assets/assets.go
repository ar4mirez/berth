// Package assets writes berth's embedded image/ and compose.yml to <state>/berth/, stamped with a
// content hash, and names the image after that hash (berth/claude-env:<hash>). ccenv's own copies in
// a legacy checkout (<state>/compose.yml, <state>/image/) are never touched, and berth never tags
// claude-env:latest (plan, decisions 5 and 8).
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/ar4mirez/berth"
	"github.com/ar4mirez/berth/internal/host"
)

// Dir is berth's assets directory under the state root.
const Dir = "berth"

// ImageRepo is the repository berth tags its images in.
const ImageRepo = "berth/claude-env"

const stampFile = ".berth-assets"

// executable are the image/ files git records as 0755 (embed.FS keeps no modes).
// TestModesMatchGit keeps this in step with the repo.
var executable = map[string]bool{
	"image/archive.sh": true, "image/bashrc.sh": true, "image/entrypoint.sh": true, "image/init-firewall.sh": true,
}

// Set is a materialized (or overridden) set of assets.
type Set struct {
	Compose  string // compose.yml path
	ImageDir string // the image build context
	Tag      string // berth/claude-env:<12 hex>
}

type file struct {
	path string // "compose.yml", "image/Dockerfile", ...
	mode fs.FileMode
	data []byte
}

func files() ([]file, error) {
	var out []file
	err := fs.WalkDir(berth.Assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := berth.Assets.ReadFile(p)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if executable[p] {
			mode = 0o755
		}
		out = append(out, file{p, mode, b})
		return nil
	})
	out = append(out, file{".gitignore", 0o644, []byte(gitignore)})
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, err
}

// gitignore keeps <state>/berth out of git: at cutover the state root is the legacy checkout, a git
// repo, and berth's generated files must not show up in it (nor need an edit to its .gitignore).
// It's part of the stamp, not of the image tag.
const gitignore = "# berth's generated files (its image build context and compose.yml); not part of this checkout.\n*\n"

// hashOf is a stable content hash over (path, mode, content) of the files under prefix ("" = all).
func hashOf(all []file, prefix string) string {
	h := sha256.New()
	for _, f := range all {
		if !strings.HasPrefix(f.path, prefix) {
			continue
		}
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00", f.path, f.mode, len(f.data))
		h.Write(f.data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Embedded describes the embedded assets as they would be materialized under stateRoot, without
// writing anything.
func Embedded(stateRoot string) (Set, error) {
	all, err := files()
	if err != nil {
		return Set{}, err
	}
	dir := path.Join(stateRoot, Dir)
	return Set{Compose: path.Join(dir, "compose.yml"), ImageDir: path.Join(dir, "image"), Tag: ImageRepo + ":" + hashOf(all, "image/")[:12]}, nil
}

// LockFile is the state root's lock, in berth's own directory (git-ignored with the rest of it).
const LockFile = ".lock"

// LockPath returns <stateRoot>/berth/.lock, creating the directory (with its .gitignore) if needed,
// so a lock taken before the first materialization doesn't show up in a git checkout either.
func LockPath(fsys host.FS, stateRoot string) (string, error) {
	dir := path.Join(stateRoot, Dir)
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	gi := path.Join(dir, ".gitignore")
	if _, err := fsys.Stat(gi); err != nil {
		if err := fsys.WriteFile(gi, []byte(gitignore), 0o644); err != nil {
			return "", err
		}
	}
	return path.Join(dir, LockFile), nil
}

// Materialized reports whether <stateRoot>/berth holds the current embedded assets.
func Materialized(fsys host.FS, stateRoot string) bool {
	all, err := files()
	if err != nil {
		return false
	}
	b, err := fsys.ReadFile(path.Join(stateRoot, Dir, stampFile))
	return err == nil && strings.TrimSpace(string(b)) == hashOf(all, "")
}

// Materialize writes the embedded assets to <stateRoot>/berth unless they're already there. Stale
// image/ files are removed. The stamp is written last, so an interrupted write is redone next time.
// A <stateRoot>/berth that has files but no stamp isn't berth's, and is refused.
func Materialize(fsys host.FS, stateRoot string) (Set, error) {
	set, err := Embedded(stateRoot)
	if err != nil {
		return Set{}, err
	}
	if Materialized(fsys, stateRoot) {
		return set, nil
	}
	all, _ := files()
	dir := path.Join(stateRoot, Dir)
	if _, err := fsys.ReadFile(path.Join(dir, stampFile)); err != nil {
		if entries, rerr := fsys.ReadDir(dir); rerr == nil {
			for _, e := range entries {
				if n := e.Name(); n != LockFile && n != ".gitignore" {
					return Set{}, fmt.Errorf("%s exists and isn't berth's (no %s stamp); move it away", dir, stampFile)
				}
			}
		}
	}
	if err := fsys.MkdirAll(path.Join(dir, "image"), 0o755); err != nil {
		return Set{}, err
	}
	want := map[string]bool{}
	for _, f := range all {
		want[f.path] = true
		if err := fsys.WriteFileAtomic(path.Join(dir, f.path), f.data, f.mode); err != nil {
			return Set{}, err
		}
	}
	if entries, err := fsys.ReadDir(path.Join(dir, "image")); err == nil {
		for _, e := range entries {
			if p := "image/" + e.Name(); !e.IsDir() && !want[p] {
				if err := fsys.Remove(path.Join(dir, p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return Set{}, err
				}
			}
		}
	}
	if err := fsys.WriteFileAtomic(path.Join(dir, stampFile), []byte(hashOf(all, "")+"\n"), 0o644); err != nil {
		return Set{}, err
	}
	return set, nil
}

// Override is BERTH_IMAGE_DIR: build from dir (a flat image/ directory) instead of the embedded
// copy. The tag is a hash of its files, read through fsys.
func Override(fsys host.FS, set Set, dir string) (Set, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return Set{}, fmt.Errorf("BERTH_IMAGE_DIR: %w", err)
	}
	var all []file
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := fsys.Stat(path.Join(dir, e.Name()))
		if err != nil {
			return Set{}, err
		}
		b, err := fsys.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return Set{}, err
		}
		all = append(all, file{"image/" + e.Name(), fi.Mode().Perm(), b})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].path < all[j].path })
	set.ImageDir, set.Tag = dir, ImageRepo+":"+hashOf(all, "image/")[:12]
	return set, nil
}
