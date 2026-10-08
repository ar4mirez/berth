#!/usr/bin/env bash
# Inside a container of berth's image (NET_ADMIN, NET_RAW, a writable /config): the firewall applies
# one at a time, and a change always leaves dnsmasq running (#149).
set -euo pipefail
fail() { echo "FAIL: $*" >&2; exit 1; }
one_dnsmasq() {
  [ "$(pgrep -c -x dnsmasq)" = 1 ] || fail "$1: $(pgrep -c -x dnsmasq) dnsmasq processes"
  [ "$(cat /proc/"$(cat /run/dnsmasq.pid)"/comm)" = dnsmasq ] || fail "$1: the pid file isn't dnsmasq's"
  grep -qx "nameserver 127.0.0.1" /etc/resolv.conf || fail "$1: resolv.conf doesn't use dnsmasq"
  grep -q "^on " /run/firewall.status || fail "$1: status is '$(cat /run/firewall.status)'"
}
printf 'mode on\nexample.com\n' > /config/firewall.txt
init-firewall.sh apply >/dev/null
one_dnsmasq "the first apply"

# A change, twenty times, back to back: each restarts dnsmasq.
for i in $(seq 1 20); do
  printf 'mode on\nexample.com\nr%s.example.org\n' "$i" > /config/firewall.txt
  init-firewall.sh apply >/tmp/out 2>/tmp/err || fail "change $i: exit $?: $(cat /tmp/err)"
  ! grep -q "dnsmasq" /tmp/err || fail "change $i: $(cat /tmp/err)"
  one_dnsmasq "change $i"
  grep -qx "ipset=/r$i.example.org/allowed-dns" /run/berth-dnsmasq.conf || fail "change $i isn't in dnsmasq's config"
done

# Eight applies at once, while the list changes under them: none fails, and one dnsmasq is left,
# on the last list.
for round in 1 2 3; do
  pids=()
  for i in $(seq 1 8); do
    printf 'mode on\nexample.com\nc%s-%s.example.org\n' "$round" "$i" > /config/firewall.txt
    init-firewall.sh apply >/dev/null 2>"/tmp/err.$i" & pids+=($!)
  done
  for i in "${!pids[@]}"; do
    wait "${pids[$i]}" || fail "round $round, apply $((i + 1)): exit $?: $(cat "/tmp/err.$((i + 1))")"
  done
  init-firewall.sh apply >/dev/null
  one_dnsmasq "round $round of concurrent applies"
  grep -qx "ipset=/c$round-8.example.org/allowed-dns" /run/berth-dnsmasq.conf || fail "round $round: dnsmasq isn't on the last list"
done

# The lock isn't kept by dnsmasq: a later apply gets it at once.
timeout 20 init-firewall.sh apply >/dev/null || fail "an apply after the others waited for the lock"

# A pid file left behind, whose pid is now another process: that process is left alone.
sleep 600 & other=$!
kill "$(cat /run/dnsmasq.pid)"; while pgrep -x dnsmasq >/dev/null; do sleep 0.1; done
echo "$other" > /run/dnsmasq.pid
printf 'mode on\nexample.com\nstale.example.org\n' > /config/firewall.txt
init-firewall.sh apply >/dev/null || fail "apply with a stale pid file: exit $?"
kill -0 "$other" 2>/dev/null || fail "the stale pid's process was killed"
one_dnsmasq "after a stale pid file"
kill "$other"

# dnsmasq that ignores SIGTERM is still replaced.
kill -STOP "$(cat /run/dnsmasq.pid)"
printf 'mode on\nexample.com\nstopped.example.org\n' > /config/firewall.txt
init-firewall.sh apply >/dev/null 2>/tmp/err || fail "apply over a stopped dnsmasq: exit $?: $(cat /tmp/err)"
one_dnsmasq "after a dnsmasq that didn't stop"
echo "ok: firewall applies run one at a time, and each change leaves one dnsmasq running"
