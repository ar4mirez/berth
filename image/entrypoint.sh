#!/usr/bin/env bash
# Runs as root: firewall, identity, sshd/ttyd/tmux/remote-control; user-facing work runs as `node`.
set -euo pipefail

ORG="${ORG:?ORG must be set}"
as_node() { runuser -u node -- "$@"; }

# --- node becomes the host user (HOST_UID/HOST_GID), before anything runs as node (#38) ---
. /usr/local/lib/claude-env/uid-remap.sh

mkdir -p /etc/claude-env
echo "$ORG" > /etc/claude-env/org

# --- Secrets kept as files (#37): exported here, so every session below inherits them ---
. /usr/local/lib/claude-env/secrets-env.sh

# --- Firewall (re-applied every 5 min so CDN IP changes and edits are picked up) ---
/usr/local/bin/init-firewall.sh apply
( while sleep 300; do /usr/local/bin/init-firewall.sh apply >/dev/null || true; done ) &

# --- Env snapshot for SSH sessions (they don't inherit container env) --------
: > /etc/claude-env/env
for v in ORG CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY GH_TOKEN CLAUDE_CONFIG_DIR \
         DISABLE_AUTOUPDATER LANG ANTHROPIC_MODEL REPO_POLICY ${CCENV_ENV_KEYS:-}; do   # + custom: berth env <org> set
  [ -n "${!v:-}" ] && printf '%s=%q\n' "$v" "${!v}" >> /etc/claude-env/env
done
chown root:node /etc/claude-env/env && chmod 640 /etc/claude-env/env

# --- Git identity (per org) --------------------------------------------------
# The git key is mounted at /opt/claude-secrets/ssh (root-only). Git's transport is the system-wide
# git-ssh-guard (see Dockerfile), which only connects for repos in /config/repos.txt.
install -d -m 700 -o node -g node /home/node/.ssh
as_node git config --global init.defaultBranch main
as_node git config --global --replace-all safe.directory '*'
[ -n "${GIT_USER_NAME:-}" ]  && as_node git config --global user.name  "$GIT_USER_NAME"
[ -n "${GIT_USER_EMAIL:-}" ] && as_node git config --global user.email "$GIT_USER_EMAIL"

# --- Workspace sweep: only registered repos may live in /workspace ------------------
# REPO_POLICY=enforce moves anything else to /quarantine (orgs/<org>/quarantine on the host);
# warn only logs it; off disables. Hidden .claude (Claude project settings) is always allowed.
. /usr/local/lib/claude-env/repo-policy.sh
repo_sweep() {
  local e name
  for e in /workspace/* /workspace/.[!.]* /workspace/..?*; do
    [ -e "$e" ] || [ -L "$e" ] || continue
    name=$(basename "$e")
    [ "$name" = .claude ] && continue
    repo_allowed_dir "$name" && continue
    if [ "${REPO_POLICY:-enforce}" = enforce ] && [ -d /quarantine ]; then
      mv "$e" "/quarantine/$name.$(date +%Y%m%d-%H%M%S)" \
        && echo "$(date -Is) quarantined /workspace/$name (not a registered repo)" >> /run/repo-policy.log
    else
      echo "$(date -Is) unregistered: /workspace/$name" >> /run/repo-policy.log
    fi
  done
}
if [ "${REPO_POLICY:-enforce}" != off ]; then
  ( while true; do repo_sweep || true; sleep 30; done ) &
fi

# --- Claude Code defaults (model + permissions are enforced by managed settings) ---
CFG=/home/node/.claude
[ -f "$CFG/settings.json" ] || echo '{ "includeCoAuthoredBy": true }' > "$CFG/settings.json"
[ -f "$CFG/.claude.json" ] || echo '{}' > "$CFG/.claude.json"
tmp=$(mktemp)
jq '.hasCompletedOnboarding = true
    | .bypassPermissionsModeAccepted = true
    | .remoteDialogSeen = true
    | .projects["/workspace"].hasTrustDialogAccepted = true' \
  "$CFG/.claude.json" > "$tmp" && cat "$tmp" > "$CFG/.claude.json" && rm -f "$tmp"
chown -R node:node "$CFG"

# --- sshd ---------------------------------------------------------------------
mkdir -p /etc/ssh/keys
[ -f /etc/ssh/keys/ssh_host_ed25519_key ] || ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/keys/ssh_host_ed25519_key
[ -f /etc/ssh/keys/ssh_host_rsa_key ]     || ssh-keygen -q -t rsa -b 4096 -N '' -f /etc/ssh/keys/ssh_host_rsa_key
chmod 600 /etc/ssh/keys/*_key
if [ -f /config/authorized_keys ]; then
  install -o root -g root -m 644 /config/authorized_keys /etc/ssh/authorized_keys/node
else
  echo "sshd: WARNING no authorized_keys mounted; SSH login disabled" >&2
fi

# --- tmux watchdog: the shared session every terminal attaches to ------------
( while true; do
    as_node tmux has-session -t main 2>/dev/null \
      || as_node tmux new-session -d -s main -c /workspace -n claude
    sleep 10
  done ) &

# --- ttyd (browser terminal); password comes from a secrets file, never env/logs ---
( while true; do
    if [ -s /config/secrets/ttyd_credential ]; then
      as_node ttyd -W -d 3 -p 7681 -c "$(cat /config/secrets/ttyd_credential)" -t titleFixed="claude:$ORG" \
        tmux new-session -A -s main -c /workspace >/dev/null 2>&1 || true
    fi
    sleep 3
  done ) &

# --- Remote Control service: new sessions are started from claude.ai/code -------
# Needs a full-scope login (`berth login`); the inference-only token can't do it.
RC_LOG="$CFG/remote-control.log"
if [ "${REMOTE_CONTROL:-1}" = "1" ]; then
  ( while true; do
      if [ -f "$CFG/.credentials.json" ]; then
        [ -f "$RC_LOG" ] && [ "$(stat -c %s "$RC_LOG")" -gt 5000000 ] && : > "$RC_LOG"
        echo "=== $(date -Is) starting remote-control" >> "$RC_LOG"
        ( cd /workspace && as_node env -u CLAUDE_CODE_OAUTH_TOKEN claude remote-control \
            --name "$ORG" --remote-control-session-name-prefix "$ORG" \
            --spawn same-dir --capacity "${REMOTE_CAPACITY:-8}" \
            --permission-mode bypassPermissions </dev/null >> "$RC_LOG" 2>&1 ) || true
        if tail -n 3 "$RC_LOG" | grep -q "disabled by your organization's policy"; then
          # The account's org turned Remote Control off: retry hourly so it recovers if an admin enables it.
          echo "=== $(date -Is) blocked by organization policy, retrying in 1h" >> "$RC_LOG"
          sleep 3600
        else
          echo "=== $(date -Is) remote-control exited, restarting in 5s" >> "$RC_LOG"
          sleep 5
        fi
      else
        sleep 15
      fi
    done ) &
fi

echo "claude-env[$ORG]: ready"
exec /usr/sbin/sshd -D -e
