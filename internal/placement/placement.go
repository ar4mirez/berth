// Package placement is berth's proof of where a supervised process runs (#216,
// docs/decisions/216-placement-proof.md): a short-lived token, signed with an Ed25519 key berth keeps
// on the host and outside every container, that names the org, its current container and the
// process. A consumer verifies it with berth's public keys alone (GET /v1/placement/keys): no secret
// is shared with the container.
//
// A token is a compact JWS (RFC 7515) with alg EdDSA (RFC 8037): base64url(header) "."
// base64url(claims) "." base64url(signature), the signature over the first two parts as written.
package placement

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/host"
)

const (
	// TokenTTL is how long a token is good for: at most 15 minutes, refreshed before it ends.
	TokenTTL = 15 * time.Minute
	// KeyLife is how long a key signs new tokens; it is listed until its last token has expired.
	KeyLife = 7 * 24 * time.Hour
	// Type is the tokens' typ.
	Type = "berth-placement+jwt"
	// Issuer is the tokens' iss.
	Issuer = "berth"
)

// Claims are what a token says (all of it is checked by Verify, but org, container and
// container_started_at are the consumer's to compare with GET /v1/orgs/{org}).
type Claims struct {
	Issuer string `json:"iss"`
	Org    string `json:"org"`
	// Container is the container's id, and ContainerStartedAt when it was started (as
	// berth.info/v1's started_at): a recreated container is another one.
	Container          string `json:"container"`
	ContainerStartedAt string `json:"container_started_at"`
	Process            string `json:"process"`
	IssuedAt           int64  `json:"iat"`
	Expires            int64  `json:"exp"`
	Nonce              string `json:"nonce"`
}

// PublicKey is one key a consumer verifies tokens with (berth.placement-keys/v1).
type PublicKey struct {
	ID  string `json:"id"`
	Alg string `json:"alg"`
	// PublicKey is the key's 32 bytes, base64 (standard, padded).
	PublicKey string `json:"public_key"`
	// NotAfter is when its last token expires: after it, the key verifies nothing (RFC 3339, UTC).
	NotAfter string `json:"not_after"`
}

type key struct {
	ID          string    `json:"id"`
	Created     time.Time `json:"created"`
	RetireAfter time.Time `json:"retire_after"`
	Seed        []byte    `json:"seed"`
}

// Store is the signing keys, in one file on the host (mode 0600, never in a container).
type Store struct {
	FS   host.FS
	Path string
	Now  func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// keys loads the keys, drops the ones whose last token has expired, and makes a new one when none
// may sign any more; it saves the file when it changed it. The caller holds the lock.
func (s Store) keys() ([]key, error) { return s.load(true) }

// load is keys, and with write false it neither makes a key nor saves the file.
func (s Store) load(write bool) ([]key, error) {
	var ks []key
	b, err := s.FS.ReadFile(s.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &ks); err != nil {
			return nil, fmt.Errorf("%s: %w", s.Path, err)
		}
	}
	now, changed := s.now(), false
	ks = slices.DeleteFunc(ks, func(k key) bool {
		gone := !now.Before(k.RetireAfter.Add(TokenTTL)) || len(k.Seed) != ed25519.SeedSize
		changed = changed || gone
		return gone
	})
	if !write {
		return ks, nil
	}
	if !slices.ContainsFunc(ks, func(k key) bool { return now.Before(k.RetireAfter) }) {
		id := make([]byte, 8)
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		ks = append(ks, key{ID: hex.EncodeToString(id), Created: now, RetireAfter: now.Add(KeyLife), Seed: seed})
		changed = true
	}
	if changed {
		b, _ := json.MarshalIndent(ks, "", "  ")
		if err := s.FS.MkdirAll(path.Dir(s.Path), 0o700); err != nil {
			return nil, err
		}
		if err := s.FS.WriteFileAtomic(s.Path, append(b, '\n'), 0o600); err != nil {
			return nil, err
		}
	}
	return ks, nil
}

func (s Store) locked(ctx context.Context, f func([]key) error) error {
	if err := s.FS.MkdirAll(path.Dir(s.Path), 0o700); err != nil {
		return err
	}
	u, err := s.FS.Lock(ctx, s.Path+".lock")
	if err != nil {
		return err
	}
	defer func() { _ = u.Unlock() }()
	ks, err := s.keys()
	if err != nil {
		return err
	}
	return f(ks)
}

// PublicKeys are the keys a token may be signed with now: the one signing, and older ones until
// their last token has expired. Newest first. The first call makes the first key.
func (s Store) PublicKeys(ctx context.Context) ([]PublicKey, error) {
	var out []PublicKey
	err := s.locked(ctx, func(ks []key) error { out = public(ks); return nil })
	return out, err
}

// PublicKeysReadOnly is PublicKeys without making or retiring a key, for --read-only: the keys as
// saved, but for the ones whose last token has expired.
func (s Store) PublicKeysReadOnly() ([]PublicKey, error) {
	ks, err := s.load(false)
	return public(ks), err
}

func public(ks []key) []PublicKey {
	out := []PublicKey{}
	for _, k := range ks {
		out = append(out, PublicKey{ID: k.ID, Alg: "Ed25519",
			PublicKey: base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(k.Seed).Public().(ed25519.PublicKey)),
			NotAfter:  k.RetireAfter.Add(TokenTTL).UTC().Format(time.RFC3339)})
	}
	slices.Reverse(out)
	return out
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

var b64 = base64.RawURLEncoding

// Issue signs a token for c with the key signing now: issued now, good for TokenTTL, with a nonce
// of its own. c's Issuer, IssuedAt, Expires and Nonce are set here.
func (s Store) Issue(ctx context.Context, c Claims) (string, Claims, error) {
	var tok string
	err := s.locked(ctx, func(ks []key) error {
		// The newest key that may still sign: keys() leaves at least one.
		var k key
		for _, x := range ks {
			if s.now().Before(x.RetireAfter) && (k.ID == "" || x.Created.After(k.Created)) {
				k = x
			}
		}
		n := make([]byte, 16)
		if _, err := rand.Read(n); err != nil {
			return err
		}
		now := s.now()
		c.Issuer, c.IssuedAt, c.Expires, c.Nonce = Issuer, now.Unix(), now.Add(TokenTTL).Unix(), hex.EncodeToString(n)
		h, _ := json.Marshal(header{Alg: "EdDSA", Typ: Type, Kid: k.ID})
		p, _ := json.Marshal(c)
		signing := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
		tok = signing + "." + b64.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(k.Seed), []byte(signing)))
		return nil
	})
	return tok, c, err
}

// Verify is what a consumer does with a token and berth's public keys: the signature by the key it
// names, a key still listed (not after its not_after), typ and iss as berth writes them, and a
// token issued in the past that hasn't expired. It returns the claims; comparing the org and its
// container with what berth says now (GET /v1/orgs/{org}: started_at) is the consumer's.
func Verify(token string, keys []PublicKey, now time.Time) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("not a compact JWS")
	}
	hb, err1 := b64.DecodeString(parts[0])
	pb, err2 := b64.DecodeString(parts[1])
	sig, err3 := b64.DecodeString(parts[2])
	if err := errors.Join(err1, err2, err3); err != nil {
		return c, fmt.Errorf("not base64url: %w", err)
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil || h.Alg != "EdDSA" || h.Typ != Type {
		return c, errors.New("not a berth placement token (alg EdDSA, typ " + Type + ")")
	}
	i := slices.IndexFunc(keys, func(k PublicKey) bool { return k.ID == h.Kid })
	if i < 0 {
		return c, fmt.Errorf("signed by an unknown key %q", h.Kid)
	}
	pub, err := base64.StdEncoding.DecodeString(keys[i].PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize || keys[i].Alg != "Ed25519" {
		return c, fmt.Errorf("key %s is not an Ed25519 public key", h.Kid)
	}
	if na, err := time.Parse(time.RFC3339, keys[i].NotAfter); err != nil || now.After(na) {
		return c, fmt.Errorf("key %s is past its not_after", h.Kid)
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return c, errors.New("the signature doesn't verify")
	}
	if err := json.Unmarshal(pb, &c); err != nil {
		return c, fmt.Errorf("its claims: %w", err)
	}
	switch {
	case c.Issuer != Issuer:
		return c, fmt.Errorf("issued by %q, not berth", c.Issuer)
	case c.Org == "" || c.Container == "" || c.Process == "":
		return c, errors.New("it names no org, container or process")
	case c.Expires-c.IssuedAt > int64(TokenTTL/time.Second):
		return c, errors.New("it is good for longer than berth issues tokens for")
	case now.Unix() >= c.Expires:
		return c, errors.New("it has expired")
	case now.Unix() < c.IssuedAt-60:
		return c, errors.New("it was issued in the future")
	}
	return c, nil
}
