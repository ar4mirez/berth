package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"

	"go.yaml.in/yaml/v3"

	"github.com/ar4mirez/berth/internal/host"
)

// Leases record which host may run each org (#47): its active host. They live on the operator's
// machine, next to the registry; the host holding a lease also has a marker in its state root
// (LeaseMarker), so the host itself can tell.
type Leases struct {
	Orgs map[string]string `yaml:"leases"` // org -> host name ("local" or a registered host)
}

func (p Paths) Leases() string { return path.Join(p.Dir, "leases.yaml") }

// LeaseMarker is the marker file for org under a host's state root.
func LeaseMarker(stateRoot, org string) string {
	return path.Join(stateRoot, "berth", "lease", org)
}

// LoadLeases reads the leases; a missing file is none.
func LoadLeases(fsys host.FS, p Paths) (*Leases, error) {
	l := &Leases{}
	b, err := fsys.ReadFile(p.Leases())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(l); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: %w", p.Leases(), err)
		}
	}
	if l.Orgs == nil {
		l.Orgs = map[string]string{}
	}
	return l, nil
}

// SaveLeases writes the leases atomically (0600).
func SaveLeases(fsys host.FS, p Paths, l *Leases) error {
	if err := fsys.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	head := "# berth's active-host leases: the one host each org may run on (docs/hosts.md). Move one with --take-lease.\n"
	return fsys.WriteFileAtomic(p.Leases(), append([]byte(head), b...), 0o600)
}
