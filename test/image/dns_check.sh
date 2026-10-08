#!/usr/bin/env bash
# Inside a container of berth's image (NET_ADMIN, NET_RAW, a writable /config): the firewall's "is
# there DNS?" check asks again when a query gets no answer, and says so only when none does (#159).
set -euo pipefail
fail() { echo "FAIL: $*" >&2; exit 1; }
real=$(command -v dig)
mkdir -p /tmp/stub
# A dig that loses the check's queries: the first $LOSE of them, or every UDP one ($LOSE=udp), or
# all of them ($LOSE=all). Anything else is the real dig.
cat > /tmp/stub/dig <<STUB
#!/usr/bin/env bash
case "\$*" in *berth-dns-check-*) ;; *) exec $real "\$@" ;; esac
n=\$(( \$(cat /tmp/stub/asked 2>/dev/null || echo 0) + 1 )); echo "\$n" > /tmp/stub/asked
case "\${LOSE:-0}" in
  all) exit 9 ;;
  udp) case " \$* " in *" +tcp "*) ;; *) exit 9 ;; esac ;;
  *) [ "\$n" -gt "\$LOSE" ] || exit 9 ;;
esac
exec $real "\$@"
STUB
chmod +x /tmp/stub/dig
apply() {  # apply <LOSE>: one apply with that many lost queries; its exit code in $rc
  rm -f /tmp/stub/asked; rc=0
  LOSE=$1 PATH=/tmp/stub:$PATH init-firewall.sh apply >/tmp/out 2>/tmp/err || rc=$?
}
printf 'mode on\nexample.com\n' > /config/firewall.txt

apply 0
[ "$rc" = 0 ] && grep -qx "on [0-9]*" /run/firewall.status || fail "no loss: exit $rc, status '$(cat /run/firewall.status)': $(cat /tmp/err)"
[ "$(cat /tmp/stub/asked)" = 2 ] || fail "no loss: the check asked $(cat /tmp/stub/asked) times, not once before and once after"

# The first two queries are lost: the check before the build asks a third time, and the list is built.
apply 2
[ "$rc" = 0 ] && grep -qx "on [0-9]*" /run/firewall.status || fail "two lost: exit $rc, status '$(cat /run/firewall.status)': $(cat /tmp/err)"
! grep -q "DNS isn't answering" /tmp/err || fail "two lost: $(cat /tmp/err)"

# Every UDP query is lost: TCP still answers.
apply udp
[ "$rc" = 0 ] && grep -qx "on [0-9]*" /run/firewall.status || fail "UDP lost: exit $rc, status '$(cat /run/firewall.status)': $(cat /tmp/err)"

# Nothing answers: that is said, the exit is 3, and the addresses already allowed are kept (#98).
before=$(ipset list allowed | grep -c '^[0-9]')
apply all
[ "$rc" = 3 ] && grep -q "DNS not answering" /run/firewall.status || fail "no DNS: exit $rc, status '$(cat /run/firewall.status)'"
grep -q "DNS isn't answering" /tmp/err || fail "no DNS: no warning: $(cat /tmp/err)"
[ "$(ipset list allowed | grep -c '^[0-9]')" = "$before" ] || fail "no DNS: the allowlist changed"
curl -fsS -o /dev/null --max-time 15 https://example.com || fail "example.com isn't reachable after the applies"
echo "ok: the DNS check asks again after a lost query, falls back to TCP, and reports only when nothing answers"
