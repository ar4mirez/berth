package integration

import (
	"regexp"
	"strconv"
	"strings"
)

// knownTeardownError is the one teardown failure the rootless host-guard test tolerates (#215):
// Podman's rootless network cleanup, after every guard check has run.
const knownTeardownError = "rootless netns: kill network process: permission denied"

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
