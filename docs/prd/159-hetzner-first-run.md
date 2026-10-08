# PRD: what the first run on Hetzner found (#159)

Status: **done** · Issue: [#159](https://github.com/ar4mirez/berth/issues/159) · Restarts orgs: no (the firewall fix reaches an org at its next restart)

## Problem

`berth host create` ran against Hetzner Cloud for the first time (#52). It worked: the server came up closed to the
internet, joined the tailnet and was registered, and a `t-*` org on it started and was isolated. Two things were
wrong on the way.

1. **A false "DNS isn't answering".** The org's firewall warned at start and recorded `DNS not answering`, while
   names resolved. The provider's IPv4 resolvers dropped a share of UDP queries from the server (between a quarter
   and five in six, in bursts of twelve); TCP, IPv6 and public resolvers answered every one. The check was one
   `dig` with two 2-second tries over UDP.
2. **`org create acme@box1` printed next steps without the host**: `berth up acme`.

## Why the first one matters

The same check runs before the allowlist is built. Failing there skips the build and keeps "the addresses already
allowed" (#98), which at an org's first start is none.

## Fix

- **The check asks again**: up to three lookups of a new name (two tries each), then one over TCP. It says "no DNS"
  only when none is answered, which takes about 15 seconds instead of 4.
- **The next steps use the address the org was given**, `acme@box1`, wherever a command is to be typed.
- `docs/hosts.md` says what has and hasn't been run against Hetzner, and asks for an auth key that isn't reusable.
- `.envrc` is ignored by git: it is where the two secrets for `host create` tend to live.

## Tasks

- [x] `dns_answers` retries and falls back to TCP (`image/init-firewall.sh`)
- [x] `test/image/dns_check.sh`, run in the image workflow: no loss, two lost, all UDP lost, nothing answers
- [x] `org create` on a host names the host in its next steps; asserted in `test/integration`
- [x] docs: `hosts.md`, `guides/firewall.md`

## Not tested

- The fix was not re-run on a Hetzner server: the test loses queries with a stand-in for `dig`.
- Whether the resolvers' loss is usual there, or was caused by the test's own bursts of queries, isn't known.
- `host destroy` and `host reconcile` have still not run against Hetzner.
