# Sourced (as root, by entrypoint.sh): export the org's secrets and custom variables from files, one
# per variable, in /config/secrets/env (<org>/config/secrets/env on the host, mounted read-only).
# Values kept there never reach the container's config, so `docker inspect` can't show them (#37).
# A value still in org.env (an org not migrated yet) arrives as an env var and keeps working; a
# file wins over it. `berth secrets migrate <org>` moves the values.
if [ -d /config/secrets/env ]; then
  for _f in /config/secrets/env/*; do
    [ -f "$_f" ] || continue
    _k=${_f##*/}
    case "$_k" in ""|[0-9]*|*[!A-Z0-9_]*) continue ;; esac   # a valid variable name only
    export "$_k=$(cat "$_f")"
  done
  unset _f _k
fi
