package upgrade_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/upgrade"
	"github.com/ar4mirez/berth/internal/upgrade/upgradetest"
)

func TestFetch(t *testing.T) {
	ctx := context.Background()
	f := &upgradetest.FakeRelease{Tag: "v9.9.9", Binary: []byte("#!/bin/sh\necho hi\n")}
	c := f.Start(t, "linux", "amd64")
	rel, err := c.Get(ctx, "")
	if err != nil || rel.Tag != "v9.9.9" {
		t.Fatalf("latest: %+v, %v", rel, err)
	}
	v := &upgradetest.FakeVerifier{}
	bin, err := upgrade.Fetch(ctx, c, v, rel, "linux", "amd64")
	if err != nil || string(bin) != "#!/bin/sh\necho hi\n" || len(v.Tags) != 1 || v.Tags[0] != "v9.9.9" {
		t.Fatalf("fetch: %q, %v, verifier saw %v", bin, err, v.Tags)
	}

	// A signature that doesn't verify: the archive isn't even downloaded.
	f.Requests = nil
	if _, err := upgrade.Fetch(ctx, c, &upgradetest.FakeVerifier{Reject: true}, rel, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "doesn't verify") {
		t.Errorf("rejected signature: %v", err)
	}
	for _, r := range f.Requests {
		if strings.HasSuffix(r, ".tar.gz") {
			t.Error("the archive was downloaded despite a bad signature")
		}
	}

	// An archive that doesn't match its checksum.
	bad := &upgradetest.FakeRelease{Tag: "v9.9.9", Binary: []byte("x"), Tamper: true}
	bc := bad.Start(t, "linux", "amd64")
	rel, _ = bc.Get(ctx, "v9.9.9")
	if _, err := upgrade.Fetch(ctx, bc, &upgradetest.FakeVerifier{}, rel, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "doesn't match its checksum") {
		t.Errorf("tampered archive: %v", err)
	}
	// No archive for this platform.
	if _, err := upgrade.Fetch(ctx, c, &upgradetest.FakeVerifier{}, rel, "plan9", "mips"); err == nil {
		t.Error("a missing platform must fail")
	}
}

func TestIdentity(t *testing.T) {
	s, i := upgrade.Identity("v1.2.3")
	if s != "https://github.com/ar4mirez/berth/.github/workflows/release.yml@refs/tags/v1.2.3" || i != "https://token.actions.githubusercontent.com" {
		t.Errorf("%s %s", s, i)
	}
}
