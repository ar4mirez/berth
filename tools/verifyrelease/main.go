// Command verifyrelease runs berth's own release verifier (internal/upgrade, what `berth upgrade`
// uses) on a freshly signed checksums.txt in the release workflow, so a release only ships if
// berth itself accepts its signature, and rejects a tampered copy (#42).
//
//	verifyrelease <checksums.txt> <checksums.txt.sigstore.json> <certificate identity>
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/ar4mirez/berth/internal/upgrade"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verifyrelease <checksums.txt> <bundle> <identity>")
		os.Exit(2)
	}
	sums, err := os.ReadFile(os.Args[1]) // #nosec G703 -- a CI tool: reads the files it is given
	if err != nil {
		fail(err)
	}
	bundle, err := os.ReadFile(os.Args[2]) // #nosec G703 -- a CI tool: reads the files it is given
	if err != nil {
		fail(err)
	}
	ctx, v := context.Background(), upgrade.Sigstore{}
	_, issuer := upgrade.Identity("")
	if err := v.VerifyIdentity(ctx, sums, bundle, os.Args[3], issuer); err != nil {
		fail(fmt.Errorf("berth's verifier rejects the release signature: %w", err))
	}
	if v.VerifyIdentity(ctx, append(bytes.Clone(sums), '\n', 'x'), bundle, os.Args[3], issuer) == nil {
		fail(fmt.Errorf("berth's verifier accepted a modified checksums.txt"))
	}
	fmt.Println("berth's verifier: the signature verifies, and a modified checksums.txt doesn't")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
