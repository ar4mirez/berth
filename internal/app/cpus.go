package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/host"
)

// cappedCPUs is the CPUS an org starts with when it asks for more than its engine has, and whether
// that is so. Docker refuses a limit above its own count ("range of CPUs is from 0.01 to 2.00"),
// and an org asks for 4 unless told otherwise, which is more than a small VM has (Colima's default
// is 2). The limit is a ceiling, so one at the engine's count allows the org all there is.
//
// Only Docker is asked: Podman and Apple's container take the limit as written.
func (a *App) cappedCPUs(ctx context.Context, o string) (string, bool) {
	if a.Host.EngineName() != host.EngineDocker {
		return "", false
	}
	asked := a.env(o, "CPUS")
	if asked == "" {
		asked = "4"
	}
	want, err := strconv.ParseFloat(asked, 64)
	if err != nil {
		return "", false // compose says what is wrong with it
	}
	out, err := a.capture(ctx, true, "docker", "info", "--format", "{{.NCPU}}")
	has, perr := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || perr != nil || has < 1 || want <= float64(has) {
		return "", false
	}
	sayf(a.Stderr, "%s: %s asks for %s CPUs (CPUS in its org.env) and this Docker has %d, so it starts with %d. For more, give Docker more (Colima: colima stop, then colima start --cpu %s).\n",
		Tool, o, asked, has, has, asked)
	return strconv.Itoa(has), true
}
