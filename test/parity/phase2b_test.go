package parity

import (
	"regexp"
	"strings"
	"testing"
)

// Phase 2b: up and build.

const sevenLogLines = "entrypoint: 1\nentrypoint: 2\nentrypoint: 3\nfirewall: on\nsshd: listening\nttyd: listening\nremote-control: started\n"

func init() {
	more := []checked{
		{Scenario{Name: "up", Args: []string{"up", "acme"}, Files: twoOrgs, Rules: running("acme",
			Rule{Bin: "docker", Match: `^logs claude-acme$`, Stdout: sevenLogLines, Stderr: "warning on stderr\n"})},
			0, []string{"remote-control: started"}},
		{Scenario{Name: "up, no trailing newline in logs", Args: []string{"up", "globex"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^logs claude-globex$`, Stdout: "a\nb\nc"},
		}}, 0, []string{"c"}},
		{Scenario{Name: "up, compose fails", Args: []string{"up", "globex"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^compose `, Stderr: "Error: build failed\n", Exit: 17},
		}}, 17, []string{"build failed"}},
		{Scenario{Name: "up, docker logs fails", Args: []string{"up", "globex"}, Files: twoOrgs, Rules: []Rule{
			{Bin: "docker", Match: `^logs claude-globex$`, Stderr: "Error: No such container: claude-globex\n", Exit: 1},
		}}, 1, []string{"No such container"}},
		{Scenario{Name: "up, unknown org", Args: []string{"up", "nope"}, Files: twoOrgs}, 1, []string{"unknown org"}},
		{Scenario{Name: "build", Args: []string{"build"}}, 0, nil},
		{Scenario{Name: "build, args passed through", Args: []string{"build", "--no-cache", "--progress=plain"}, Rules: []Rule{
			{Bin: "docker", Match: `^build `, Stdout: "#1 building\n", Exit: 3},
		}}, 3, []string{"#1 building"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
}

var hex12 = `[0-9a-f]{12}`

// TestBerthAssets checks, without the harness's normalization, what berth really does with its
// image: materialize image/ and compose.yml in <state>/berth (modes from git), build and run
// berth/claude-env:<hash>, and never tag claude-env.
func TestBerthAssets(t *testing.T) {
	raw := BerthUnowned(berthBin) // no normalization of the image or the assets dir
	raw.Prepare = Berth(berthBin).Prepare

	b := run(t, raw, Scenario{Args: []string{"build", "--no-cache"}})
	if len(b.Calls) != 1 || !regexp.MustCompile(`^docker "build" "-t" "berth/claude-env:`+hex12+`" "--build-arg" "USER_UID=\d+" "--build-arg" "USER_GID=\d+" "--no-cache" "<RUN>/state/berth/image"$`).MatchString(b.Calls[0]) {
		t.Errorf("build call: %v", b.Calls)
	}
	tree := strings.Join(b.Tree, "\n")
	for _, want := range []string{
		"state/berth/ 0755", "state/berth/.berth-assets 0644", "state/berth/compose.yml 0644",
		"state/berth/image/entrypoint.sh 0755", "state/berth/image/archive.sh 0755", "state/berth/image/Dockerfile 0644",
		"state/berth/image/repo-guard.js 0644",
	} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree lacks %q", want)
		}
	}

	u := run(t, raw, Scenario{Args: []string{"up", "globex"}, Files: twoOrgs})
	var compose string
	for _, c := range u.Calls {
		if strings.HasPrefix(c, `docker "compose"`) {
			compose = c
		}
	}
	re := regexp.MustCompile(`^docker "compose" "-f" "<RUN>/state/berth/compose.yml" "--env-file" "<RUN>/state/orgs/globex/org.env" "up" "-d" "--build" "--force-recreate"  \[BIND_ADDR="127.0.0.1"\]  \[CLAUDE_ENV_IMAGE="berth/claude-env"\]  \[CLAUDE_ENV_IMAGE_DIR="<RUN>/state/berth/image"\]  \[HOST_GID="\d+"\]  \[HOST_UID="\d+"\]  \[IMAGE_TAG="` + hex12 + `"\]  \[ORG="globex"\]  \[ORG_DIR="<RUN>/state/orgs/globex"\]$`)
	if !re.MatchString(compose) {
		t.Errorf("up's compose call:\n%s", compose)
	}
	if tag := regexp.MustCompile(`berth/claude-env:(` + hex12 + `)`).FindStringSubmatch(b.Calls[0]); tag == nil || !strings.Contains(compose, `IMAGE_TAG="`+tag[1]+`"`) {
		t.Errorf("build and up use different tags: %v / %s", b.Calls, compose)
	}
	for _, c := range append(b.Calls, u.Calls...) {
		if strings.Contains(c, `"claude-env"`) || strings.Contains(c, "claude-env:latest") {
			t.Errorf("berth used ccenv's image name: %s", c)
		}
	}

	// A <state>/berth that isn't berth's is refused, not overwritten.
	f := run(t, raw, Scenario{Args: []string{"build"}, Files: map[string]File{"state/berth/notes.txt": {Content: "mine\n"}}})
	if f.Exit != 1 || !strings.Contains(f.Stderr, "isn't berth's") || len(f.Calls) != 0 || !strings.Contains(strings.Join(f.Tree, "\n"), `state/berth/notes.txt 0600 "mine\n"`) {
		t.Errorf("foreign assets dir: exit %d, stderr %q, calls %v", f.Exit, f.Stderr, f.Calls)
	}

	// Under --read-only nothing is written: logs uses a ccenv checkout's own compose.yml, without the
	// image variables.
	ro := raw
	ro.Command = func(run string, args []string) []string {
		argv := raw.Command(run, args)
		return append([]string{argv[0], "--read-only"}, argv[1:]...)
	}
	l := run(t, ro, Scenario{Args: []string{"logs", "globex"}, Files: merge(twoOrgs, map[string]File{"state/compose.yml": {Content: "name: x\n", Mode: 0o644}})})
	if len(l.Calls) != 1 || !strings.HasPrefix(l.Calls[0], `docker "compose" "-f" "<RUN>/state/compose.yml"`) || strings.Contains(l.Calls[0], "CLAUDE_ENV_IMAGE") {
		t.Errorf("read-only logs: %v", l.Calls)
	}
	if strings.Contains(strings.Join(l.Tree, "\n"), "state/berth") {
		t.Error("read-only logs wrote the assets")
	}
}
