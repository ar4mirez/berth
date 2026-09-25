// Package berth holds the files berth ships with: the container image's build context (image/) and
// the per-org compose.yml. internal/assets writes them out to the state root.
package berth

import "embed"

// Assets is image/ (every file, dotfiles included) and compose.yml, as in this checkout.
//
//go:embed compose.yml all:image
var Assets embed.FS
