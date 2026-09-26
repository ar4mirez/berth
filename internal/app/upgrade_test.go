package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
	"github.com/ar4mirez/berth/internal/upgrade/upgradetest"
)

// fakeBerth is a stand-in binary: it reports its version, its image tag, and links itself on install.
func fakeBerth(version, tag string) []byte {
	return []byte(`#!/bin/sh
case "$1" in
  --version) echo "berth ` + version + ` (commit x, built y)" ;;
  image-tag) echo "` + tag + `" ;;
  install) mkdir -p "$2" && ln -sfn "$0" "$2/berth" && echo "Linked $2/berth -> $0" ;;
esac
`)
}

// TestUpgrade: berth upgrade installs a verified release next to the current one and moves the
// link; --rollback moves it back; a bad signature, a bad checksum or a binary that doesn't run
// change nothing (#42).
func TestUpgrade(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	opt := filepath.Join(home, ".local", "opt", "berth")
	bin := filepath.Join(home, ".local", "bin")
	cur := filepath.Join(opt, "0.2.0", "berth")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Dir(cur), 0o755))
	must(os.WriteFile(cur, fakeBerth("0.2.0", "berth/claude-env:oldtag"), 0o755))
	must(os.MkdirAll(bin, 0o755))
	must(os.Symlink(cur, filepath.Join(bin, "berth")))

	newApp := func(f *upgradetest.FakeRelease, v *upgradetest.FakeVerifier) (*App, *bytes.Buffer) {
		var out bytes.Buffer
		st := config.State{Home: config.Home{Path: filepath.Join(home, "state")}}
		a := New(st, local.New(), nil, &out, &out, func(k string) string {
			if k == "HOME" {
				return home
			}
			return ""
		})
		a.Self, a.Invoked = cur, filepath.Join(bin, "berth")
		a.Upgrader = &Upgrader{Client: f.Start(t, runtime.GOOS, runtime.GOARCH), Verifier: v}
		return a, &out
	}
	link := func() string { l, _ := os.Readlink(filepath.Join(bin, "berth")); return l }
	next := filepath.Join(opt, "9.9.9", "berth")

	// A signature that doesn't verify: nothing is installed, the link stays.
	a, out := newApp(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("9.9.9", "x")}, &upgradetest.FakeVerifier{Reject: true})
	if err := a.Upgrade(ctx, nil); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("bad signature: %v\n%s", err, out)
	}
	// A tampered archive: the same.
	a, _ = newApp(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("9.9.9", "x"), Tamper: true}, &upgradetest.FakeVerifier{})
	if err := a.Upgrade(ctx, nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("bad checksum: %v", err)
	}
	// A binary that isn't the version it claims: removed, the link stays.
	a, _ = newApp(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("1.0.0", "x")}, &upgradetest.FakeVerifier{})
	if err := a.Upgrade(ctx, nil); err == nil || !strings.Contains(err.Error(), "doesn't run as v9.9.9") {
		t.Errorf("wrong version: %v", err)
	}
	if _, err := os.Stat(next); err == nil || link() != cur {
		t.Fatalf("a refused upgrade changed things: %s exists, link -> %s", next, link())
	}

	// The upgrade.
	a, out = newApp(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("9.9.9", "berth/claude-env:newtag")}, &upgradetest.FakeVerifier{})
	must(a.Upgrade(ctx, nil))
	if fi, err := os.Stat(next); err != nil || fi.Mode().Perm() != 0o755 || link() != next {
		t.Fatalf("after upgrade: %v, link -> %s\n%s", err, link(), out)
	}
	if b, _ := os.ReadFile(filepath.Join(opt, ".previous")); strings.TrimSpace(string(b)) != cur {
		t.Errorf(".previous: %q", b)
	}
	for _, want := range []string{"Upgraded to v9.9.9", "upgrade --rollback", "The container image changes", "Nothing was restarted"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// Rollback, from the new version.
	a, out = newApp(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("9.9.9", "x")}, &upgradetest.FakeVerifier{})
	a.Self = next
	must(a.Upgrade(ctx, []string{"--rollback"}))
	if link() != cur || !strings.Contains(out.String(), "Rolled back to "+cur) {
		t.Errorf("rollback: link -> %s\n%s", link(), out)
	}
	if b, _ := os.ReadFile(filepath.Join(opt, ".previous")); strings.TrimSpace(string(b)) != next {
		t.Errorf(".previous after rollback: %q (the undo of the undo)", b)
	}

	// --read-only refuses.
	a, _ = newApp(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("9.9.9", "x")}, &upgradetest.FakeVerifier{})
	a.State.ReadOnly = true
	if err := a.Upgrade(ctx, nil); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("--read-only: %v", err)
	}
}
