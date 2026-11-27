package hosts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeIptables is iptables-nft as guard.sh uses it, with its rules in $FAKE_RULES: chains (-N),
// and jumps and rules (-A). iptables-nft-restore takes the chains a restore declares; with
// FAKE_SLOW it takes a second first, as a busy host's would.
const fakeIptables = `#!/usr/bin/env bash
r="$FAKE_RULES"; touch "$r"
case "$(basename "$0")" in
  *-restore)
    [ -n "${FAKE_SLOW:-}" ] && sleep 1
    while read -r l; do case "$l" in :*) c=${l#:}; c=${c%% *}; grep -qx -- "-N $c" "$r" || echo "-N $c" >> "$r" ;; esac; done
    exit 0 ;;
  iptables-legacy) exit 1 ;;
esac
args=(); for a in "$@"; do [ "$a" = -w ] || args+=("$a"); done
set -- "${args[@]}"
case "$1" in
  -S) if [ -n "${2:-}" ]; then grep -q -- "^-N $2$\|^-A $2 " "$r" || [ "$2" = DOCKER-USER ] || exit 1; fi; cat "$r" ;;
  -C) grep -qx -- "-A $2 -j $4" "$r" ;;
  -I) echo "-A $2 -j $5" >> "$r" ;;
  -D) grep -qx -- "-A $2 -j $4" "$r" || exit 1; grep -vx -- "-A $2 -j $4" "$r" > "$r.t"; mv "$r.t" "$r" ;;
  -F) grep -qx -- "-N $2" "$r" ;;
  -X) grep -vx -- "-N $2" "$r" > "$r.t"; mv "$r.t" "$r" ;;
esac
`

// fakeCurl is Docker's API as guard.sh asks it: not Podman, and no org network.
const fakeCurl = `#!/usr/bin/env bash
case "$*" in *_ping*) exit 7 ;; *) echo '[]' ;; esac
`

// TestGuardRemoveStays: the guard's remove takes its rules away for good, though the guard's own
// loop keeps applying them until its container is removed (#224's flake: "rules remain after host
// rm", the chains and their jumps back after a remove). An apply that comes after the remove puts
// nothing back, and one already under way when the remove starts finishes before it.
func TestGuardRemoveStays(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("guard.sh runs in berth's Linux image")
	}
	for _, tool := range []string{"bash", "flock", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"iptables-nft": fakeIptables, "iptables-nft-restore": fakeIptables, "iptables-legacy": fakeIptables, "curl": fakeCurl} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rules := func(state string) string {
		b, _ := os.ReadFile(filepath.Join(state, "rules"))
		return strings.TrimSpace(string(b))
	}
	guard := func(state string, extra []string, cmd string) *exec.Cmd {
		c := exec.Command("bash", "-c", GuardScript, "guard", cmd)
		c.Env = append(os.Environ(), append([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"FAKE_RULES=" + filepath.Join(state, "rules"), "BERTH_GUARD_STATE=" + state}, extra...)...)
		return c
	}
	run := func(state, cmd string, extra ...string) {
		t.Helper()
		if out, err := guard(state, extra, cmd).CombinedOutput(); err != nil {
			t.Fatalf("guard %s: %v\n%s", cmd, err, out)
		}
	}

	state := t.TempDir()
	run(state, "apply")
	if r := rules(state); !strings.Contains(r, "-A INPUT -j BERTH-INPUT") || !strings.Contains(r, "-A DOCKER-USER -j BERTH-FORWARD") {
		t.Fatalf("apply put no rules in:\n%s", r)
	}
	run(state, "remove")
	if r := rules(state); r != "" {
		t.Fatalf("after remove:\n%s", r)
	}
	// The loop's next round, before the container is gone.
	run(state, "apply")
	if r := rules(state); r != "" {
		t.Errorf("an apply after the remove put rules back:\n%s", r)
	}

	// An apply under way (slow) when the remove starts: the remove waits for it, so nothing is left.
	state = t.TempDir()
	slow := guard(state, []string{"FAKE_SLOW=1"}, "apply")
	if err := slow.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the apply holds the lock, in its restore
	run(state, "remove")
	if err := slow.Wait(); err != nil {
		t.Fatal(err)
	}
	if r := rules(state); r != "" {
		t.Errorf("an apply under way during the remove left rules:\n%s", r)
	}
}
