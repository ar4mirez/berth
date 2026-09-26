package org

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
)

// Orgs is the orgs/ directory under a state root, on a host. All access goes through host.FS.
type Orgs struct {
	FS  host.FS
	Dir string // <state root>/orgs
	// Lock, if set, is taken around Set's read-modify-write (the state root's lock; re-entrant).
	Lock func() (unlock func(), err error)
}

// EnvPath is <orgs>/<org>/org.env.
func (o Orgs) EnvPath(org string) string { return path.Join(o.Dir, org, "org.env") }

// keyName is what ccenv's keys look like. ccenv splices the key into a grep/awk regex, so only
// plain names behave the same in both; anything else is refused rather than guessed at.
var keyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkKey(key string) error {
	if !keyName.MatchString(key) {
		return fmt.Errorf("invalid org.env key %q", key)
	}
	return nil
}

// Get is envval <org> KEY. A missing key is ("", false, nil); a missing org.env is an error
// (grep fails, envval prints nothing).
func (o Orgs) Get(org, key string) (string, bool, error) {
	if err := checkKey(key); err != nil {
		return "", false, err
	}
	data, err := o.FS.ReadFile(o.EnvPath(org))
	if err != nil {
		return "", false, err
	}
	v, ok := Lookup(data, key)
	return v, ok, nil
}

// Set is setval <org> KEY VALUE: the file is rewritten in place, so it keeps its inode and mode
// (0600 from init). Like setval, it never creates org.env.
func (o Orgs) Set(org, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if o.Lock != nil {
		unlock, err := o.Lock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	p := o.EnvPath(org)
	data, err := o.FS.ReadFile(p)
	if err != nil {
		return err
	}
	return o.FS.WriteFile(p, Set(data, key, value), 0o600)
}

// NextPort is next_port KEY BASE: one more than the largest KEY across every org.env, and at
// least BASE. Like ccenv's `"$ORGS"/*/org.env` glob it skips hidden entries (e.g. .restore-*
// staging dirs) and follows symlinks. A file whose value isn't a single integer (duplicate keys,
// junk) is skipped, as bash's `[ … -gt … ]` fails on it.
//
// Divergences, recorded in PARITY.md: bash prints "integer expression expected" to stderr for a
// skipped file and berth stays silent; and bash evaluates the winning value in $(( )), so a
// leading zero means octal there (or an error), while berth always reads decimal.
func (o Orgs) NextPort(key string, base int) (int, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	entries, err := o.FS.ReadDir(o.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return base, nil // the glob matches nothing
	}
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	maxPort := int64(base) - 1
	for _, n := range names {
		p := o.EnvPath(n)
		fi, err := o.FS.Stat(p) // [ -f "$f" ]: follows symlinks, regular files only
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := o.FS.ReadFile(p)
		if err != nil {
			continue // grep fails on an unreadable file and next_port skips it
		}
		if v, ok := bashInt(portValue(data, key)); ok && v > maxPort {
			maxPort = v
		}
	}
	return int(maxPort + 1), nil
}
