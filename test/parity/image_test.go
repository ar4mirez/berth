package parity

import (
	"slices"
	"strings"
	"testing"
)

// Released images (#41): a berth with a published image pulls it (and tags it as its local name)
// instead of building, and falls back to building when the pull fails. Development builds (no
// published image) behave exactly as before: the parity suite. BERTH_PUBLISHED_IMAGE stands in for
// the digest a release embeds.
func TestBerthPublishedImage(t *testing.T) {
	const img = "ghcr.io/acme/berth-image@sha256:0123456789abcdef"
	owned := withFile(twoOrgs, "state/orgs/acme/org.env", File{Content: "MANAGER=berth\n" + acmeEnv})
	env := []string{"BERTH_PUBLISHED_IMAGE=" + img}
	missing := Rule{Bin: "docker", Match: `^image inspect berth/claude-env:`, Exit: 1, Once: true}
	callsOf := func(r Result) []string {
		var out []string
		for _, c := range r.Calls {
			if strings.HasPrefix(c, `docker "pull"`) || strings.HasPrefix(c, `docker "tag"`) || strings.HasPrefix(c, `docker "compose"`) || strings.HasPrefix(c, `docker "image" "inspect"`) {
				out = append(out, c)
			}
		}
		return out
	}
	has := func(calls []string, prefix string) bool {
		return slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
	}
	raw := BerthUnowned(berthBin)

	// Missing: pulled and tagged, then compose runs without --build.
	r := run(t, raw, Scenario{Args: []string{"up", "acme"}, Env: env, Files: owned, Rules: running("acme", missing)})
	c := callsOf(r)
	if r.Exit != 0 || !has(c, `docker "pull" "`+img+`"`) || !has(c, `docker "tag" "`+img+`" "berth/claude-env:`) ||
		!slices.ContainsFunc(c, func(s string) bool {
			return strings.Contains(s, `"up" "-d" "--force-recreate"`) && !strings.Contains(s, "--build")
		}) {
		t.Errorf("up, image missing: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(c, "\n"))
	}
	// Present: no pull, and still no --build.
	r = run(t, raw, Scenario{Args: []string{"up", "acme"}, Env: env, Files: owned, Rules: running("acme")})
	c = callsOf(r)
	if r.Exit != 0 || has(c, `docker "pull"`) || slices.ContainsFunc(c, func(s string) bool { return strings.Contains(s, "--build") }) {
		t.Errorf("up, image present:\n%s", strings.Join(c, "\n"))
	}
	// The pull fails (offline): it says so, and builds as before.
	r = run(t, raw, Scenario{Args: []string{"up", "acme"}, Env: env, Files: owned,
		Rules: running("acme", missing, Rule{Bin: "docker", Match: `^pull `, Stderr: "network unreachable\n", Exit: 1})})
	c = callsOf(r)
	if r.Exit != 0 || !strings.Contains(r.Stderr, "couldn't pull it; building the image locally instead") ||
		!slices.ContainsFunc(c, func(s string) bool { return strings.Contains(s, `"up" "-d" "--build" "--force-recreate"`) }) {
		t.Errorf("up, pull fails: exit %d, stderr %q\n%s", r.Exit, r.Stderr, strings.Join(c, "\n"))
	}
	// restart pulls a missing image first; its compose call is unchanged.
	r = run(t, raw, Scenario{Args: []string{"restart", "acme"}, Env: env, Files: owned, Rules: running("acme", missing)})
	if r.Exit != 0 || !has(callsOf(r), `docker "pull" "`+img+`"`) {
		t.Errorf("restart: exit %d\n%s", r.Exit, strings.Join(r.Calls, "\n"))
	}

	// berth pull: gets the image, restarts nothing.
	r = run(t, raw, Scenario{Args: []string{"pull"}, Env: env, Files: owned, Rules: []Rule{missing}})
	c = callsOf(r)
	if r.Exit != 0 || !has(c, `docker "pull"`) || has(c, `docker "compose"`) || !strings.Contains(r.Stdout, "Nothing was restarted") {
		t.Errorf("pull: exit %d, stdout %q\n%s", r.Exit, r.Stdout, strings.Join(c, "\n"))
	}
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{nil, "has no released image (a development build"},
		{[]string{"BERTH_PUBLISHED_IMAGE=none"}, "has no released image (a development build"},
	} {
		if r = run(t, raw, Scenario{Args: []string{"pull"}, Env: tc.env, Files: owned}); r.Exit != 1 || !strings.Contains(r.Stderr, tc.want) {
			t.Errorf("pull %v: exit %d, stderr %q", tc.env, r.Exit, r.Stderr)
		}
	}
	if r = run(t, raw, Scenario{Args: []string{"--read-only", "pull"}, Env: env, Files: owned}); r.Exit != 1 || len(r.Calls) != 0 {
		t.Errorf("--read-only pull: exit %d, calls %v", r.Exit, r.Calls)
	}

	// A development build: up builds, with no pull, exactly as ccenv does.
	r = run(t, raw, Scenario{Args: []string{"up", "acme"}, Files: owned, Rules: running("acme", missing)})
	c = callsOf(r)
	if has(c, `docker "pull"`) || !slices.ContainsFunc(c, func(s string) bool { return strings.Contains(s, `"--build"`) }) {
		t.Errorf("development build:\n%s", strings.Join(c, "\n"))
	}
}
