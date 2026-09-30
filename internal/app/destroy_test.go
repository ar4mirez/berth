package app

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
)

// TestDestroyedRecord: destroy's record keeps 1 line per org (the latest), and ls reports only the
// orgs that don't exist again.
func TestDestroyedRecord(t *testing.T) {
	st := t.TempDir()
	var out bytes.Buffer
	a := New(config.State{Home: config.Home{Path: st}}, local.New(), nil, &out, &out, func(string) string { return "" })
	for _, o := range []string{"t-a", "t-b", "t-a"} {
		if err := a.recordDestroyed(o); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(st, "berth", "destroyed"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^t-b \S+Z\nt-a \S+Z\n$`).Match(b) {
		t.Errorf("destroyed:\n%s", b)
	}
	if err := os.MkdirAll(filepath.Join(st, "orgs", "t-b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st, "orgs", "t-b", "org.env"), []byte("MANAGER=berth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := a.destroyedOrgs(); len(got) != 1 || got[0].Name != "t-a" || got[0].Host != "local" {
		t.Errorf("destroyedOrgs = %+v", got)
	}
}
