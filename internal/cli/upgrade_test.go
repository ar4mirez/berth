package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/upgrade"
	"github.com/ar4mirez/berth/internal/upgrade/upgradetest"
	"github.com/ar4mirez/berth/internal/version"
)

// TestUpgraderIsWired: the App every command gets can fetch berth's releases from GitHub and verify
// them with Sigstore. Nothing set it before, so `berth upgrade` never got past "upgrade isn't
// available in this build" in any released binary (#81).
func TestUpgraderIsWired(t *testing.T) {
	u := newUpgrader()
	if u == nil || u.Client == nil || u.Verifier == nil {
		t.Fatalf("newUpgrader: %+v", u)
	}
	if want := "https://api.github.com/repos/" + upgrade.Repo; u.Client.API != want || u.Client.HTTP == nil {
		t.Errorf("client: %+v, want API %s", u.Client, want)
	}
	if _, ok := u.Verifier.(upgrade.Sigstore); !ok {
		t.Errorf("verifier is %T, not Sigstore", u.Verifier)
	}
}

// fakeBerth is a release's "berth": it answers what upgrade asks of a new binary.
func fakeBerth(ver string) []byte {
	return []byte("#!/bin/sh\ncase \"$1\" in\n  --version) echo \"berth " + ver + " (commit x, built y)\" ;;\n  image-tag) echo berth/claude-env:000000000000 ;;\n" +
		"  install) echo \"installed $0\" > \"$HOME/installed\" ;;\nesac\n")
}

// TestUpgradeThroughTheCLI runs `berth upgrade` as the command line does, against a fake release:
// the test that would have caught #81, where only App.Upgrade with an injected Upgrader was tested.
func TestUpgradeThroughTheCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake release is a shell script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	state := filepath.Join(home, "state")
	serve := func(f *upgradetest.FakeRelease, v *upgradetest.FakeVerifier) {
		client := f.Start(t, runtime.GOOS, runtime.GOARCH)
		old := newUpgrader
		newUpgrader = func() *app.Upgrader { return &app.Upgrader{Client: client, Verifier: v} }
		t.Cleanup(func() { newUpgrader = old })
	}

	// A newer release: fetched, verified, installed next to the others, and linked by its own install.
	serve(&upgradetest.FakeRelease{Tag: "v9.9.9", Binary: fakeBerth("9.9.9")}, &upgradetest.FakeVerifier{})
	out, errOut, code := run(t, "--home", state, "upgrade")
	installed := filepath.Join(home, ".local", "opt", "berth", "9.9.9", "berth")
	if code != 0 || errOut != "" || !strings.Contains(out, "Upgraded to v9.9.9 ("+installed+")") {
		t.Fatalf("upgrade: code %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "isn't available in this build") {
		t.Error("the upgrader isn't wired (#81)")
	}
	if b, err := os.ReadFile(installed); err != nil || string(b) != string(fakeBerth("9.9.9")) {
		t.Errorf("the release's binary isn't at %s: %v", installed, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "installed")); !strings.Contains(string(b), installed) {
		t.Errorf("the new binary's install didn't run: %q", b)
	}

	// The same through the group (`system upgrade`), pinned to a version: here, the one running.
	cur := "v" + strings.TrimPrefix(version.Version, "v")
	serve(&upgradetest.FakeRelease{Tag: cur, Binary: fakeBerth(strings.TrimPrefix(cur, "v"))}, &upgradetest.FakeVerifier{})
	out, errOut, code = run(t, "--home", state, "system", "upgrade", "--version", cur)
	if code != 0 || errOut != "" || out != "berth is already at "+cur+".\n" {
		t.Errorf("already current: code %d, stdout %q, stderr %q", code, out, errOut)
	}

	// A signature that doesn't verify: an error, and nothing installed.
	serve(&upgradetest.FakeRelease{Tag: "v9.9.10", Binary: fakeBerth("9.9.10")}, &upgradetest.FakeVerifier{Reject: true})
	_, errOut, code = run(t, "--home", state, "upgrade")
	if code != 1 || !strings.Contains(errOut, "nothing was changed") {
		t.Errorf("bad signature: code %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "opt", "berth", "9.9.10")); err == nil {
		t.Error("a release whose signature doesn't verify was installed")
	}

	// --read-only refuses before anything is fetched.
	if _, errOut, code = run(t, "--home", state, "--read-only", "upgrade"); code != 1 || !strings.Contains(errOut, "read-only") {
		t.Errorf("--read-only: code %d, stderr %q", code, errOut)
	}
}
