package hosts

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// Key is an SSH key berth made for one host.
type Key struct {
	Private []byte // OpenSSH PEM, unencrypted: the file is 0600 on the operator's machine
	Public  gossh.PublicKey
}

// AuthorizedLine is the key's authorized_keys line, with comment.
func (k Key) AuthorizedLine(comment string) string {
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(k.Public))) + " " + comment
}

// NewKey makes an ed25519 key.
func NewKey(comment string) (Key, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, err
	}
	block, err := gossh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return Key{}, err
	}
	s, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return Key{}, err
	}
	return Key{Private: pem.EncodeToMemory(block), Public: s.PublicKey()}, nil
}

// PublicOf reads the public key of a private key file's content.
func PublicOf(private []byte) (gossh.PublicKey, error) {
	s, err := gossh.ParsePrivateKey(private)
	if err != nil {
		return nil, err
	}
	return s.PublicKey(), nil
}

// Comment is the comment on berth's line in a host's authorized_keys, so the operator can tell it
// apart from their own.
func Comment(name string) string { return "berth:" + name }

// AddAuthorized appends line to an authorized_keys file's content, unless that key is already there.
func AddAuthorized(content []byte, line string) []byte {
	if k, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line)); err == nil && hasKey(content, k) {
		return content
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	return append(content, line+"\n"...)
}

// RemoveAuthorized drops every line holding key from an authorized_keys file's content, whatever
// its options or comment, and reports whether there was one. Other lines are kept byte for byte.
func RemoveAuthorized(content []byte, key gossh.PublicKey) ([]byte, bool) {
	var out []byte
	found := false
	for _, l := range bytes.SplitAfter(content, []byte("\n")) {
		if k, _, _, _, err := gossh.ParseAuthorizedKey(l); err == nil && bytes.Equal(k.Marshal(), key.Marshal()) {
			found = true
			continue
		}
		out = append(out, l...)
	}
	return out, found
}

func hasKey(content []byte, key gossh.PublicKey) bool {
	_, found := RemoveAuthorized(content, key)
	return found
}
