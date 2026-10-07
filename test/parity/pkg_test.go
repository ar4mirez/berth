package parity

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	pkgFile   = "state/orgs/acme/config/packages.txt"
	pkgHeader = "# System packages installed in acme's image, managed with: <tool> pkg acme add|rm ...\\n# One Debian package or @preset per line. Presets: <tool> pkg acme presets\\n"
)

var (
	orgTag      = `berth/claude-env-acme:[0-9a-f]{12}-[0-9a-f]{12}`
	orgBuild    = regexp.MustCompile(`^docker "build" "-t" "` + orgTag + `" "<RUN>/state/berth-orgs/acme"$`)
	composeOrg  = regexp.MustCompile(`\[CLAUDE_ENV_IMAGE="berth/claude-env-acme"\]  \[CLAUDE_ENV_IMAGE_DIR="<RUN>/state/berth-orgs/acme"\].*\[IMAGE_TAG="[0-9a-f]{12}-[0-9a-f]{12}"\]`)
	composeBase = regexp.MustCompile(`\[CLAUDE_ENV_IMAGE="berth/claude-env"\]  \[CLAUDE_ENV_IMAGE_DIR="<ROOT>/image"\].*\[IMAGE_TAG="[0-9a-f]{12}"\]`)
)

func callsMatching(r Result, re *regexp.Regexp) int {
	n := 0
	for _, c := range r.Calls {
		if re.MatchString(c) {
			n++
		}
	}
	return n
}

// TestBerthPkg: `berth pkg` (#106, berth-only). An org lists the system packages it needs; berth
// builds an image for it on top of its own and compose runs the org on that image. Nothing is
// restarted, a bad name is refused before anything is written, and a build that fails puts the list
// back.
func TestBerthPkg(t *testing.T) {
	raw := allCalls() // the image names as they are
	missing := Rule{Bin: "docker", Match: `^image inspect berth/claude-env-acme:`, Exit: 1}

	// add: the list is written, the org's image is built from a generated Dockerfile, nothing restarts.
	r := run(t, raw, Scenario{Args: []string{"pkg", "acme", "add", "libnss3", "@playwright-chromium", "libnss3"}, Files: twoOrgs, Rules: running("acme", missing)})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "added: libnss3\nadded: @playwright-chromium\nalready listed: libnss3\n") ||
		!regexp.MustCompile(orgTag+` is ready \(30 packages on berth/claude-env:[0-9a-f]{12}\)\. Nothing was restarted; it applies with: <tool> restart acme \(that stops the work running in it\)\n$`).MatchString(r.Stdout) {
		t.Errorf("add: exit %d\nstdout %q\nstderr %q", r.Exit, r.Stdout, r.Stderr)
	}
	if want := pkgFile + ` 0644 "` + pkgHeader + `libnss3\n@playwright-chromium\n"`; treeLine(r, pkgFile) != want {
		t.Errorf("packages.txt: %s\nwant %s", treeLine(r, pkgFile), want)
	}
	df := treeLine(r, "state/berth-orgs/acme/Dockerfile")
	if !regexp.MustCompile(`FROM berth/claude-env:[0-9a-f]{12}\\n`).MatchString(df) || !strings.Contains(df, "fonts-freefont-ttf fonts-ipafont-gothic") || !strings.Contains(df, " libnss3 ") {
		t.Errorf("Dockerfile: %s", df)
	}
	if callsMatching(r, orgBuild) != 1 || callsMatching(r, regexp.MustCompile(`"compose"|"restart"|"rm"`)) != 0 {
		t.Errorf("add: one build of the org's image, and no compose:\n%s", strings.Join(r.Calls, "\n"))
	}
	withPkgs := merge(twoOrgs, map[string]File{pkgFile: {Content: "libnss3\n", Mode: 0o644}})

	// A name apt must never see, or an unknown preset: refused, and nothing is written or run.
	for arg, want := range map[string]string{"x;id": "isn't a Debian package name", "@nope": "unknown preset '@nope'"} {
		r = run(t, raw, Scenario{Args: []string{"pkg", "acme", "add", "zlib1g", arg}, Files: twoOrgs, Rules: running("acme")})
		if r.Exit != 1 || !strings.Contains(r.Stderr, want) || treeLine(r, pkgFile) != "" || callsMatching(r, regexp.MustCompile(`"build"`)) != 0 {
			t.Errorf("%s: exit %d, stderr %q, file %q", arg, r.Exit, r.Stderr, treeLine(r, pkgFile))
		}
	}

	// A build that fails (no such package): the list is as it was.
	r = run(t, raw, Scenario{Args: []string{"pkg", "acme", "add", "libnope"}, Files: withPkgs, Rules: running("acme", missing,
		Rule{Bin: "docker", Match: `^build -t berth/claude-env-acme:`, Stderr: "E: Unable to locate package libnope\n", Exit: 100})})
	if r.Exit != 100 || !strings.HasSuffix(r.Stderr, "<tool>: the image didn't build, so acme's packages are as they were (the build's output above says why)\n") || treeLine(r, pkgFile) != pkgFile+` 0644 "libnss3\n"` {
		t.Errorf("failed build: exit %d, stderr %q, file %s", r.Exit, r.Stderr, treeLine(r, pkgFile))
	}

	// --no-build only saves; rm takes an entry out, and the last one leaves berth's own image.
	r = run(t, raw, Scenario{Args: []string{"pkg", "globex", "add", "zlib1g", "--no-build"}, Files: twoOrgs})
	if r.Exit != 0 || !strings.HasSuffix(r.Stdout, "Saved. globex's image is built at its next start, or now with: <tool> pkg globex build\n") || callsMatching(r, regexp.MustCompile(`^docker`)) != 0 {
		t.Errorf("--no-build: exit %d, stdout %q\n%s", r.Exit, r.Stdout, strings.Join(r.Calls, "\n"))
	}
	r = run(t, raw, Scenario{Args: []string{"pkg", "acme", "rm", "libnss3", "zlib1g"}, Files: withPkgs, Rules: running("acme")})
	if r.Exit != 0 || !strings.HasPrefix(r.Stdout, "removed: libnss3\nnot in the list: zlib1g\nacme has no extra packages: it runs on berth/claude-env:") || treeLine(r, pkgFile) != pkgFile+` 0644 ""` {
		t.Errorf("rm: exit %d, stdout %q, file %s", r.Exit, r.Stdout, treeLine(r, pkgFile))
	}

	// up and restart run the org on its image, building it first when it isn't there; an org
	// without packages runs on berth's, with no call it didn't make before.
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: withPkgs, Rules: running("acme", missing)})
	build, compose := slices.IndexFunc(r.Calls, orgBuild.MatchString), slices.IndexFunc(r.Calls, func(c string) bool { return strings.Contains(c, `"compose"`) })
	if r.Exit != 0 || build < 0 || compose < build || !composeOrg.MatchString(r.Calls[compose]) {
		t.Errorf("restart with packages: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: withPkgs, Rules: running("acme")}) // already built
	if r.Exit != 0 || callsMatching(r, orgBuild) != 0 || callsMatching(r, composeOrg) != 1 {
		t.Errorf("restart, image there: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}
	r = run(t, raw, Scenario{Args: []string{"down", "acme"}, Files: withPkgs, Rules: running("acme", missing)})
	if r.Exit != 0 || callsMatching(r, orgBuild) != 0 || callsMatching(r, composeOrg) != 1 {
		t.Errorf("down builds nothing, and names the org's image: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Files: twoOrgs, Rules: running("acme")})
	if r.Exit != 0 || callsMatching(r, composeBase) != 1 || callsMatching(r, regexp.MustCompile(`claude-env-acme|"build"`)) != 0 {
		t.Errorf("restart without packages: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}

	// Reading: the list and its image; a ccenv-managed or read-only run changes nothing.
	r = run(t, raw, Scenario{Args: []string{"pkg", "acme"}, Files: withPkgs, Rules: running("acme", missing)})
	if r.Exit != 0 || !regexp.MustCompile(`^<RUN>/`+pkgFile+`\n  libnss3\n1 packages, in `+orgTag+` \(not built yet: <tool> pkg acme build, or the org's next start builds it\)\n$`).MatchString(r.Stdout) {
		t.Errorf("ls: exit %d, stdout %q", r.Exit, r.Stdout)
	}
	r = run(t, raw, Scenario{Args: []string{"pkg", "globex"}, Files: twoOrgs})
	if r.Exit != 0 || r.Stdout != "(no extra packages; add some: <tool> pkg globex add <package|@preset>)\n" {
		t.Errorf("ls, none: exit %d, stdout %q", r.Exit, r.Stdout)
	}
	r = run(t, raw, Scenario{Args: []string{"--read-only", "pkg", "acme", "add", "zlib1g"}, Files: twoOrgs})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "read-only") || treeLine(r, pkgFile) != "" {
		t.Errorf("--read-only: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}
