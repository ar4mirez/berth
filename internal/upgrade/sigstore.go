package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// Sigstore verifies a release's keyless cosign signature (a Sigstore bundle over checksums.txt)
// against Sigstore's public-good trust root, fetched and checked with TUF (cached under
// ~/.sigstore). The certificate must name berth's release workflow at the release's tag (Identity),
// the signature must be in the transparency log, and the certificate must have been valid when it
// signed. No cosign binary is needed.
type Sigstore struct{}

// ErrOldBundle is a release signed before berth's releases used the standard Sigstore bundle
// format (v0.1.0 and v0.2.0): install those by hand (docs/host-install.md).
var ErrOldBundle = errors.New("this release predates the signature format berth upgrade verifies (releases before v0.3.0); install it by hand, as docs/host-install.md shows")

// Verify implements Verifier.
func (s Sigstore) Verify(ctx context.Context, checksums, bundleJSON []byte, tag string) error {
	subject, issuer := Identity(tag)
	return s.VerifyIdentity(ctx, checksums, bundleJSON, subject, issuer)
}

// VerifyIdentity verifies against a given certificate identity (the release workflow at a ref).
func (Sigstore) VerifyIdentity(_ context.Context, checksums, bundleJSON []byte, subject, issuer string) error {
	if bytes.Contains(bundleJSON, []byte(`"base64Signature"`)) {
		return ErrOldBundle // cosign's own, older format
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("reading the signature bundle: %w", err)
	}
	trusted, err := root.FetchTrustedRoot()
	if err != nil {
		return fmt.Errorf("getting Sigstore's trust root: %w", err)
	}
	v, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1), verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return err
	}
	id, err := verify.NewShortCertificateIdentity(issuer, "", subject, "")
	if err != nil {
		return err
	}
	_, err = v.Verify(&b, verify.NewPolicy(verify.WithArtifact(bytes.NewReader(checksums)), verify.WithCertificateIdentity(id)))
	return err
}
