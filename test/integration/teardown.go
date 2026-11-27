package integration

import (
	"regexp"
	"strconv"
	"strings"
)

// knownTeardownError is the one teardown failure the rootless host-guard test tolerates (#215):
// Podman's rootless network cleanup, after every guard check has run. It is tolerated only when the
// kernel says why (pastaSignalDenials).
const knownTeardownError = "rootless netns: kill network process: permission denied"

// The cause (#215) is upstream, in the host's AppArmor policy: Ubuntu's pasta profile has no signal
// rule, and Podman runs under a profile of its own name (podman, unconfined), so the kernel refuses
// the SIGTERM Podman sends pasta when it removes the rootless network namespace. The fixture runs
// on the runner's kernel, under its policy. Reported as
// https://bugs.launchpad.net/ubuntu/+source/passt/+bug/2154379; the rule it needs,
// `signal (receive) peer=podman,`, is proposed in passt as
// https://archives.passt.top/passt-dev/be4eef52592341b08433f80995025768@kdo.de/.
var pastaDenialFields = []string{
	`apparmor="DENIED"`, `operation="signal"`, `profile="pasta"`, `requested_mask="receive"`, `signal=term`, `peer="podman"`,
}

// pastaSignalDenials are the lines of a kernel log that record that refusal.
func pastaSignalDenials(kernelLog string) []string {
	var out []string
	for _, l := range strings.Split(kernelLog, "\n") {
		all := true
		for _, f := range pastaDenialFields {
			if !strings.Contains(l, f) {
				all = false
				break
			}
		}
		if all {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// upstreamTeardown reports whether a failed `berth down`'s output is the known error, and the kernel
// recorded AppArmor's refusal of pasta's SIGTERM since before (the kernel log as read before the
// down) and after (as read after it): the denial it names, or "" when it isn't that case.
func upstreamTeardown(out, before, after string) string {
	if !knownRootlessTeardown(out) {
		return ""
	}
	b, a := pastaSignalDenials(before), pastaSignalDenials(after)
	if len(a) <= len(b) {
		return ""
	}
	return a[len(a)-1]
}

var (
	teardownBullet = regexp.MustCompile(`(?m)^\s*\* (.*\S)\s*$`)
	teardownDaemon = regexp.MustCompile(`(?m)^.*Error response from daemon: (.*)$`)
	removeNetwork  = regexp.MustCompile(`^removing container [0-9a-f]+ network: (\d+) errors? occurred:\s*$`)
)

// knownRootlessTeardown reports whether a failed `berth down`'s output holds that error and no
// other: every daemon error is a container's network removal, and every error it lists is
// knownTeardownError.
func knownRootlessTeardown(out string) bool {
	daemon := teardownDaemon.FindAllStringSubmatch(out, -1)
	bullets := teardownBullet.FindAllStringSubmatch(out, -1)
	if len(daemon) == 0 || len(bullets) == 0 {
		return false
	}
	want := 0
	for _, d := range daemon {
		m := removeNetwork.FindStringSubmatch(d[1])
		if m == nil {
			return false
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return false
		}
		want += n
	}
	if len(bullets) != want {
		return false
	}
	for _, b := range bullets {
		if b[1] != knownTeardownError {
			return false
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Error:") && !strings.Contains(l, "docker-compose") {
			return false
		}
	}
	return true
}
