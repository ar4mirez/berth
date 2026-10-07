// Package berth holds the files berth ships with: the container image's build context (image/) and
// the per-org compose.yml. internal/assets writes them out to the state root.
package berth

import "embed"

// Assets is image/ (every file, dotfiles included) and compose.yml, as in this checkout.
//
//go:embed compose.yml all:image
var Assets embed.FS

// Docs is berth's documentation and PARITY.md, as in this checkout: `berth mcp` serves them to
// agents as reference (#61). The generated command reference and the PRDs are left out.
//
//go:embed PARITY.md docs/*.md docs/guides/*.md
var Docs embed.FS
