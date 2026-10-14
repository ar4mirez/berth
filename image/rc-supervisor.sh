#!/usr/bin/env bash
# Remote Control's supervisor: keeps `claude remote-control` running for the org, so new sessions
# can be started from claude.ai/code. The entrypoint starts it, as root, when REMOTE_CONTROL=1.
#
# It waits between attempts: 5 seconds after an exit, an hour when the account's Claude organization
# has turned Remote Control off, 15 seconds while there is no login yet. Every wait ends early when
# /run/rc-retry appears: `berth account remote restart <org>` and `berth account login` create it, so a retry is
# never further away than the next two seconds (#7).
#
# It starts one only when no other is left (#187): one that is still exiting, or that outlived a
# runuser killed before it, still serves /workspace, and a second fails with "This folder is already
# served" until the first is gone, for minutes. One that hasn't exited after RC_STOP_WAIT seconds is
# killed.
#
# Not `set -e`: a command that fails must never end the supervisor. It used to run under the
# entrypoint's set -e, where killing its `sleep` ended the loop for good.
set -uo pipefail

ORG="${ORG:?ORG must be set}"
CFG="${CFG:-/home/node/.claude}"
RC_LOG="${RC_LOG:-$CFG/remote-control.log}"
RETRY="${RC_RETRY:-/run/rc-retry}"
BLOCKED_WAIT="${RC_BLOCKED_WAIT:-3600}"
STOP_WAIT="${RC_STOP_WAIT:-60}"
# What runs, and where: the tests replace them (test/image/supervisor_test.go).
CLAUDE="${RC_CLAUDE:-claude}"
DIR="${RC_DIR:-/workspace}"
PROC="^$CLAUDE remote-control"

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

# wait_stopped: wait until no `claude remote-control` is left, then return; kill one still there
# after STOP_WAIT seconds.
wait_stopped() {
  local left="$STOP_WAIT"
  pgrep -f "$PROC" >/dev/null || return 0
  log "waiting for the previous remote-control to exit"
  while [ "$left" -gt 0 ]; do
    sleep 1
    left=$((left - 1))
    pgrep -f "$PROC" >/dev/null || return 0
  done
  log "the previous remote-control didn't exit in ${STOP_WAIT}s, killing it"
  pkill -KILL -f "$PROC"
  sleep 1
}

while true; do
  if [ -f "$CFG/.credentials.json" ]; then
    [ -f "$RC_LOG" ] && [ "$(stat -c %s "$RC_LOG")" -gt 5000000 ] && : > "$RC_LOG"
    wait_stopped
    rm -f "$RETRY"   # this attempt answers any request made so far
    log "starting remote-control"
    ( cd "$DIR" && runuser -u node -- env -u CLAUDE_CODE_OAUTH_TOKEN "$CLAUDE" remote-control \
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
