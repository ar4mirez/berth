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
# Usage: bash -c "$(cat guard.sh)" guard run|apply|remove|status
set -u

api() { curl -fsS --unix-socket /var/run/docker.sock "http://docker$1"; }

# The iptables backend Docker uses: the one that has its DOCKER-USER chain.
backend() {
  local b
  for b in nft legacy; do
    if "iptables-$b" -w -S DOCKER-USER >/dev/null 2>&1; then echo "$b"; return 0; fi
  done
  return 1
}

org_bridges() {
  api '/networks' | jq -r '.[]
    | select(.Driver == "bridge")
    | select((.Labels // {})["com.docker.compose.project"] // "" | startswith("claude-"))
    | (.Options // {})["com.docker.network.bridge.name"] // ("br-" + .Id[0:12])'
}

apply() {
  local b ipt ifs i
  b=$(backend) || { echo "berth-guard: Docker's DOCKER-USER chain isn't there (yet)" >&2; return 1; }
  ipt="iptables-$b"
  ifs=$(org_bridges) || { echo "berth-guard: can't list Docker's networks" >&2; return 1; }
  {
    echo '*filter'
    echo ':BERTH-INPUT - [0:0]'
    echo ':BERTH-FORWARD - [0:0]'
    echo '-F BERTH-INPUT'
    echo '-F BERTH-FORWARD'
    for i in $ifs; do
      echo "-A BERTH-INPUT -i $i -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN"
      echo "-A BERTH-INPUT -i $i -j DROP"
      echo "-A BERTH-FORWARD -i $i -d 169.254.0.0/16 -j DROP"
    done
    echo 'COMMIT'
  } | "$ipt-restore" -w -n || return 1
  "$ipt" -w -C INPUT -j BERTH-INPUT 2>/dev/null || "$ipt" -w -I INPUT 1 -j BERTH-INPUT || return 1
  "$ipt" -w -C DOCKER-USER -j BERTH-FORWARD 2>/dev/null || "$ipt" -w -I DOCKER-USER 1 -j BERTH-FORWARD || return 1
}

remove() {
  local b ipt
  for b in nft legacy; do
    ipt="iptables-$b"
    while "$ipt" -w -D INPUT -j BERTH-INPUT 2>/dev/null; do :; done
    while "$ipt" -w -D DOCKER-USER -j BERTH-FORWARD 2>/dev/null; do :; done
    for c in BERTH-INPUT BERTH-FORWARD; do
      "$ipt" -w -F "$c" 2>/dev/null && "$ipt" -w -X "$c" 2>/dev/null
    done
  done
  return 0
}

status() {
  local b
  b=$(backend) || { echo "no DOCKER-USER chain"; return 1; }
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
