package app

import (
	"os"
	"strings"
	"testing"
)

// TestFwEntry: fw allow takes what init-firewall.sh can use, and says why for the rest (#104).
func TestFwEntry(t *testing.T) {
	for _, e := range []string{"example.com", "a.b-c.example.com", "*.example.com", "localhost", "_dmarc.example.com",
		"EXAMPLE.com", "10.0.0.1", "10.0.0.0/8", "192.168.1.7/32", "@node", "@my-preset"} {
		if err := fwEntry(e, e); err != nil {
			t.Errorf("%s: refused: %v", e, err)
		}
	}
	for e, want := range map[string]string{
		"web-git-*.example.app": "a wildcard only works as a leading '*.'",
		"*":                     "a wildcard only works as a leading '*.'",
		"*.*.example.com":       "a wildcard only works as a leading '*.'",
		"*example.com":          "a wildcard only works as a leading '*.'",
		"":                      "has no host to allow",
		"999.1.1.1":             "isn't an IPv4 address or range",
		"10.0.0.0/33":           "isn't an IPv4 address or range",
		"example..com":          "isn't a hostname",
		"-example.com":          "isn't a hostname",
		"example.com.":          "isn't a hostname",
		"mode off":              "isn't a hostname",
		"@":                     "isn't a hostname",
		"exa$mple.com":          "isn't a hostname",
	} {
		err := fwEntry(e, e)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error with %q", e, err, want)
		}
	}
}

// TestFwEntryMatchesTheImage: fw allow and init-firewall.sh accept the same names.
func TestFwEntryMatchesTheImage(t *testing.T) {
	b, err := os.ReadFile("../../image/init-firewall.sh")
	if err != nil {
		t.Fatal(err)
	}
	const label = `[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?`
	if !strings.Contains(string(b), "LABEL='"+label+"'\n") || !strings.Contains(string(b), `NAME_RE="^(\\*\\.)?$LABEL(\\.$LABEL)*\$"`) {
		t.Error("init-firewall.sh's LABEL or NAME_RE changed: keep fwName the same")
	}
	if want := `^(\*\.)?` + label + `(\.` + label + `)*$`; fwName.String() != want {
		t.Errorf("fwName is %s, want %s", fwName, want)
	}
}
