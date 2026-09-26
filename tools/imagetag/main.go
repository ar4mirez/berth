// Command imagetag prints the content hash berth tags its image with (berth/claude-env:<hash>),
// for the release workflow, which publishes the same image/ under that hash (#41).
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ar4mirez/berth/internal/assets"
)

func main() {
	set, err := assets.Embedded("/")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, hash, _ := strings.Cut(set.Tag, ":")
	fmt.Println(hash)
}
