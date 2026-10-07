package ops

import (
	"strings"
	"testing"
)

// TestPackages: what may be listed, what a list stands for, and the image it names (#106).
func TestPackages(t *testing.T) {
	for _, ok := range []string{"libnss3", "libatk-bridge2.0-0", "g++", "libstdc++6", "fonts-noto-color-emoji", "@playwright-chromium"} {
		if err := CheckPackage(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for bad, want := range map[string]string{
		"@nope":         "unknown preset '@nope' (presets: @playwright-chromium)",
		"LibNSS3":       "isn't a Debian package name",
		"libnss3=1.0":   "isn't a Debian package name",
		"a":             "isn't a Debian package name",
		"x; rm -rf /":   "isn't a Debian package name",
		"-o":            "isn't a Debian package name",
		"lib$(id)":      "isn't a Debian package name",
		"../etc":        "isn't a Debian package name",
		"":              "isn't a Debian package name",
		"libfoo\nRUN x": "isn't a Debian package name",
	} {
		if err := CheckPackage(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", bad, err, want)
		}
	}

	pkgs, bad := ExpandPackages([]string{"zlib1g", "@playwright-chromium", "libnss3", "Bad Name", "zlib1g", "@gone"})
	if len(pkgs) != 31 || pkgs[0] != "fonts-freefont-ttf" || pkgs[len(pkgs)-1] != "zlib1g" || strings.Join(bad, ",") != "Bad Name,@gone" {
		t.Errorf("expand: %d packages (%v … %v), bad %v", len(pkgs), pkgs[0], pkgs[len(pkgs)-1], bad)
	}

	const base = "berth/claude-env:0123456789ab"
	if got := OrgImage(base, "acme", nil); got != base {
		t.Errorf("no packages: %s", got)
	}
	a, b := OrgImage(base, "acme", []string{"libnss3"}), OrgImage(base, "acme", []string{"libnss3", "zlib1g"})
	if !strings.HasPrefix(a, "berth/claude-env-acme:0123456789ab-") || len(a) != len("berth/claude-env-acme:0123456789ab-")+12 || a == b {
		t.Errorf("org images: %s, %s", a, b)
	}
	if OrgImage("berth/claude-env:ffffffffffff", "acme", []string{"libnss3"}) == a || OrgImage(base, "globex", []string{"libnss3"}) == a {
		t.Error("another base, or another org, is the same image")
	}
	df := OrgDockerfile(base, "acme", []string{"libnss3", "zlib1g"})
	for _, want := range []string{"FROM " + base + "\n", "ARG USER_UID\nARG USER_GID\n", "USER root\n",
		"apt-get install -y --no-install-recommends \\\n      libnss3 zlib1g \\\n && rm -rf /var/lib/apt/lists/*\n"} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, df)
		}
	}
}
