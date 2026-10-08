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
# Usage: bash -c "$(cat guard.sh)" guard run|apply|remove|status
set -u

api() { curl -fsS --unix-socket /var/run/docker.sock "http://docker$1"; }

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
    if iptables-legacy -w -S NETAVARK_FORWARD >/dev/null 2>&1; then echo legacy; else echo nft; fi
    return 0
  fi
  for b in nft legacy; do
    if "iptables-$b" -w -S DOCKER-USER >/dev/null 2>&1; then echo "$b"; return 0; fi
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
  } | "$ipt-restore" -w -n || return 1
  "$ipt" -w -C INPUT -j BERTH-INPUT 2>/dev/null || "$ipt" -w -I INPUT 1 -j BERTH-INPUT || return 1
  "$ipt" -w -C "$fwd" -j BERTH-FORWARD 2>/dev/null || "$ipt" -w -I "$fwd" 1 -j BERTH-FORWARD || return 1
}

remove() {
  local b ipt
  for b in nft legacy; do
    ipt="iptables-$b"
    while "$ipt" -w -D INPUT -j BERTH-INPUT 2>/dev/null; do :; done
    while "$ipt" -w -D DOCKER-USER -j BERTH-FORWARD 2>/dev/null; do :; done
    while "$ipt" -w -D FORWARD -j BERTH-FORWARD 2>/dev/null; do :; done
    for c in BERTH-INPUT BERTH-FORWARD; do
      "$ipt" -w -F "$c" 2>/dev/null && "$ipt" -w -X "$c" 2>/dev/null
    done
  done
  return 0
}

status() {
  local b
  b=$(backend) || { echo "no DOCKER-USER chain"; return 1; }
  "iptables-$b" -w -C "$(forward_chain)" -j BERTH-FORWARD 2>/dev/null || { echo "BERTH-FORWARD isn't hooked into $(forward_chain)"; return 1; }
  "iptables-$b" -w -S BERTH-INPUT && "iptables-$b" -w -S BERTH-FORWARD
}

case "${1:-run}" in
  apply) apply ;;
  remove) remove ;;
  status) status ;;
  run)
    trap 'exit 0' TERM INT
    while :; do
      apply || true
      sleep 20 & wait $!
    done ;;
  *) echo "usage: guard run|apply|remove|status" >&2; exit 2 ;;
esac
