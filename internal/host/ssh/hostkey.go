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

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ar4mirez/berth/internal/host"
)

// hostKeys enforces berth's own known_hosts file (never ~/.ssh/known_hosts): an unknown host
// is refused unless acceptNew is set, and a changed or revoked key is always refused.
type hostKeys struct {
	fs        host.FS // the operator's machine, where the file lives
	path      string
	acceptNew bool
}

// ErrUnknownHost is returned for a host that isn't in berth's known_hosts, without AcceptNewHostKey.
var ErrUnknownHost = errors.New("unknown SSH host")

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
			if !h.acceptNew {
				return fmt.Errorf("%w %s: its %s key is %s and it is not in %s. Verify that fingerprint "+
					"with the host's owner, then connect once with --accept-new-host-key",
					ErrUnknownHost, hostname, key.Type(), gossh.FingerprintSHA256(key), h.path)
			}
			return h.add(hostname, key)
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
