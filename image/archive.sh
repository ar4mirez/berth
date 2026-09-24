#!/usr/bin/env bash
# Backup/restore engine. Runs as root in a throwaway, network-less container so it can read
# everything (e.g. root-owned sshd host keys), and so the host needs nothing but Docker.
#   archive.sh plan               show what a backup keeps vs. skips (sizes)
#   archive.sh create             write the backup to stdout; ENC=gpg|age|none
#   archive.sh extract UID GID    read a backup (any format) from stdin into /dst
# Secrets come from /secrets (mounted 0700 tmpfs-like dir): pass | recipients | identity.
#
# Formats: zstd tar (plain) | gpg symmetric AES-256 (MDC-authenticated) | age (X25519/ssh, AEAD).
# Both encrypted formats fail loudly on a wrong key or any tampering.
set -euo pipefail

SRC=/src DST=/dst

# Regenerable data: never worth backing up, rebuilt by `ccenv rehydrate`.
SKIP_FIXED="mise/data mise/cache mise/state mise/cargo mise/rustup mise/go mise/xdg-cache
claude/cache claude/debug claude/statsig claude/remote-control.log"
SKIP_DIR_NAMES="node_modules .venv __pycache__ .pytest_cache .mypy_cache .ruff_cache .tox .nox
.next .nuxt .svelte-kit .turbo .parcel-cache .angular .gradle .terraform .dart_tool .pnpm-store .cache"

excludes() {  # prints paths (relative to $SRC) to skip
  cd "$SRC"
  for p in $SKIP_FIXED; do [ -e "$p" ] && echo "$p"; done
  [ -d workspace ] || return 0
  local expr=() n
  for n in $SKIP_DIR_NAMES; do expr+=(-name "$n" -o); done
  unset 'expr[${#expr[@]}-1]'
  find workspace -type d \( "${expr[@]}" \) -prune -print
  # Rust build output (only when it's really a cargo target dir), Ruby vendored gems, any Python venv.
  find workspace -type d -name node_modules -prune -o -type d -name target -print -prune | while read -r d; do
    [ -f "$(dirname "$d")/Cargo.toml" ] && echo "$d"; done
  find workspace -type d -name node_modules -prune -o -type d -path '*/vendor/bundle' -print -prune | while read -r d; do
    [ -f "$(dirname "$(dirname "$d")")/Gemfile" ] && echo "$d"; done
  find workspace -type d \( -name node_modules -o -name .venv \) -prune -o -name pyvenv.cfg -printf '%h\n'
}

human() { numfmt --to=iec --suffix=B "$1" 2>/dev/null || echo "${1}B"; }
gpg_() { GNUPGHOME=$(mktemp -d) gpg --batch --quiet --no-tty --pinentry-mode loopback --passphrase-file /secrets/pass "$@"; }
need_secret() { [ -s "/secrets/$1" ] || { echo "archive: $2" >&2; exit 3; }; }

encrypt_stream() {
  case "${ENC:-none}" in
    none) cat ;;
    gpg)  need_secret pass "no passphrase provided"
          gpg_ --symmetric --cipher-algo AES256 --s2k-mode 3 --s2k-digest-algo SHA512 \
               --s2k-count 65011712 --compress-algo none --force-mdc -o - ;;
    age)  need_secret recipients "no age recipients provided"
          age -e -R /secrets/recipients ;;
    *) echo "archive: unknown ENC=$ENC" >&2; exit 2 ;;
  esac
}

detect() {  # detect <header-file> -> zstd|age|gpg|unknown
  local hex; hex=$(od -An -tx1 -N4 "$1" | tr -d ' \n')
  if [ "$hex" = "28b52ffd" ]; then echo zstd
  elif head -c 21 "$1" | grep -q '^age-encryption.org/v1'; then echo age
  elif [[ "$hex" == 8c* || "$hex" == c3* ]]; then echo gpg
  else echo unknown; fi
}

case "${1:-}" in
  plan)
    cd "$SRC"
    ex=$(excludes | sort -u)
    total=$(du -sb . | cut -f1)
    skipped=0
    [ -n "$ex" ] && skipped=$(echo "$ex" | tr '\n' '\0' | du -sbc --files0-from=- | tail -1 | cut -f1)
    echo "Org folder:    $(human "$total")"
    echo "Backed up:     $(human $(( total - skipped )))  (before compression)"
    echo "Skipped:       $(human "$skipped")  (regenerable, rebuilt on restore)"
    if [ -n "$ex" ]; then
      echo
      echo "Largest skipped:"
      echo "$ex" | tr '\n' '\0' | du -sb --files0-from=- | sort -rn | head -12 | \
        while read -r s p; do printf '  %8s  %s\n' "$(human "$s")" "$p"; done
    fi
    ;;
  create)
    cd "$SRC"
    exf=$(mktemp); excludes | sort -u | sed 's|^|./|' > "$exf"
    tools=$(jq -Rs . 2>/dev/null < mise/config/config.toml || echo '""')
    mkdir -p /tmp/m
    jq -n --arg org "$ORG" --arg host "$SRC_HOST" --arg date "$(date -Is)" --arg enc "${ENC:-none}" \
          --argjson tools "$tools" --rawfile skipped "$exf" \
      '{format: 2, org: $org, source_host: $host, created: $date, encryption: $enc,
        mise_global_config: $tools, skipped: ($skipped | split("\n") | map(select(. != "")))}' \
      > /tmp/m/.ccenv-manifest.json
    tar --anchored --no-wildcards --exclude-from="$exf" --warning=no-file-changed --warning=no-file-removed \
        -cf - -C /tmp/m .ccenv-manifest.json -C "$SRC" . | zstd -q -T0 -9 | encrypt_stream
    ;;
  extract)
    uid="$2" gid="$3"
    mkdir -p "$DST"
    dd bs=1 count=64 status=none of=/tmp/head
    fmt=$(detect /tmp/head)
    case "$fmt" in
      zstd) dec() { cat; } ;;
      gpg)  need_secret pass "backup is passphrase-encrypted: set CCENV_BACKUP_PASSPHRASE or run interactively"
            dec() { gpg_ --decrypt -o -; } ;;
      age)  need_secret identity "backup is key-encrypted: pass --identity <age-or-ssh-key> (default ~/.config/ccenv/backup.key)"
            dec() { age -d -i /secrets/identity; } ;;
      *) echo "archive: not a ccenv backup (unrecognized format)" >&2; exit 4 ;;
    esac
    echo "$fmt" > "$DST/.ccenv-format"
    cat /tmp/head - | dec | zstd -dc | tar -xf - -C "$DST" --numeric-owner
    # Files belong to whoever runs ccenv on this host; sshd host keys stay root-only.
    find "$DST" -mindepth 1 -maxdepth 1 ! -name sshd -exec chown -R "$uid:$gid" {} +
    if [ -d "$DST/sshd" ]; then chown -R 0:0 "$DST/sshd"; chmod 600 "$DST"/sshd/*_key 2>/dev/null || true; fi
    ;;
  keygen)
    age-keygen 2>/dev/null
    ;;
  *) echo "usage: archive.sh plan|create|extract UID GID|keygen" >&2; exit 2 ;;
esac
