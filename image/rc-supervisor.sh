#!/usr/bin/env bash
# Remote Control's supervisor: keeps `claude remote-control` running for the org, so new sessions
# can be started from claude.ai/code. The entrypoint starts it, as root, when REMOTE_CONTROL=1.
#
# It waits between attempts: 5 seconds after an exit, an hour when the account's Claude organization
# has turned Remote Control off, 15 seconds while there is no login yet. Every wait ends early when
# /run/rc-retry appears: `berth remote <org> restart` and `berth login` create it, so a retry is
# never further away than the next two seconds (#7).
#
# Not `set -e`: a command that fails must never end the supervisor. It used to run under the
# entrypoint's set -e, where killing its `sleep` ended the loop for good.
set -uo pipefail

ORG="${ORG:?ORG must be set}"
CFG="${CFG:-/home/node/.claude}"
RC_LOG="${RC_LOG:-$CFG/remote-control.log}"
RETRY=/run/rc-retry
BLOCKED_WAIT="${RC_BLOCKED_WAIT:-3600}"

log() { echo "=== $(date -Is) $*" >> "$RC_LOG"; }

# wait_or_retry <seconds>: wait that long, or until a retry is asked for.
wait_or_retry() {
  local left="$1"
  while [ "$left" -gt 0 ]; do
    if [ -e "$RETRY" ]; then
      rm -f "$RETRY"
      log "retry requested"
      return 0
    fi
    sleep 2
    left=$((left - 2))
  done
}

while true; do
  if [ -f "$CFG/.credentials.json" ]; then
    [ -f "$RC_LOG" ] && [ "$(stat -c %s "$RC_LOG")" -gt 5000000 ] && : > "$RC_LOG"
    rm -f "$RETRY"   # this attempt answers any request made so far
    log "starting remote-control"
    ( cd /workspace && runuser -u node -- env -u CLAUDE_CODE_OAUTH_TOKEN claude remote-control \
        --name "$ORG" --remote-control-session-name-prefix "$ORG" \
        --spawn same-dir --capacity "${REMOTE_CAPACITY:-8}" \
        --permission-mode bypassPermissions </dev/null >> "$RC_LOG" 2>&1 ) || true
    if tail -n 3 "$RC_LOG" | grep -q "disabled by your organization's policy"; then
      # The account's org turned Remote Control off: retry hourly so it recovers if an admin enables it.
      log "blocked by organization policy, retrying in 1h"
      wait_or_retry "$BLOCKED_WAIT"
    else
      log "remote-control exited, restarting in 5s"
      wait_or_retry 5
    fi
  else
    wait_or_retry 15
  fi
done
