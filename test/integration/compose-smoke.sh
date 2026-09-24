#!/usr/bin/env bash
# Smoke test against a real Docker daemon (CI only; the dev container has no Docker on purpose).
# Brings up the real compose.yml for a synthetic t-* org, with the same environment ccenv's
# compose() sets, checks the container and compose labels, then tears it down and checks nothing
# is left behind. The image is swapped for busybox: this tests the compose contract, not image/.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ORG=t-smoke
HOME_DIR="$(mktemp -d)"
ORG_DIR="$HOME_DIR/orgs/$ORG"
OVERRIDE="$HOME_DIR/override.yml"
fail() { echo "FAIL: $*" >&2; exit 1; }

dc() {  # mirrors ccenv compose(): same env vars, same -f / --env-file
  BIND_ADDR=127.0.0.1 ORG="$ORG" ORG_DIR="$ORG_DIR" HOST_UID="$(id -u)" HOST_GID="$(id -g)" \
    docker compose -f "$ROOT/compose.yml" -f "$OVERRIDE" --env-file "$ORG_DIR/org.env" "$@"
}
cleanup() { dc down --remove-orphans >/dev/null 2>&1 || true; rm -rf "$HOME_DIR"; }
trap cleanup EXIT

# Synthetic org scaffold, same layout as `ccenv init` (no keys or tokens needed for this test).
mkdir -p "$ORG_DIR"/{workspace,claude,ssh,sshd,mise,home-config,quarantine,config/secrets}
chmod 700 "$ORG_DIR" "$ORG_DIR/ssh" "$ORG_DIR/claude" "$ORG_DIR/home-config" "$ORG_DIR/config/secrets"
cat > "$ORG_DIR/org.env" <<ENV
CLAUDE_CODE_OAUTH_TOKEN=
GIT_USER_NAME=Test User
GIT_USER_EMAIL=test@example.com
BIND_ADDR=127.0.0.1
SSH_PORT=2290
TTYD_PORT=7790
REMOTE_CONTROL=0
REPO_POLICY=enforce
MEM_LIMIT=256m
CPUS=1
ENV
chmod 600 "$ORG_DIR/org.env"
cat > "$OVERRIDE" <<'YML'
services:
  claude:
    image: busybox:1.37
    build: !reset null
    entrypoint: ["sleep", "infinity"]
YML

echo "== compose config"
dc config --quiet
cfg=$(dc config --format json)
[ "$(jq -r .name <<<"$cfg")" = "claude-$ORG" ] || fail "project name"
[ "$(jq -r .services.claude.container_name <<<"$cfg")" = "claude-$ORG" ] || fail "container_name"

echo "== up"
dc up -d --force-recreate
c="claude-$ORG"
[ "$(docker inspect -f '{{.State.Running}}' "$c")" = true ] || fail "$c not running"
[ "$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$c")" = "claude-$ORG" ] || fail "compose project label"
[ "$(docker inspect -f '{{.Config.Hostname}}' "$c")" = "$ORG" ] || fail "hostname"
[ "$(docker inspect -f '{{.HostConfig.Memory}}' "$c")" = 268435456 ] || fail "mem_limit"
[ "$(docker inspect -f '{{.HostConfig.NanoCpus}}' "$c")" = 1000000000 ] || fail "cpus"
[ "$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$c")" = unless-stopped ] || fail "restart policy"
ports=$(docker port "$c" | sort)
grep -qx '2222/tcp -> 127.0.0.1:2290' <<<"$ports" || fail "ssh port binding: $ports"
grep -qx '7681/tcp -> 127.0.0.1:7790' <<<"$ports" || fail "ttyd port binding: $ports"
docker exec "$c" sh -c 'test "$ORG" = t-smoke && test "$REPO_POLICY" = enforce' || fail "container env"
docker exec "$c" sh -c 'touch /config/x 2>/dev/null' && fail "/config must be read-only"

echo "== restart (force-recreate keeps a single container)"
dc up -d --force-recreate
[ "$(docker ps -aq --filter "label=com.docker.compose.project=claude-$ORG" | wc -l)" = 1 ] || fail "duplicate containers"

echo "== down"
dc down
[ -z "$(docker ps -aq --filter "label=com.docker.compose.project=claude-$ORG")" ] || fail "orphan container"
[ -z "$(docker network ls -q --filter "label=com.docker.compose.project=claude-$ORG")" ] || fail "orphan network"
echo "OK"
