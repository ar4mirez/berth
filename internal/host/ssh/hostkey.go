package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"slices"
	"strings"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ar4mirez/berth/internal/host"
)

// hostKeys enforces berth's own known_hosts file (never ~/.ssh/known_hosts): an unknown host
// is refused unless acceptNew is set or its fingerprint is the expected one, and a changed or
// revoked key is always refused.
type hostKeys struct {
	fs        host.FS // the operator's machine, where the file lives
	path      string
	acceptNew bool
	expect    string // accept a new key with this SHA256 fingerprint
}

// ErrUnknownHost is returned for a host that isn't in berth's known_hosts, without AcceptNewHostKey.
// The error is an *UnknownHostError, which carries the key's fingerprint.
var ErrUnknownHost = errors.New("unknown SSH host")

// UnknownHostError is a host that isn't in berth's known_hosts: the key it presented, so the
// operator can compare the fingerprint with the host's before trusting it.
type UnknownHostError struct {
	Host        string
	KeyType     string
	Fingerprint string // SHA256:…
	File        string // berth's known_hosts
	// Expected is set when the caller expected another fingerprint (ExpectFingerprint).
	Expected string
}

func (e *UnknownHostError) Error() string {
	if e.Expected != "" {
		return fmt.Sprintf("%s %s: its %s key is %s, not the expected %s. Don't connect until you know why",
			ErrUnknownHost, e.Host, e.KeyType, e.Fingerprint, e.Expected)
	}
	return fmt.Sprintf("%s %s: its %s key is %s and it is not in %s. Verify that fingerprint "+
		"with the host's owner, then connect once with --accept-new-host-key", ErrUnknownHost, e.Host, e.KeyType, e.Fingerprint, e.File)
}

func (e *UnknownHostError) Unwrap() error { return ErrUnknownHost }

// ErrHostKeyChanged is returned when the host presents a key other than the one on file.
var ErrHostKeyChanged = errors.New("SSH host key changed")

// load returns the known_hosts check (nil when the file doesn't exist yet).
func (h hostKeys) load() (gossh.HostKeyCallback, error) {
	if _, err := h.fs.Stat(h.path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	cb, err := knownhosts.New(h.path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", h.path, err)
	}
	return cb, nil
}

// callback is the HostKeyCallback for one connection.
func (h hostKeys) callback(known gossh.HostKeyCallback) gossh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key gossh.PublicKey) error {
		var err error = &knownhosts.KeyError{} // no file yet: every host is unknown
		if known != nil {
			err = known(hostname, remote, key)
		}
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		var re *knownhosts.RevokedError
		switch {
		case errors.As(err, &ke) && len(ke.Want) == 0:
			fp := gossh.FingerprintSHA256(key)
			if h.expect != "" && fp == h.expect || h.expect == "" && h.acceptNew {
				return h.add(hostname, key)
			}
			return &UnknownHostError{Host: hostname, KeyType: key.Type(), Fingerprint: fp, File: h.path, Expected: h.expect}
		case errors.As(err, &ke):
			w := ke.Want[0]
			return fmt.Errorf("%w for %s: it now presents %s %s, but %s:%d has %s %s. This could be an attack; "+
				"if the host was rebuilt, remove that line and verify the new key",
				ErrHostKeyChanged, hostname, key.Type(), gossh.FingerprintSHA256(key),
				w.Filename, w.Line, w.Key.Type(), gossh.FingerprintSHA256(w.Key))
		case errors.As(err, &re):
			return fmt.Errorf("host key for %s is revoked (%s:%d)", hostname, re.Revoked.Filename, re.Revoked.Line)
		}
		return err
	}
}

// add appends hostname's key to the file (mode 0600, directory 0700).
func (h hostKeys) add(hostname string, key gossh.PublicKey) error {
	if err := h.fs.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return err
	}
	old, err := h.fs.ReadFile(h.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(old) > 0 && old[len(old)-1] != '\n' {
		old = append(old, '\n')
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	return h.fs.WriteFile(h.path, append(old, line...), 0o600)
}

// ForgetHost removes addr's lines from berth's known_hosts file at path (on fsys, the operator's
// machine), as `ssh-keygen -R` would. A missing file is not an error.
func ForgetHost(fsys host.FS, path, addr string) error {
	b, err := fsys.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	want := knownhosts.Normalize(addr)
	var out []byte
	changed := false
	for _, l := range strings.SplitAfter(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 0 && !strings.HasPrefix(f[0], "#") && !strings.HasPrefix(f[0], "@") &&
			slices.Contains(strings.Split(f[0], ","), want) {
			changed = true
			continue
		}
		out = append(out, l...)
	}
	if !changed {
		return nil
	}
	return fsys.WriteFileAtomic(path, out, 0o600)
}

// algorithms lists the host key algorithms to offer for addr: those of the keys on file, so a
// server with several key types presents the one we pinned instead of failing as "changed".
// nil (the library default) when nothing is on file.
func algorithms(known gossh.HostKeyCallback, addr string) []string {
	if known == nil {
		return nil
	}
	// Probe with a throwaway key: the mismatch error lists every key on file for addr.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}
	probe, err := gossh.NewPublicKey(pub)
	if err != nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if !errors.As(known(addr, &net.TCPAddr{IP: net.IPv4zero}, probe), &ke) {
		return nil
	}
	var algos []string
	for _, w := range ke.Want {
		switch t := w.Key.Type(); t {
		case gossh.KeyAlgoRSA:
			// An ssh-rsa key is verified with the SHA-2 signature algorithms.
			algos = append(algos, gossh.KeyAlgoRSASHA512, gossh.KeyAlgoRSASHA256)
		default:
			algos = append(algos, t)
		}
	}
	slices.Sort(algos)
	return slices.Compact(algos)
}
