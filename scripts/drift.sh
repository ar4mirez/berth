#!/usr/bin/env bash
# drift.sh [legacy-checkout]: diff berth's image/ + compose.yml against the legacy claude-envs checkout (run on the host).
# Both diffs always run. Exit 0: no drift; 1: drift (shown above); 2: a diff couldn't run (a missing file, say).
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
legacy="${1:-${CCENV_LEGACY:-$HOME/Work/claude-envs}}"
[ -f "$legacy/ccenv" ] || { echo "drift: no legacy checkout at $legacy" >&2; exit 2; }
rc=0
diff -ru "$legacy/image" "$here/image" || rc=$?
diff -u "$legacy/compose.yml" "$here/compose.yml" || { r=$?; [ "$r" -gt "$rc" ] && rc=$r; }
[ "$rc" = 0 ] && echo "drift: none"
exit "$rc"
