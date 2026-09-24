#!/usr/bin/env bash
# drift.sh [legacy-checkout]: diff berth's image/ + compose.yml against the legacy claude-envs checkout (run on the host).
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
legacy="${1:-${CCENV_LEGACY:-$HOME/Work/claude-envs}}"
[ -f "$legacy/ccenv" ] || { echo "drift: no legacy checkout at $legacy" >&2; exit 2; }
diff -ru "$legacy/image" "$here/image" && diff -u "$legacy/compose.yml" "$here/compose.yml" && echo "drift: none"
