# Sourced (as root, first thing, by entrypoint.sh): make the container user `node` the host user
# who runs berth, so files in the bind mounts stay theirs (#38). The UID/GID come from compose
# (HOST_UID/HOST_GID); the image no longer bakes one in, so one image fits every host user.
# /etc/passwd and /etc/group are edited directly: usermod would walk the bind-mounted home dirs
# (.claude, .mise, .config), which can be large and are the host's already. Only files that came
# with the image are chowned, and nothing happens when the IDs already match.
_want_uid=${HOST_UID:-}; _want_gid=${HOST_GID:-}
_cur_uid=$(id -u node); _cur_gid=$(id -g node)
case "$_want_uid$_want_gid" in *[!0-9]*) echo "uid-remap: HOST_UID/HOST_GID must be numbers" >&2; exit 1 ;; esac
if [ -n "$_want_gid" ] && [ "$_want_gid" != "$_cur_gid" ]; then
  sed -i -E "s/^node:([^:]*):[0-9]+:/node:\1:${_want_gid}:/" /etc/group
  sed -i -E "s/^node:([^:]*):([0-9]+):[0-9]+:/node:\1:\2:${_want_gid}:/" /etc/passwd
fi
if [ -n "$_want_uid" ] && [ "$_want_uid" != "$_cur_uid" ]; then
  sed -i -E "s/^node:([^:]*):[0-9]+:/node:\1:${_want_uid}:/" /etc/passwd
fi
if [ "$(id -u node):$(id -g node)" != "$_cur_uid:$_cur_gid" ]; then
  # The image's own files under /home/node; the bind mounts (.claude, .mise, .config) are skipped.
  find /home/node -xdev \( -path /home/node/.claude -o -path /home/node/.mise -o -path /home/node/.config \) -prune \
    -o \( -uid "$_cur_uid" -o -gid "$_cur_gid" \) -exec chown -h node:node {} +
fi
unset _want_uid _want_gid _cur_uid _cur_gid
