package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/ops"
)

// The API's access over TCP (#62): tokens, and the certificate it serves with. Both live in the
// operator's ~/.config/berth/api, as berth's host keys live beside them.

// The scopes of a token: what its bearer may do.
const (
	ScopeRead    = "read"
	ScopeWrite   = "write"   // and read
	ScopeRestart = "restart" // and write
)

// APIToken is one token, as stored: its hash, never the token.
type APIToken struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Hash    string `json:"sha256"`
	Created string `json:"created"`
}

// APITokens is `berth serve token ls --output json`.
type APITokens struct {
	Schema string     `json:"schema"` // "berth.api-tokens/v1"
	Tokens []APIToken `json:"tokens"`
}

var tokenName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (a *App) apiDir() string       { return path.Join(a.hostPaths().Dir, "api") }
func (a *App) apiTokenFile() string { return path.Join(a.apiDir(), "tokens.json") }

// ListAPITokens reads the tokens (none when the file isn't there).
func (a *App) ListAPITokens() ([]APIToken, error) {
	b, err := a.Operator.FS.ReadFile(a.apiTokenFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f APITokens
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", a.apiTokenFile(), err)
	}
	return f.Tokens, nil
}

func (a *App) writeAPITokens(ts []APIToken) error {
	if err := a.Operator.FS.MkdirAll(a.apiDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(APITokens{Schema: "berth.api-tokens/v1", Tokens: ts}, "", "  ")
	if err != nil {
		return err
	}
	return a.Operator.FS.WriteFileAtomic(a.apiTokenFile(), append(b, '\n'), 0o600)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// APITokenScope is the scope of a token a request presented, in constant time; ok is false for one
// berth didn't issue.
func (a *App) APITokenScope(token string) (scope string, ok bool) {
	ts, err := a.ListAPITokens()
	if err != nil || token == "" {
		return "", false
	}
	h := []byte(hashToken(token))
	for _, t := range ts {
		if subtle.ConstantTimeCompare(h, []byte(t.Hash)) == 1 {
			scope, ok = t.Scope, true
		}
	}
	return scope, ok
}

// APIToken is `berth serve token add|ls|rm`.
func (a *App) APIToken(args []string) error {
	usage := &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("usage: %s serve token add <name> [--scope read|write|restart] | ls | rm <name>", Tool)}
	if len(args) == 0 {
		return usage
	}
	ts, err := a.ListAPITokens()
	if err != nil {
		return err
	}
	switch args[0] {
	case "ls":
		if a.Output == OutputJSON {
			if ts == nil {
				ts = []APIToken{}
			}
			return a.writeJSON(APITokens{Schema: "berth.api-tokens/v1", Tokens: ts})
		}
		if len(ts) == 0 {
			sayf(a.Stdout, "No API tokens. Add one: %s serve token add <name>\n", Tool)
			return nil
		}
		sayf(a.Stdout, "%-24s %-8s %s\n", "NAME", "SCOPE", "CREATED")
		for _, t := range ts {
			sayf(a.Stdout, "%-24s %-8s %s\n", t.Name, t.Scope, t.Created)
		}
		return nil
	case "add":
		scope, name := ScopeRead, ""
		for i := 1; i < len(args); i++ {
			switch {
			case args[i] == "--scope" && i+1 < len(args):
				i++
				scope = args[i]
			case strings.HasPrefix(args[i], "-") || name != "":
				return usage
			default:
				name = args[i]
			}
		}
		switch {
		case !tokenName.MatchString(name):
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "a token's name is lowercase letters, digits and dashes (as in: ci, laptop-2)"}
		case scope != ScopeRead && scope != ScopeWrite && scope != ScopeRestart:
			return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "--scope is read, write or restart"}
		case slices.ContainsFunc(ts, func(t APIToken) bool { return t.Name == name }):
			return fmt.Errorf("there is already a token named %s (%s serve token rm %s)", name, Tool, name)
		}
		if err := a.State.Writable("add an API token"); err != nil {
			return err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		token := "berth_" + base64.RawURLEncoding.EncodeToString(raw)
		ts = append(ts, APIToken{Name: name, Scope: scope, Hash: hashToken(token), Created: time.Now().UTC().Format(time.RFC3339)})
		if err := a.writeAPITokens(ts); err != nil {
			return err
		}
		// The token goes to stdout alone, so a script can capture it; the rest is for a person.
		sayf(a.Stderr, "Token %s (scope: %s). It is shown once, and only its hash is kept:\n", name, scope)
		sayf(a.Stdout, "%s\n", token)
		return nil
	case "rm":
		if len(args) != 2 {
			return usage
		}
		i := slices.IndexFunc(ts, func(t APIToken) bool { return t.Name == args[1] })
		if i < 0 {
			return &ops.Error{Kind: ops.KindNotFound, Code: 1, Msg: "no token named " + args[1]}
		}
		if err := a.State.Writable("remove an API token"); err != nil {
			return err
		}
		if err := a.writeAPITokens(slices.Delete(ts, i, i+1)); err != nil {
			return err
		}
		sayf(a.Stdout, "Removed %s: a running server refuses it from now on.\n", args[1])
		return nil
	}
	return usage
}

// APICertificate is the certificate the API serves TCP with: the one in ~/.config/berth/api, made
// the first time (self-signed, for the names given). fingerprint is the SHA-256 of the
// certificate, which a client pins.
func (a *App) APICertificate(names []string) (cert tls.Certificate, fingerprint string, err error) {
	certFile, keyFile := path.Join(a.apiDir(), "cert.pem"), path.Join(a.apiDir(), "key.pem")
	c, cerr := a.Operator.FS.ReadFile(certFile)
	k, kerr := a.Operator.FS.ReadFile(keyFile)
	if errors.Is(cerr, fs.ErrNotExist) || errors.Is(kerr, fs.ErrNotExist) {
		if err := a.State.Writable("create the API's certificate"); err != nil {
			return cert, "", err
		}
		if c, k, err = selfSigned(names, time.Now()); err != nil {
			return cert, "", err
		}
		if err := a.Operator.FS.MkdirAll(a.apiDir(), 0o700); err != nil {
			return cert, "", err
		}
		if err := a.Operator.FS.WriteFileAtomic(keyFile, k, 0o600); err != nil {
			return cert, "", err
		}
		if err := a.Operator.FS.WriteFileAtomic(certFile, c, 0o644); err != nil {
			return cert, "", err
		}
	} else if err := errors.Join(cerr, kerr); err != nil {
		return cert, "", err
	}
	return LoadAPICertificate(c, k)
}

// LoadAPICertificate parses a certificate and key (PEM), and gives the certificate's fingerprint.
func LoadAPICertificate(certPEM, keyPEM []byte) (tls.Certificate, string, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return cert, "", err
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return cert, hex.EncodeToString(sum[:]), nil
}

func selfSigned(names []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "berth API"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, n := range append([]string{"localhost", "127.0.0.1", "::1"}, names...) {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if n != "" && !slices.Contains(tmpl.DNSNames, n) {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), nil
}
