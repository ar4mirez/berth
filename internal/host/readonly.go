package host

import (
	"io"
	"io/fs"

	"github.com/ar4mirez/berth/internal/config"
)

// Guard returns h with its FS refusing every write while st is read-only. The command layer
// already refuses mutating commands; this holds the line for writes reached from a read command.
// Exec is not wrapped: whether a process mutates depends on its argv, so that stays a command-level
// decision.
func Guard(h *Host, st config.State) *Host {
	if !st.ReadOnly {
		return h
	}
	g := *h
	g.FS = readOnlyFS{FS: h.FS, st: st}
	return &g
}

type readOnlyFS struct {
	FS
	st config.State
}

func (r readOnlyFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if err := r.st.Writable("write " + name); err != nil {
		return err
	}
	return r.FS.WriteFile(name, data, perm)
}

func (r readOnlyFS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) error {
	if err := r.st.Writable("write " + name); err != nil {
		return err
	}
	return r.FS.WriteFileAtomic(name, data, perm)
}

func (r readOnlyFS) Chmod(name string, mode fs.FileMode) error {
	if err := r.st.Writable("chmod " + name); err != nil {
		return err
	}
	return r.FS.Chmod(name, mode)
}

func (r readOnlyFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := r.st.Writable("create " + name); err != nil {
		return err
	}
	return r.FS.MkdirAll(name, perm)
}

func (r readOnlyFS) Rename(oldpath, newpath string) error {
	if err := r.st.Writable("move " + oldpath); err != nil {
		return err
	}
	return r.FS.Rename(oldpath, newpath)
}

func (r readOnlyFS) Remove(name string) error {
	if err := r.st.Writable("remove " + name); err != nil {
		return err
	}
	return r.FS.Remove(name)
}

func (r readOnlyFS) RemoveAll(name string) error {
	if err := r.st.Writable("remove " + name); err != nil {
		return err
	}
	return r.FS.RemoveAll(name)
}

func (r readOnlyFS) Create(name string, perm fs.FileMode) (io.WriteCloser, error) {
	if err := r.st.Writable("write " + name); err != nil {
		return nil, err
	}
	return r.FS.Create(name, perm)
}

func (r readOnlyFS) Symlink(target, link string) error {
	if err := r.st.Writable("link " + link); err != nil {
		return err
	}
	return r.FS.Symlink(target, link)
}

func (r readOnlyFS) MkdirTemp(dir, pattern string) (string, error) {
	if err := r.st.Writable("create a temp dir"); err != nil {
		return "", err
	}
	return r.FS.MkdirTemp(dir, pattern)
}
