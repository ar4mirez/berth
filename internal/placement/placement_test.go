package placement

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/host/local"
)

func store(t *testing.T, now *time.Time) Store {
	t.Helper()
	return Store{FS: local.New().FS, Path: filepath.Join(t.TempDir(), "placement", "keys.json"), Now: func() time.Time { return *now }}
}

// TestPlacementToken: a token berth issues verifies with its public keys alone, for at most 15
// minutes, and says the org, container and process it was issued for (#216). The oracle for the
// format is RFC 7515 and RFC 8037: the token is checked here with crypto/ed25519 directly, not
// with Verify, so a mistake shared by Issue and Verify can't pass.
func TestPlacementToken(t *testing.T) {
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	s := store(t, &now)
	ctx := context.Background()
	tok, c, err := s.Issue(ctx, Claims{Org: "acme", Container: "f00d", ContainerStartedAt: "2026-11-27T17:59:00Z", Process: "runner"})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := s.PublicKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Alg != "Ed25519" || keys[0].NotAfter != now.Add(KeyLife+TokenTTL).Format(time.RFC3339) {
		t.Fatalf("keys: %+v %v", keys, err)
	}

	// By hand: three base64url parts, the header EdDSA with the key's id, an Ed25519 signature
	// over the first two, and the claims as issued.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", tok)
	}
	raw := func(i int) []byte {
		b, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		return b
	}
	var h map[string]string
	if json.Unmarshal(raw(0), &h) != nil || h["alg"] != "EdDSA" || h["typ"] != "berth-placement+jwt" || h["kid"] != keys[0].ID {
		t.Errorf("header %s", raw(0))
	}
	pub, _ := base64.StdEncoding.DecodeString(keys[0].PublicKey)
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), raw(2)) {
		t.Error("the signature doesn't verify with the listed public key")
	}
	var got map[string]any
	if json.Unmarshal(raw(1), &got) != nil || got["org"] != "acme" || got["container"] != "f00d" || got["process"] != "runner" ||
		got["iss"] != "berth" || got["exp"].(float64)-got["iat"].(float64) != 900 || len(got["nonce"].(string)) != 32 {
		t.Errorf("claims %s", raw(1))
	}
	if c.Expires != now.Add(15*time.Minute).Unix() {
		t.Errorf("expires %d", c.Expires)
	}

	// Verify, as a consumer would, and what it refuses.
	if v, err := Verify(tok, keys, now.Add(14*time.Minute)); err != nil || v.Org != "acme" || v.ContainerStartedAt != "2026-11-27T17:59:00Z" {
		t.Errorf("verify: %+v %v", v, err)
	}
	if _, err := Verify(tok, keys, now.Add(15*time.Minute)); err == nil {
		t.Error("an expired token verified")
	}
	other := store(t, &now)
	otherKeys, _ := other.PublicKeys(ctx)
	if _, err := Verify(tok, otherKeys, now); err == nil {
		t.Error("it verified with another berth's keys")
	}
	// A claim changed after signing: the same signature, another org.
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw(1)), `"acme"`, `"globex"`, 1))) + "." + parts[2]
	if _, err := Verify(forged, keys, now); err == nil {
		t.Error("a token with a changed claim verified")
	}
	if _, err := Verify(parts[0]+"."+parts[1]+".", keys, now); err == nil {
		t.Error("an unsigned token verified")
	}

	// The key file is the host's, 0600, and holds no token.
	if fi, err := os.Stat(s.Path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("keys.json: %v %v", fi, err)
	}
}

// TestPlacementKeyRotation: a key signs for KeyLife, then a new one does; the old one is still
// listed until its last token can have expired, and its tokens still verify until then.
func TestPlacementKeyRotation(t *testing.T) {
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	s := store(t, &now)
	ctx := context.Background()
	first, _ := s.PublicKeys(ctx)
	now = now.Add(KeyLife - time.Minute)
	old, _, _ := s.Issue(ctx, Claims{Org: "acme", Container: "c1", Process: "runner"})
	now = now.Add(2 * time.Minute) // the first key may no longer sign
	tok, _, _ := s.Issue(ctx, Claims{Org: "acme", Container: "c1", Process: "runner"})
	keys, _ := s.PublicKeys(ctx)
	if len(keys) != 2 || keys[1].ID != first[0].ID || keys[0].ID == first[0].ID {
		t.Fatalf("after rotation: %+v", keys)
	}
	if _, err := Verify(old, keys, now); err != nil {
		t.Errorf("a token of the old key, still valid: %v", err)
	}
	if v, err := Verify(tok, keys[:1], now); err != nil || v.Org != "acme" {
		t.Errorf("the new key's token: %v", err)
	}
	now = now.Add(TokenTTL) // the old key's last token has expired
	if keys, _ = s.PublicKeys(ctx); len(keys) != 1 || keys[0].ID == first[0].ID {
		t.Errorf("the old key is still listed after its last token: %+v", keys)
	}
}
