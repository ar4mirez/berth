//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/ar4mirez/berth/internal/upgrade"
)

// TestReleaseSignature: berth's verifier (#42) against real releases. v0.2.0 was signed in cosign's
// older bundle format, which berth upgrade refuses with a clear message; releases from v0.3.0 use
// the standard Sigstore bundle, and the release workflow runs this same verifier on each one
// (tools/verifyrelease) before it ships.
func TestReleaseSignature(t *testing.T) {
	ctx := context.Background()
	c := upgrade.NewClient()
	rel, err := c.Get(ctx, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	sums, err := c.Download(ctx, rel, "checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := c.Download(ctx, rel, "checksums.txt.sigstore.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := (upgrade.Sigstore{}).Verify(ctx, sums, bundle, "v0.2.0"); !errors.Is(err, upgrade.ErrOldBundle) {
		t.Errorf("v0.2.0: %v, want ErrOldBundle", err)
	}
}
