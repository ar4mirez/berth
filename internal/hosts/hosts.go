// Package hosts is berth's registry of the hosts it manages (#44): this machine, always there as
// "local", and the hosts added with `berth host add`, reached over SSH (internal/host/ssh).
//
// The registry and berth's keys live on the operator's machine, next to config.yaml:
//
//	~/.config/berth/hosts.yaml      the hosts (0600)
//	~/.config/berth/keys/<name>     berth's own SSH key for each host (0600, and <name>.pub)
//	~/.config/berth/known_hosts     their pinned host keys (never ~/.ssh/known_hosts)
//
// A host only ever receives berth's public key in its authorized_keys; the operator's backup.key
// never goes to a host (docs/plan.md, decision 4).
package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ar4mirez/berth/internal/host"
)

// Local is the name of this machine, which is always registered.
const Local = "local"

// Kinds of host.
const (
	KindLocal = "local"
	KindSSH   = "ssh"
)

// Entry is one registered host.
type Entry struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
	// User and Addr (host:port) reach an ssh host.
	User string `yaml:"user,omitempty"`
	Addr string `yaml:"addr,omitempty"`
	// Home is the state root on that host (its orgs/ and backups/).
	Home string `yaml:"home"`
	// Key is berth's private key for the host, on the operator's machine.
	Key string `yaml:"key,omitempty"`
}

// Address is user@host:port, as shown to the operator.
func (e Entry) Address() string {
	if e.Kind == KindLocal {
		return ""
	}
	return e.User + "@" + e.Addr
}

// Registry is the hosts file.
type Registry struct {
	Hosts []Entry `yaml:"hosts"`
}

// Find returns the entry named name.
func (r *Registry) Find(name string) (Entry, bool) {
	i := slices.IndexFunc(r.Hosts, func(e Entry) bool { return e.Name == name })
	if i < 0 {
		return Entry{}, false
	}
	return r.Hosts[i], true
}

// Remove drops the entry named name, reporting whether it was there.
func (r *Registry) Remove(name string) bool {
	n := len(r.Hosts)
	r.Hosts = slices.DeleteFunc(r.Hosts, func(e Entry) bool { return e.Name == name })
	return len(r.Hosts) != n
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// CheckName refuses a name that isn't a host name berth accepts (and "local", which is taken).
func CheckName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid host name %q: lowercase letters, digits and dashes, up to 32, starting with a letter or digit", name)
	}
	if name == Local {
		return fmt.Errorf("%q is this machine, and is always registered", Local)
	}
	return nil
}

// Paths are the registry's files on the operator's machine.
type Paths struct {
	Dir string // ~/.config/berth
}

func (p Paths) File() string           { return path.Join(p.Dir, "hosts.yaml") }
func (p Paths) Lock() string           { return path.Join(p.Dir, ".hosts.lock") }
func (p Paths) KnownHosts() string     { return path.Join(p.Dir, "known_hosts") }
func (p Paths) Key(name string) string { return path.Join(p.Dir, "keys", name) }

// Load reads the registry; a missing file is an empty registry. Unknown keys are an error, so a
// typo doesn't silently drop a host.
func Load(fsys host.FS, p Paths) (*Registry, error) {
	r := &Registry{}
	b, err := fsys.ReadFile(p.File())
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(r); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", p.File(), err)
	}
	seen := map[string]bool{}
	for _, e := range r.Hosts {
		if err := CheckName(e.Name); err != nil {
			return nil, fmt.Errorf("%s: %w", p.File(), err)
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("%s: host %q is listed twice", p.File(), e.Name)
		}
		seen[e.Name] = true
		if e.Kind != KindSSH || e.User == "" || e.Addr == "" || !path.IsAbs(e.Home) || e.Key == "" {
			return nil, fmt.Errorf("%s: host %q is incomplete (kind ssh, user, addr, an absolute home and key are required)", p.File(), e.Name)
		}
	}
	return r, nil
}

// Save writes the registry atomically (0600, directory 0700).
func Save(fsys host.FS, p Paths, r *Registry) error {
	if err := fsys.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	head := "# berth's hosts: berth host add|rm|rotate-access change this file (docs/hosts.md).\n"
	return fsys.WriteFileAtomic(p.File(), append([]byte(head), b...), 0o600)
}

// ParseTarget splits [user@]host[:port] into a user (defaultUser when none is given) and host:port
// (port 22 when none is given). IPv6 addresses go in brackets: [2001:db8::1]:22.
func ParseTarget(s, defaultUser string) (user, addr string, err error) {
	user = defaultUser
	if i := strings.LastIndex(s, "@"); i >= 0 {
		user, s = s[:i], s[i+1:]
	}
	if user == "" {
		return "", "", errors.New("no user: give it as user@host")
	}
	h, port := s, "22"
	if hh, pp, e := net.SplitHostPort(s); e == nil {
		h, port = hh, pp
	} else if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		h = s[1 : len(s)-1]
	} else if strings.Contains(s, ":") {
		return "", "", fmt.Errorf("%q: invalid address (an IPv6 address goes in brackets, as [addr]:port)", s)
	}
	if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("%q: invalid port", s)
	}
	if h == "" || strings.ContainsAny(h, " /\t") {
		return "", "", fmt.Errorf("%q: invalid host", s)
	}
	return user, net.JoinHostPort(h, port), nil
}
