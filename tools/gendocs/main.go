// gendocs writes berth's command reference and man pages from the command tree (#56).
//
//	go run ./tools/gendocs                 # docs/reference/commands (committed; CI checks it's current)
//	go run ./tools/gendocs man <dir> [ver] # man pages (the release packs them)
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ar4mirez/berth/internal/cli"
	"github.com/ar4mirez/berth/internal/docgen"
	"github.com/ar4mirez/berth/internal/version"
)

func main() {
	var err error
	switch {
	case (len(os.Args) == 3 || len(os.Args) == 4) && os.Args[1] == "man":
		v := version.Version
		if len(os.Args) == 4 {
			v = os.Args[3] // the release's version (goreleaser's hook)
		}
		err = docgen.Man(cli.NewRoot(), os.Args[2], v)
	case len(os.Args) == 1:
		dir := filepath.Join("docs", docgen.RefDir)
		if err = os.RemoveAll(dir); err == nil {
			err = docgen.Markdown(cli.NewRoot(), dir)
		}
	default:
		err = fmt.Errorf("usage: gendocs | gendocs man <dir>")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}
