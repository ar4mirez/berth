# berth's host guard (#48): defence in depth under each org's own firewall, on a registered host.
# It runs as the berth-host-guard container (berth's image, host network, NET_ADMIN, the Docker
# socket) and keeps two chains in the host's filter table:
#   BERTH-INPUT   (jumped to from INPUT):       new connections from an org network to the host
#                                               itself (the bridge gateway, or any host address)
#                                               are dropped; replies still get through.
#   BERTH-FORWARD (jumped to from DOCKER-USER): traffic from an org network to 169.254.0.0/16
#                                               (cloud metadata, link-local) is dropped.
# Org networks are the bridge networks of compose projects named claude-<org>. Other containers
# on the host are left alone. Every 20 seconds (and whenever berth starts an org there) the chains
# are rebuilt in one iptables-restore, so new orgs are covered without a restart.
#
# On a rootful Podman (#97) the socket mounted is Podman's, and two things differ:
#   - there is no DOCKER-USER: BERTH-FORWARD is jumped to from FORWARD. netavark keeps its own
#     rules in its own table (nftables) or chains (iptables); a drop here holds whatever they accept.
#   - containers resolve names through aardvark-dns, which listens on the bridge's gateway, on the
#     host: DNS to the host is let through before the drop.
#
#
# On a rootless Podman (#153) org networks live in the user's own network namespace, and the host
# is reached from there through pasta, at 169.254.1.2 (host.containers.internal): the 169.254.0.0/16
# drop is what keeps an org from the host. The guard is still a container, started without a
# nested user namespace so that it may enter that namespace (BERTH_GUARD_NETNS) and keep the same
# chains there.
#
# That namespace exists only while a bridge container runs, and its file is a mount made inside
# Podman's own mount namespace: a container sees it only if it was there when the container
# started. So a guard that finds the namespace up but out of its sight (stale) ends, and its
# restart policy starts it again with a fresh view; berth does the same when it starts an org.
# With no org network at all there is nothing to guard, and apply says so quietly.
#
# Usage: bash -c "$(cat guard.sh)" guard run|apply|remove|status
set -u

SOCK=${BERTH_GUARD_SOCK:-/var/run/docker.sock}
NETNS=${BERTH_GUARD_NETNS:-}

api() { curl -fsS --unix-socket "$SOCK" "http://docker$1"; }

# ns runs a command where the rules live: the rootless network namespace, or right here.
ns() { if [ -n "$NETNS" ]; then nsenter --net="$NETNS" "$@"; else "$@"; fi; }
# no_ns: rootless, and its network namespace can't be entered from here.
no_ns() { [ -n "$NETNS" ] && ! nsenter --net="$NETNS" true 2>/dev/null; }
# stale: the namespace is up (pasta, which connects it, left its pid beside it) but this
# container started before it and can't see it.
stale() { no_ns && [ -e "$(dirname "$NETNS")/rootless-netns-conn.pid" ]; }

# podman: whether the socket is Podman's (its own API answers beside Docker's).
podman() { api '/libpod/_ping' >/dev/null 2>&1; }

# The chain BERTH-FORWARD is jumped to from.
forward_chain() { if podman; then echo FORWARD; else echo DOCKER-USER; fi; }

# The iptables backend to use: for Docker, the one that has its DOCKER-USER chain; for Podman, the
# one netavark's chains are in when it uses iptables, else nft (the kernel's own, where netavark's
# nftables table is).
backend() {
  local b
  if podman; then
    if ns iptables-legacy -w -S NETAVARK_FORWARD >/dev/null 2>&1; then echo legacy; else echo nft; fi
    return 0
  fi
  for b in nft legacy; do
    if ns "iptables-$b" -w -S DOCKER-USER >/dev/null 2>&1; then echo "$b"; return 0; fi
  done
  return 1
}

org_bridges() {
  local nets
  if podman; then
    # Podman's own API, which names each network's bridge (the Docker-compatible one doesn't).
    nets=$(api '/v4.0.0/libpod/networks/json') || return 1
    jq -r '.[]
      | select(.driver == "bridge")
      | select((.labels // {})["com.docker.compose.project"] // "" | startswith("claude-"))
      | .network_interface // empty' <<<"$nets"
    return
  fi
  nets=$(api '/networks') || return 1
  jq -r '.[]
    | select(.Driver == "bridge")
    | select((.Labels // {})["com.docker.compose.project"] // "" | startswith("claude-"))
    | (.Options // {})["com.docker.network.bridge.name"] // ("br-" + .Id[0:12])' <<<"$nets"
}

apply() {
  local b ipt ifs i fwd dns=
  if stale; then echo "berth-guard: the rootless network came up after this container: it must restart to see it" >&2; return 75; fi
  if no_ns; then return 0; fi
  b=$(backend) || { echo "berth-guard: Docker's DOCKER-USER chain isn't there (yet)" >&2; return 1; }
  ipt="iptables-$b"
  fwd=$(forward_chain)
  if podman; then dns=1; fi
  ifs=$(org_bridges) || { echo "berth-guard: can't list the engine's networks" >&2; return 1; }
  {
    echo '*filter'
    echo ':BERTH-INPUT - [0:0]'
    echo ':BERTH-FORWARD - [0:0]'
    echo '-F BERTH-INPUT'
    echo '-F BERTH-FORWARD'
    for i in $ifs; do
      echo "-A BERTH-INPUT -i $i -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN"
      if [ -n "$dns" ]; then
        echo "-A BERTH-INPUT -i $i -p udp --dport 53 -j RETURN"
        echo "-A BERTH-INPUT -i $i -p tcp --dport 53 -j RETURN"
      fi
      echo "-A BERTH-INPUT -i $i -j DROP"
      echo "-A BERTH-FORWARD -i $i -d 169.254.0.0/16 -j DROP"
    done
    echo 'COMMIT'
  } | ns "$ipt-restore" -w -n || return 1
  ns "$ipt" -w -C INPUT -j BERTH-INPUT 2>/dev/null || ns "$ipt" -w -I INPUT 1 -j BERTH-INPUT || return 1
  ns "$ipt" -w -C "$fwd" -j BERTH-FORWARD 2>/dev/null || ns "$ipt" -w -I "$fwd" 1 -j BERTH-FORWARD || return 1
}

remove() {
  local b ipt
  if no_ns; then return 0; fi
  for b in nft legacy; do
    ipt="iptables-$b"
    while ns "$ipt" -w -D INPUT -j BERTH-INPUT 2>/dev/null; do :; done
    while ns "$ipt" -w -D DOCKER-USER -j BERTH-FORWARD 2>/dev/null; do :; done
    while ns "$ipt" -w -D FORWARD -j BERTH-FORWARD 2>/dev/null; do :; done
    for c in BERTH-INPUT BERTH-FORWARD; do
      ns "$ipt" -w -F "$c" 2>/dev/null && ns "$ipt" -w -X "$c" 2>/dev/null
    done
  done
  return 0
}

status() {
  local b
  if stale; then echo "the rootless network came up after the guard started: it restarts within 20 seconds, or at the next berth up"; return 1; fi
  if no_ns; then echo "no org network yet: nothing to guard (rootless Podman makes its network namespace with the first org)"; return 0; fi
  b=$(backend) || { echo "no DOCKER-USER chain"; return 1; }
  ns "iptables-$b" -w -C "$(forward_chain)" -j BERTH-FORWARD 2>/dev/null || { echo "BERTH-FORWARD isn't hooked into $(forward_chain)"; return 1; }
  ns "iptables-$b" -w -S BERTH-INPUT && ns "iptables-$b" -w -S BERTH-FORWARD
}

case "${1:-run}" in
  apply) apply ;;
  remove) remove ;;
  status) status ;;
  run)
    trap 'exit 0' TERM INT
    while :; do
      apply
      [ $? -ne 75 ] || exit 75   # stale: end, and be started again with the namespace in sight
      sleep 20 & wait $!
    done ;;
  *) echo "usage: guard run|apply|remove|status" >&2; exit 2 ;;
esac
