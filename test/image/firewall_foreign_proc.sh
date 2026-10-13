#!/usr/bin/env bash
# Inside a container of berth's image (NET_ADMIN, NET_RAW, SYS_ADMIN for unshare, a writable
# /config): the firewall still replaces dnsmasq where /proc isn't the container's own. That is a
# container nested in another (rootless Podman in a container): /proc lists the outer one's
# processes, so /proc/<pid> says nothing about the dnsmasq this script started. A new pid namespace
# under the old /proc is the same thing.
set -euo pipefail
fail() { echo "FAIL: $*" >&2; exit 1; }
if [ "${1:-}" != inside ]; then
  exec unshare --pid --fork bash "$0" inside
fi
read -r seen _ < /proc/self/stat
[ "$seen" != "$$" ] || fail "/proc is this namespace's own (pid $$): nothing is tested"

one_dnsmasq() {
  local pid; pid=$(cat /run/dnsmasq.pid) || fail "$1: no pid file"
  kill -0 "$pid" 2>/dev/null || fail "$1: the pid file's process ($pid) isn't running"
  [ "$(ss -Hlun | grep -c '127\.0\.0\.1:53 ')" = 1 ] || fail "$1: $(ss -Hlun | grep -c '127\.0\.0\.1:53 ') listeners on 127.0.0.1:53"
  grep -qx "nameserver 127.0.0.1" /etc/resolv.conf || fail "$1: resolv.conf doesn't use dnsmasq"
  grep -qE "^on [0-9]+$" /run/firewall.status || fail "$1: status is '$(cat /run/firewall.status)'"
}
printf 'mode on\nexample.com\n' > /config/firewall.txt
init-firewall.sh apply >/dev/null 2>/tmp/err || fail "the first apply: exit $?: $(cat /tmp/err)"
one_dnsmasq "the first apply"

# A change restarts dnsmasq: the one running is stopped first, so the new one gets port 53.
for i in 1 2 3 4 5; do
  before=$(cat /run/dnsmasq.pid)
  printf 'mode on\nexample.com\nf%s.example.org\n' "$i" > /config/firewall.txt
  init-firewall.sh apply >/dev/null 2>/tmp/err || fail "change $i: exit $?: $(cat /tmp/err)"
  ! grep -q "dnsmasq" /tmp/err || fail "change $i: $(cat /tmp/err)"
  one_dnsmasq "change $i"
  [ "$(cat /run/dnsmasq.pid)" != "$before" ] || fail "change $i: dnsmasq wasn't replaced"
  ! kill -0 "$before" 2>/dev/null || fail "change $i: the old dnsmasq ($before) still runs"
  grep -qx "ipset=/f$i.example.org/allowed-dns" /run/berth-dnsmasq.conf || fail "change $i isn't in dnsmasq's config"
done

# No change: dnsmasq and its cache are kept.
before=$(cat /run/dnsmasq.pid)
init-firewall.sh apply >/dev/null || fail "an apply with no change: exit $?"
[ "$(cat /run/dnsmasq.pid)" = "$before" ] || fail "an apply with no change restarted dnsmasq"

# A pid file left behind, whose pid is now another process, with no dnsmasq listening: that process
# is left alone.
kill "$before"; while kill -0 "$before" 2>/dev/null; do sleep 0.1; done
sleep 600 & other=$!
echo "$other" > /run/dnsmasq.pid
printf 'mode on\nexample.com\nstale.example.org\n' > /config/firewall.txt
init-firewall.sh apply >/dev/null || fail "apply with a stale pid file: exit $?"
kill -0 "$other" 2>/dev/null || fail "the stale pid's process was killed"
one_dnsmasq "after a stale pid file"
kill "$other"
echo "ok: with a /proc that isn't its own, each change still leaves one dnsmasq running"
