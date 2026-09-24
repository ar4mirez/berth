// Command berth manages isolated Claude Code environments, one container per organization.
package main

import (
	"os"

	"github.com/ar4mirez/berth/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
