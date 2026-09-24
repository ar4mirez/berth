// Package version holds build metadata, stamped at link time by goreleaser or the Makefile.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String is the one-line form printed by `berth --version`.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
