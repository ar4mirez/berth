#!/usr/bin/env bash
# Default-deny egress firewall driven by /config/firewall.txt (live-editable via `berth fw`).
#   init-firewall.sh apply    (re)build the allowlist and rules; safe to run anytime
#   init-firewall.sh presets  list available @presets
# File format: one entry per line. `mode on|off`, a domain (or `*.domain`), an IPv4 address or range,
# or `@preset`. An entry that is none of these is skipped with a warning, and the rest still applies
# (#104). Exit codes: 0 applied; 3 applied, but something was skipped, DNS allowlisting is down, or
# DNS doesn't answer (#98); anything else, not applied.
set -euo pipefail
set -f   # entries are split on spaces and may hold a `*`: never expand them against the filesystem

CONF=/config/firewall.txt
STATUS=/run/firewall.status

# Always allowed: GitHub, npm, and the model provider's endpoints (Anthropic's unless the file has a
# `provider` line, which `berth org create --profile` writes).
BASE="registry.npmjs.org
api.github.com raw.githubusercontent.com objects.githubusercontent.com codeload.github.com uploads.github.com"
ANTHROPIC="api.anthropic.com console.anthropic.com statsig.anthropic.com mcp-proxy.anthropic.com
claude.ai platform.claude.com downloads.claude.ai statsig.com sentry.io"
# provider_hosts <name> [region]: a provider's endpoints; fails for an unknown name or missing region.
provider_hosts() {
  local r="${2:-}"
  case "$1" in
    anthropic)  echo "$ANTHROPIC" ;;
    bedrock)    [ -n "$r" ] || return 1
                echo "bedrock-runtime.$r.amazonaws.com bedrock.$r.amazonaws.com sts.$r.amazonaws.com sts.amazonaws.com" ;;
    vertex)     [ -n "$r" ] || return 1
                echo "$r-aiplatform.googleapis.com aiplatform.googleapis.com oauth2.googleapis.com www.googleapis.com" ;;
    openrouter) echo "openrouter.ai" ;;
    *) return 1 ;;
  esac
}

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

# One apply at a time (#149). A change from the host can arrive while the container's own refresh
# runs (entrypoint.sh), and two runs rebuilding the sets and restarting dnsmasq at once leave
# neither's result: the second one's dnsmasq found the first one's still on port 53.
exec 9>/run/firewall.lock
if ! flock -w 120 9; then
  echo "firewall: another apply has been running for two minutes; this one was not applied" >&2
  exit 1
fi

mode=on; entries="$BASE"; provider="$ANTHROPIC"
if [ -f "$CONF" ]; then
  while read -r line; do
    line="${line%%#*}"; line="$(echo "$line" | xargs)"; [ -n "$line" ] || continue
    case "$line" in
      "mode off") mode=off ;;
      "mode on")  mode=on ;;
      "provider "*) read -r _ pname pregion _ <<< "$line"
            if h=$(provider_hosts "$pname" "$pregion"); then provider="$h"
            else echo "firewall: bad provider line '$line' (anthropic | bedrock <region> | vertex <region> | openrouter)" >&2; fi ;;
      @*) p="${line#@}"; [ -n "${PRESETS[$p]:-}" ] && entries+=" ${PRESETS[$p]}" \
            || echo "firewall: unknown preset @$p" >&2 ;;
      *)  entries+=" $line" ;;
    esac
  done < "$CONF"
fi
entries+=" $provider"

# An entry is an IPv4 address or range, or a name dnsmasq can match: a hostname, optionally with a
# leading `*.`. Anything else is skipped: one name dnsmasq refuses would cost the container its
# resolver (#104). `berth fw allow` applies the same rule before it writes the file.
IP_RE='^[0-9]+(\.[0-9]+){3}(/[0-9]+)?$'
LABEL='[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?'
NAME_RE="^(\\*\\.)?$LABEL(\\.$LABEL)*\$"
skipped=(); dns_down=""
skip() {  # skip <entry> <why>
  skipped+=("$1"); echo "firewall: skipping '$1': $2" >&2
}
ips=(); domains=()
for e in $entries; do
  if [[ "$e" =~ $IP_RE ]]; then ips+=("$e")
  elif [[ "$e" =~ $NAME_RE ]]; then domains+=("$e")
  elif [[ "$e" == *"*"* ]]; then skip "$e" "a wildcard only works as a leading '*.' (list each host, or allow the whole domain, like '*.example.com')"
  elif [[ "$e" == *:* ]]; then skip "$e" "IPv6 addresses and ports aren't supported"
  else skip "$e" "not a hostname, an IPv4 address or range, or an @preset"
  fi
done

# --- DNS-time allowlisting (#1) ---------------------------------------------------------------
# dnsmasq, on 127.0.0.1, becomes the container's only resolver. For an allowlisted name (and its
# subdomains) it adds each answer's IPs to the "allowed-dns" set before returning the answer, so a
# connection that follows a lookup is never blocked by an IP the one-shot resolution below didn't
# see (rotating pools, CDNs). Its upstream is the resolver Docker gave the container (127.0.0.11 on
# a compose network), saved once per container; DNS to anywhere else is blocked below.
UPSTREAM=/run/resolv.conf.upstream
[ -f "$UPSTREAM" ] || cp /etc/resolv.conf "$UPSTREAM"
# dns_upstreams: the resolvers this container's DNS traffic goes to, one per line: the upstream
# file's nameservers, then Docker's ExtServers, which it writes as "[1.2.3.4 host(5.6.7.8) fd00::1]".
# A host(...) one is asked from the host's network namespace, not from here, so it needs no rule
# and gets none.
dns_upstreams() {
  awk '/^nameserver/ {print $2}' "$UPSTREAM"
  sed -n 's/^# ExtServers: \[\(.*\)\].*$/\1/p' "$UPSTREAM" | tr ',' ' ' | tr -s ' ' '\n' \
    | grep -E '^[0-9a-fA-F:.]+$' || true
}
# dns_answers: whether a lookup gets an answer from upstream. The name is new each time, so no cache
# can answer for it, and "no such name" is an answer. One lost packet isn't "no DNS": a resolver
# that drops some UDP queries (seen at a cloud provider, #159) is asked again, then over TCP. With
# no DNS at all this takes about 15 s.
dns_asked() {
  dig +noall +comments "$@" "berth-dns-check-$RANDOM$RANDOM.example.com" 2>/dev/null \
    | grep -qE 'status: (NOERROR|NXDOMAIN)'
}
dns_answers() {
  local i
  for i in 1 2 3; do
    ! dns_asked +time=2 +tries=2 || return 0
  done
  dns_asked +tcp +time=3 +tries=1
}
dns_conf() {  # dns_conf <file> <domain>...: dnsmasq's config, with one ipset line per name
  local f="$1" ns e; shift
  {
    echo "listen-address=127.0.0.1"; echo "bind-interfaces"; echo "no-resolv"; echo "cache-size=1000"
    for ns in $(awk '/^nameserver/ {print $2}' "$UPSTREAM"); do echo "server=$ns"; done
    # dnsmasq matches a name and everything under it, and has no wildcard for sets: `*.example.com`
    # goes in as `example.com`.
    for e in "$@"; do echo "ipset=/${e#\*.}/allowed-dns"; done
  } > "$f"
}
dns_ok() { dnsmasq --test --conf-file="$1" >/dev/null 2>&1; }
# dns_pid: the running dnsmasq's pid, or nothing. A pid file can outlive its process, and the pid
# be another process's by then: it counts only while that process is dnsmasq.
dns_pid() {
  local pid; pid=$(cat /run/dnsmasq.pid 2>/dev/null) || return 1
  case "$pid" in ''|*[!0-9]*) return 1 ;; esac
  [ "$(cat "/proc/$pid/comm" 2>/dev/null)" = dnsmasq ] && echo "$pid"
}
dns_running() { dns_pid >/dev/null; }
# dns_stop: stop dnsmasq and wait until it is gone, so the next one finds port 53 free (#149: a
# fixed 0.2 s wasn't always enough).
dns_stop() {
  local pid; pid=$(dns_pid) || return 0
  kill "$pid" 2>/dev/null || true
  for _ in $(seq 1 30); do [ -e "/proc/$pid" ] || return 0; sleep 0.1; done
  kill -9 "$pid" 2>/dev/null || true
  for _ in $(seq 1 10); do [ -e "/proc/$pid" ] || return 0; sleep 0.1; done
}
# dns_start: start dnsmasq on the config in place. It doesn't keep the apply lock (9), which it
# would hold for as long as it runs.
dns_start() { dnsmasq --conf-file="$1" --pid-file=/run/dnsmasq.pid --user=root 9>&-; }
dns_setup() {  # dns_setup <domain>...: (re)start dnsmasq for these names, point resolv.conf at it
  local conf=/run/berth-dnsmasq.conf new=/run/berth-dnsmasq.conf.new e ok=()
  ipset create -exist allowed-dns hash:ip timeout 3600
  # dnsmasq checks the config before it replaces the running one: a config it refuses must never
  # leave the container without a resolver (#104).
  dns_conf "$new" "$@"
  if ! dns_ok "$new"; then
    dns_conf "$new"
    if dns_ok "$new"; then
      # A name the check above let through, and dnsmasq refuses: find it, so it costs only itself.
      for e in "$@"; do
        dns_conf "$new" "$e"
        if dns_ok "$new"; then ok+=("$e"); else skip "$e" "dnsmasq can't match this name"; fi
      done
      dns_conf "$new" "${ok[@]}"
    fi
  fi
  # Restart only when the names changed or it isn't running: the 5-minute refresh keeps its cache.
  if cmp -s "$new" "$conf" && dns_running; then
    rm -f "$new"
  elif dns_ok "$new"; then
    mv "$new" "$conf"
    dns_stop
    rm -f /run/dnsmasq.pid
    # Once more if it couldn't start: the port can take a moment to be free after a kill -9.
    dns_start "$conf" || { sleep 0.5; dns_start "$conf" || true; }
    for _ in 1 2 3 4 5 6 7 8 9 10; do dns_running && break; sleep 0.2; done
  else
    dns_down="DNS allowlist not updated"
    echo "firewall: dnsmasq refuses its config, kept as $new:" >&2
    dnsmasq --test --conf-file="$new" 2>&1 | sed 's/^/firewall:   /' >&2 || true
  fi
  if dns_running; then
    { echo "nameserver 127.0.0.1"; grep -E '^(search|options)' "$UPSTREAM" || true; } > /etc/resolv.conf
  else
    # No dnsmasq: names still resolve, through the resolver Docker gave the container, and reach
    # what the one-shot resolution below allows. Only the DNS-time allowlisting is lost.
    dns_down="no DNS allowlisting"; cat "$UPSTREAM" > /etc/resolv.conf
    echo "firewall: WARNING dnsmasq isn't running; using the container's own resolver (rotating IP pools may be blocked)" >&2
  fi
}
dns_setup "${domains[@]}"

# finish <status> <message>: record the status, and end with 3 if something was skipped.
finish() {
  local problems=""
  [ ${#skipped[@]} -eq 0 ] || problems="${#skipped[@]} skipped"
  [ -z "$dns_down" ] || problems+="${problems:+, }$dns_down"
  # The rules are in place: a lookup must still get through. A firewall that looks fine while
  # nothing resolves is the worst outcome (#98).
  if [ -z "${dns_ok-unchecked}" ] || ! dns_answers; then
    problems+="${problems:+, }DNS not answering"
    echo "firewall: WARNING DNS isn't answering: names can't be resolved in this container (resolvers: $(dns_upstreams | tr '\n' ' '))" >&2
  fi
  echo "$1${problems:+ ($problems)}" > "$STATUS"
  echo "firewall: $2"
  [ -n "$problems" ] || exit 0
  [ ${#skipped[@]} -eq 0 ] || echo "firewall: WARNING skipped (see above): ${skipped[*]}. Remove each with: berth fw deny ${ORG:-<org>} '<entry>'" >&2
  exit 3
}

iptables -N CLAUDE-EGRESS 2>/dev/null || true
iptables -C OUTPUT -j CLAUDE-EGRESS 2>/dev/null || iptables -I OUTPUT 1 -j CLAUDE-EGRESS

if [ "$mode" = "off" ]; then
  iptables -F CLAUDE-EGRESS
  ip6tables -P OUTPUT ACCEPT 2>/dev/null || true
  finish off "OFF (all egress allowed)"
fi

# Build the new set while the old rules still allow DNS/GitHub, then swap atomically.
ipset create -exist allowed hash:net
ipset create -exist allowed-new hash:net; ipset flush allowed-new
# With no DNS at all (the upstream is down, or can't be reached), nothing below would resolve, at
# four seconds a name, and the swap would replace the allowlist with an empty one. The addresses
# already allowed are kept instead, until a rebuild that can resolve (#98).
dns_ok=1; dns_answers || dns_ok=""
if [ -n "$dns_ok" ]; then
  if meta=$(curl -fsS --max-time 10 https://api.github.com/meta); then
    echo "$meta" | jq -r '(.web + .api + .git)[]' | grep -v ':' | while read -r c; do ipset add -exist allowed-new "$c"; done
  else
    echo "firewall: WARNING could not fetch GitHub ranges" >&2
  fi
  for e in "${ips[@]}"; do
    ipset add -exist allowed-new "$e" 2>/dev/null || skip "$e" "not an IPv4 address or range"
  done
  for e in "${domains[@]}"; do
    case " ${skipped[*]} " in *" $e "*) continue ;; esac   # dnsmasq refused it above
    e="${e#\*.}"
    for ip in $(dig +short +time=2 +tries=2 A "$e" | grep -E '^[0-9]+(\.[0-9]+){3}$' || true); do
      ipset add -exist allowed-new "$ip"
    done
  done
  ipset swap allowed-new allowed && ipset destroy allowed-new
else
  echo "firewall: WARNING no DNS: names weren't resolved again, and the addresses already allowed are kept" >&2
  for e in "${ips[@]}"; do
    ipset add -exist allowed "$e" 2>/dev/null || skip "$e" "not an IPv4 address or range"
  done
  ipset destroy allowed-new
fi

iptables -F CLAUDE-EGRESS
iptables -A CLAUDE-EGRESS -o lo -j ACCEPT
iptables -A CLAUDE-EGRESS -m state --state ESTABLISHED,RELATED -j ACCEPT
# DNS goes to dnsmasq (loopback, accepted above), which asks the upstream Docker gave the
# container. Only that upstream is reachable on port 53: no DNS to arbitrary resolvers, which also
# closes DNS-based exfiltration. The upstream is the file's nameservers that aren't loopback, and
# the resolvers Docker's own DNS (127.0.0.11) forwards to: it lists them as "# ExtServers: [...]",
# and it sends those queries from this network namespace, through this chain, whenever the host's
# resolver isn't a loopback stub (a `dns` setting in daemon.json, a LAN or VPN resolver). Without a
# rule for them, every lookup fails (#98).
for ns in $(dns_upstreams); do
  case "$ns" in
    127.*) ;;
    *:*) ip6tables -C OUTPUT -d "$ns" -p udp --dport 53 -j ACCEPT 2>/dev/null || ip6tables -A OUTPUT -d "$ns" -p udp --dport 53 -j ACCEPT 2>/dev/null || true
         ip6tables -C OUTPUT -d "$ns" -p tcp --dport 53 -j ACCEPT 2>/dev/null || ip6tables -A OUTPUT -d "$ns" -p tcp --dport 53 -j ACCEPT 2>/dev/null || true ;;
    *[!0-9.]*) ;;
    *) iptables -A CLAUDE-EGRESS -d "$ns" -p udp --dport 53 -j ACCEPT
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

n=$(ipset list allowed | grep -cE '^[0-9]' || true)   # 0 networks is a count, not a failure (no DNS at all)
finish "on $n" "ON ($n allowlisted networks)"
