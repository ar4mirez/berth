package ssh

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/ar4mirez/berth/internal/host/local"
)

func pubKey(t *testing.T, kind string) gossh.PublicKey {
	t.Helper()
	var raw any
	var err error
	switch kind {
	case "ed25519":
		raw, _, err = ed25519.GenerateKey(rand.Reader)
	case "ecdsa":
		var k *ecdsa.PrivateKey
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		raw = &k.PublicKey
	case "rsa":
		var k *rsa.PrivateKey
		k, err = rsa.GenerateKey(rand.Reader, 2048)
		raw = &k.PublicKey
	}
	if err != nil {
		t.Fatal(err)
	}
	pk, err := gossh.NewPublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pk
}

var remoteAddr = &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2222}

const hostPort = "acme.example:2222"

func check(t *testing.T, hk hostKeys, key gossh.PublicKey) error {
	t.Helper()
	known, err := hk.load()
	if err != nil {
		t.Fatal(err)
	}
	return hk.callback(known)(hostPort, remoteAddr, key)
}

func TestUnknownHostRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "berth", "known_hosts")
	hk := hostKeys{fs: local.FS{}, path: path}
	key := pubKey(t, "ed25519")

	err := check(t, hk, key)
	if !errors.Is(err, ErrUnknownHost) || !strings.Contains(err.Error(), gossh.FingerprintSHA256(key)) {
		t.Fatalf("got %v; want ErrUnknownHost naming the fingerprint", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("refusing an unknown host must not create known_hosts")
	}

	// Same with an existing file that lists other hosts.
	other := knownhosts.Line([]string{"globex.example"}, pubKey(t, "ed25519")) + "\n"
	if err := (local.FS{}).MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(t, hk, key); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("with other hosts on file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != other {
		t.Errorf("known_hosts modified: %q", b)
	}
}

func TestAcceptNewRecordsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "berth", "known_hosts")
	key := pubKey(t, "ed25519")

	if err := check(t, hostKeys{fs: local.FS{}, path: path, acceptNew: true}, key); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := knownhosts.Line([]string{"[acme.example]:2222"}, key) + "\n"; string(b) != want {
		t.Errorf("recorded %q, want %q", b, want)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != want {
			t.Errorf("%s mode %o, want %o", p, fi.Mode().Perm(), want)
		}
	}
	// Now known: accepted without the flag.
	if err := check(t, hostKeys{fs: local.FS{}, path: path}, key); err != nil {
		t.Errorf("recorded key refused: %v", err)
	}
}

func TestChangedKeyAlwaysRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	pinned := knownhosts.Line([]string{"[acme.example]:2222"}, pubKey(t, "ed25519"))
	if err := os.WriteFile(path, []byte(pinned), 0o600); err != nil { // no trailing newline on purpose
		t.Fatal(err)
	}
	for _, acceptNew := range []bool{false, true} {
		err := check(t, hostKeys{fs: local.FS{}, path: path, acceptNew: acceptNew}, pubKey(t, "ed25519"))
		if !errors.Is(err, ErrHostKeyChanged) || !strings.Contains(err.Error(), path+":1") {
			t.Errorf("acceptNew=%v: got %v; want ErrHostKeyChanged pointing at %s:1", acceptNew, err, path)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != pinned {
		t.Errorf("known_hosts modified: %q", b)
	}
}

func TestAddKeepsExistingLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	other := knownhosts.Line([]string{"globex.example"}, pubKey(t, "ed25519"))
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil { // no trailing newline
		t.Fatal(err)
	}
	if err := check(t, hostKeys{fs: local.FS{}, path: path, acceptNew: true}, pubKey(t, "ecdsa")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2 || lines[0] != other || !strings.HasPrefix(lines[1], "[acme.example]:2222 ecdsa-") {
		t.Errorf("file now %q", b)
	}
}

func TestAlgorithms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	hk := hostKeys{fs: local.FS{}, path: path}
	if known, _ := hk.load(); algorithms(known, hostPort) != nil {
		t.Error("no file: leave the library default")
	}

	lines := knownhosts.Line([]string{"[acme.example]:2222"}, pubKey(t, "ed25519")) + "\n" +
		knownhosts.Line([]string{"[acme.example]:2222"}, pubKey(t, "rsa")) + "\n" +
		knownhosts.Line([]string{"globex.example"}, pubKey(t, "ecdsa")) + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	known, err := hk.load()
	if err != nil {
		t.Fatal(err)
	}
	got := algorithms(known, hostPort)
	want := []string{gossh.KeyAlgoED25519, gossh.KeyAlgoRSASHA256, gossh.KeyAlgoRSASHA512}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (only this host's key types; ssh-rsa via SHA-2)", got, want)
	}
	if got := algorithms(known, "initech.example:22"); got != nil {
		t.Errorf("host with nothing on file: %v, want nil", got)
	}
}

// TestExpectFingerprint: a new host is trusted only with the fingerprint the operator confirmed;
// another key is refused even with acceptNew, and the error carries what it presented (#44).
func TestExpectFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	key, other := pubKey(t, "ed25519"), pubKey(t, "ed25519")

	err := check(t, hostKeys{fs: local.FS{}, path: path, acceptNew: true, expect: gossh.FingerprintSHA256(other)}, key)
	var ue *UnknownHostError
	if !errors.As(err, &ue) || ue.Fingerprint != gossh.FingerprintSHA256(key) || ue.Expected == "" || !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("a different key: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a refused key must not be recorded")
	}
	if err := check(t, hostKeys{fs: local.FS{}, path: path, expect: ue.Fingerprint}, key); err != nil {
		t.Fatalf("the expected key: %v", err)
	}
	if err := check(t, hostKeys{fs: local.FS{}, path: path}, key); err != nil {
		t.Errorf("recorded key refused: %v", err)
	}
}

func TestForgetHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := ForgetHost(local.FS{}, path, hostPort); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	keep := "# a comment\n" + knownhosts.Line([]string{"globex.example"}, pubKey(t, "ed25519")) + "\n"
	body := knownhosts.Line([]string{"[acme.example]:2222"}, pubKey(t, "ed25519")) + "\n" + keep +
		knownhosts.Line([]string{"[acme.example]:2222"}, pubKey(t, "rsa")) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ForgetHost(local.FS{}, path, hostPort); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != keep {
		t.Errorf("after ForgetHost: %q, want %q", b, keep)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
}
