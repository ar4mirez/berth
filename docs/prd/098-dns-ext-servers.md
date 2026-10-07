# PRD: DNS when Docker forwards to a resolver that isn't loopback (#98)

Status: **done** · Issue: [#98](https://github.com/ar4mirez/berth/issues/98) · Restarts orgs: yes (image)

## Problem

Since #1, an org's only resolver is dnsmasq in its container, and the firewall lets DNS out only to the resolver
Docker gave the container. On a compose network that resolver is Docker's own, `127.0.0.11`, which resolves nothing
itself: it forwards to its **ExtServers**.

When the host's resolver isn't a loopback stub (a `dns` setting in `daemon.json`, a LAN router, a VPN resolver),
Docker sends those forwarded queries **from the container's network namespace**, through the firewall's chain. The
firewall had a port-53 rule only for the `nameserver` lines, and none of them is the ExtServer. So every lookup hit
the final `REJECT`: no DNS at all, Remote Control offline, and the firewall still said `on`.

## Objectives

1. DNS works on such hosts, and still goes only to the resolver Docker gave the container.
2. A firewall that leaves the container without DNS says so, instead of looking fine.
3. A DNS outage doesn't empty the allowlist.

## Design (`image/init-firewall.sh`)

- **ExtServers get a port-53 rule.** Docker lists them in the resolv.conf it generates, which berth saves once per
  container: `# ExtServers: [172.17.0.1]`. A plain address there is asked from the container, and gets a rule
  (IPv6 ones in `ip6tables`). A `host(…)` one is asked from the host's namespace, needs no rule, and gets none.
- **A check after the rules are in place.** One lookup of a name that is new each time, so no cache can answer:
  an answer or "no such name" passes. If nothing answers, the status is `on N (DNS not answering)`, a warning names
  the resolvers, and `apply` ends with exit code 3, as it does for a skipped entry (#104).
- **No DNS, no rebuild.** The same check runs before the allowlist is rebuilt. With no DNS, nothing would resolve
  (at four seconds a name) and the swap would install an empty allowlist: the addresses already allowed are kept
  instead, until a rebuild that can resolve.
- **Zero networks is a count.** With nothing allowlisted, the script ended on `grep -c`'s exit status, with no
  status at all.

## Tasks

- [x] T1. Reproduce: a container on a user-defined network with `--dns 1.1.1.1` has no DNS once the rules are in.
- [x] T2. Port-53 rules for ExtServers; `host(…)` and IPv6 forms.
- [x] T3. The DNS check, the status and exit code 3.
- [x] T4. Keep the allowlist through an outage; don't end on an empty one.
- [x] T5. An `image.yml` step with the reproduction, the outage and the parsing; it fails on the old script.
- [x] T6. Docs: the firewall guide and troubleshooting.

## Acceptance criteria

| Criterion | How it is checked |
|---|---|
| An org on a host whose Docker forwards to a non-loopback resolver has DNS | The CI step: `--dns 1.1.1.1`, a fresh name resolves through dnsmasq and through `127.0.0.11` after the rules are in, and after a rebuild. The same step fails on the script before this change. |
| DNS still goes only to that resolver | The same step: another resolver (`9.9.9.9`) is refused. |
| The forms Docker writes are handled | The step's second half: `[host(10.9.8.7) 10.9.8.6, fd00::53]`. |
| A firewall without DNS fails loudly | The step drops the resolver's traffic: exit 3, `DNS not answering` in the status, in under a minute, with the allowlist kept. |

## Live-org impact

The script is in the image. It reaches an org after a host upgrade and that org's next restart, which stops the
work running in it. Orgs given the workaround (`berth fw <org> allow 172.17.0.1/32`) can drop it after that restart:
`berth fw <org> deny 172.17.0.1/32`.
