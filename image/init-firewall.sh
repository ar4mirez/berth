#!/usr/bin/env bash
# Default-deny egress firewall driven by /config/firewall.txt (live-editable via `berth fw`).
#   init-firewall.sh apply    (re)build the allowlist and rules; safe to run anytime
#   init-firewall.sh presets  list available @presets
# File format: one entry per line. `mode on|off`, a domain, an IP/CIDR, or `@preset`.
set -euo pipefail

CONF=/config/firewall.txt
STATUS=/run/firewall.status

# Always allowed: Claude, GitHub, npm.
BASE="api.anthropic.com console.anthropic.com statsig.anthropic.com mcp-proxy.anthropic.com
claude.ai platform.claude.com downloads.claude.ai statsig.com sentry.io registry.npmjs.org
api.github.com raw.githubusercontent.com objects.githubusercontent.com codeload.github.com uploads.github.com"

declare -A PRESETS=(
  [mise]="mise.jdx.dev mise-versions.jdx.dev tuf-repo-cdn.sigstore.dev rekor.sigstore.dev fulcio.sigstore.dev github.com release-assets.githubusercontent.com objects.githubusercontent.com dl.google.com go.dev static.rust-lang.org sh.rustup.rs nodejs.org cache.ruby-lang.org www.python.org astral.sh"
  [ruby]="rubygems.org index.rubygems.org"
  [python]="pypi.org files.pythonhosted.org"
  [node]="registry.npmjs.org registry.yarnpkg.com nodejs.org"
  [go]="proxy.golang.org sum.golang.org storage.googleapis.com go.dev"
  [rust]="crates.io index.crates.io static.crates.io static.rust-lang.org"
  [docker]="registry-1.docker.io auth.docker.io production.cloudflare.docker.com ghcr.io pkg-containers.githubusercontent.com"
  [gitlab]="gitlab.com registry.gitlab.com"
  [bitbucket]="bitbucket.org api.bitbucket.org"
  [aws]="sts.amazonaws.com s3.amazonaws.com"
  [gcp]="oauth2.googleapis.com www.googleapis.com storage.googleapis.com"
  [azure]="login.microsoftonline.com management.azure.com"
  [debian]="deb.debian.org security.debian.org"
)

if [ "${1:-apply}" = "presets" ]; then
  for k in $(printf '%s\n' "${!PRESETS[@]}" | sort); do printf '@%-10s %s\n' "$k" "${PRESETS[$k]}"; done
  exit 0
fi

mode=on; entries="$BASE"
if [ -f "$CONF" ]; then
  while read -r line; do
    line="${line%%#*}"; line="$(echo "$line" | xargs)"; [ -n "$line" ] || continue
    case "$line" in
      "mode off") mode=off ;;
      "mode on")  mode=on ;;
      @*) p="${line#@}"; [ -n "${PRESETS[$p]:-}" ] && entries+=" ${PRESETS[$p]}" \
            || echo "firewall: unknown preset @$p" >&2 ;;
      *)  entries+=" $line" ;;
    esac
  done < "$CONF"
fi

# --- DNS-time allowlisting (#1) ---------------------------------------------------------------
# dnsmasq, on 127.0.0.1, becomes the container's only resolver. For an allowlisted name (and its
# subdomains) it adds each answer's IPs to the "allowed-dns" set before returning the answer, so a
# connection that follows a lookup is never blocked by an IP the one-shot resolution below didn't
# see (rotating pools, CDNs). Its upstream is the resolver Docker gave the container (127.0.0.11 on
# a compose network), saved once per container; DNS to anywhere else is blocked below.
UPSTREAM=/run/resolv.conf.upstream
[ -f "$UPSTREAM" ] || cp /etc/resolv.conf "$UPSTREAM"
dns_setup() {  # dns_setup <domain>...: (re)start dnsmasq for these names, point resolv.conf at it
  local conf=/run/berth-dnsmasq.conf new=/run/berth-dnsmasq.conf.new ns doms=""
  ipset create -exist allowed-dns hash:ip timeout 3600
  {
    echo "listen-address=127.0.0.1"; echo "bind-interfaces"; echo "no-resolv"; echo "cache-size=1000"
    for ns in $(awk '/^nameserver/ {print $2}' "$UPSTREAM"); do echo "server=$ns"; done
    for e in "$@"; do doms+="/$e"; done
    [ -n "$doms" ] && echo "ipset=$doms/allowed-dns"
  } > "$new"
  # Restart only when the names changed or it isn't running: the 5-minute refresh keeps its cache.
  if cmp -s "$new" "$conf" && [ -f /run/dnsmasq.pid ] && kill -0 "$(cat /run/dnsmasq.pid)" 2>/dev/null; then
    rm -f "$new"
  else
    mv "$new" "$conf"
    if [ -f /run/dnsmasq.pid ]; then kill "$(cat /run/dnsmasq.pid)" 2>/dev/null || true; sleep 0.2; fi
    dnsmasq --conf-file="$conf" --pid-file=/run/dnsmasq.pid --user=root
  fi
  { echo "nameserver 127.0.0.1"; grep -E '^(search|options)' "$UPSTREAM" || true; } > /etc/resolv.conf
}
domains=()
for e in $entries; do [[ "$e" =~ ^[0-9]+(\.[0-9]+){3}(/[0-9]+)?$ ]] || domains+=("$e"); done
dns_setup "${domains[@]}"

iptables -N CLAUDE-EGRESS 2>/dev/null || true
iptables -C OUTPUT -j CLAUDE-EGRESS 2>/dev/null || iptables -I OUTPUT 1 -j CLAUDE-EGRESS

if [ "$mode" = "off" ]; then
  iptables -F CLAUDE-EGRESS
  ip6tables -P OUTPUT ACCEPT 2>/dev/null || true
  echo "off" > "$STATUS"
  echo "firewall: OFF (all egress allowed)"
  exit 0
fi

# Build the new set while the old rules still allow DNS/GitHub, then swap atomically.
ipset create -exist allowed hash:net
ipset create -exist allowed-new hash:net; ipset flush allowed-new
if meta=$(curl -fsS --max-time 10 https://api.github.com/meta); then
  echo "$meta" | jq -r '(.web + .api + .git)[]' | grep -v ':' | while read -r c; do ipset add -exist allowed-new "$c"; done
else
  echo "firewall: WARNING could not fetch GitHub ranges" >&2
fi
for e in $entries; do
  if [[ "$e" =~ ^[0-9]+(\.[0-9]+){3}(/[0-9]+)?$ ]]; then
    ipset add -exist allowed-new "$e"; continue
  fi
  for ip in $(dig +short +time=2 +tries=2 A "$e" | grep -E '^[0-9]+(\.[0-9]+){3}$' || true); do
    ipset add -exist allowed-new "$ip"
  done
done
ipset swap allowed-new allowed && ipset destroy allowed-new

iptables -F CLAUDE-EGRESS
iptables -A CLAUDE-EGRESS -o lo -j ACCEPT
iptables -A CLAUDE-EGRESS -m state --state ESTABLISHED,RELATED -j ACCEPT
# DNS goes to dnsmasq (loopback, accepted above), which asks the upstream Docker gave the
# container. Only that upstream is reachable on port 53 when it isn't loopback: no DNS to arbitrary
# resolvers, which also closes DNS-based exfiltration.
for ns in $(awk '/^nameserver/ {print $2}' "$UPSTREAM"); do
  case "$ns" in 127.*) ;; *[!0-9.]*) ;; *)
    iptables -A CLAUDE-EGRESS -d "$ns" -p udp --dport 53 -j ACCEPT
    iptables -A CLAUDE-EGRESS -d "$ns" -p tcp --dport 53 -j ACCEPT ;;
  esac
done
subnet=$(ip -4 route | awk '!/default/ && /proto kernel/ {print $1; exit}')
[ -n "$subnet" ] && iptables -A CLAUDE-EGRESS -d "$subnet" -j ACCEPT
iptables -A CLAUDE-EGRESS -m set --match-set allowed dst -j ACCEPT
iptables -A CLAUDE-EGRESS -m set --match-set allowed-dns dst -j ACCEPT
iptables -A CLAUDE-EGRESS -j REJECT --reject-with icmp-admin-prohibited
ip6tables -P OUTPUT DROP 2>/dev/null || true
ip6tables -C OUTPUT -o lo -j ACCEPT 2>/dev/null || ip6tables -A OUTPUT -o lo -j ACCEPT 2>/dev/null || true

n=$(ipset list allowed | grep -cE '^[0-9]')
echo "on $n" > "$STATUS"
echo "firewall: ON ($n allowlisted networks)"
