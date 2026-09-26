// Package version holds build metadata, stamped at link time by goreleaser or the Makefile.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
	// Image is the released image this build pulls (the same value as assets.PublishedImage).
	Image = ""
)

// String is the one-line form printed by `berth --version`.
func String() string {
	if Image != "" {
		return fmt.Sprintf("%s (commit %s, built %s, image %s)", Version, Commit, Date, Image)
	}
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
