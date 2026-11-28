# Decision: a supervised process's placement proof is an Ed25519-signed JWS (#216)

Decided 2026-11-27, for #216's placement proof. An external supervisor accepts that a runner runs in an org's
container only with proof it does: a runner that only says so could be any process on the host.

## What it must do

- Say which org, which of its containers (a recreated one is another), and which process.
- Be short-lived: at most 15 minutes, renewed while the process runs.
- Be verifiable by its consumer without a secret shared with the container: anything a container can read, a
  program in it can copy, so a shared secret proves nothing.
- Be signed by a key berth keeps on the host, outside every container.
- Never leave the process's container except to its consumer: not in an API answer, an event, a log or the audit log.

## The format

A compact JWS (RFC 7515) signed with Ed25519, `alg` `EdDSA` (RFC 8037):

```text
base64url({"alg":"EdDSA","typ":"berth-placement+jwt","kid":"<key id>"}) "."
base64url({"iss":"berth","org":…,"container":…,"container_started_at":…,"process":…,"iat":…,"exp":…,"nonce":…}) "."
base64url(signature over the first two parts)
```

Public keys are served at `GET /v1/placement/keys` (`berth.placement-keys/v1`): `id`, `alg` (`Ed25519`), the key's
32 bytes in base64, and `not_after`, when its last token expires.

## Why

- **Asymmetric, not an HMAC.** An HMAC needs the verifier to hold the secret, which would then be one more place
  for it to leak, and one a container might reach. With Ed25519 the consumer holds only public keys.
- **Ed25519.** Small keys and signatures, deterministic signing (no nonce to get wrong), and in Go's standard
  library (`crypto/ed25519`): no new dependency in berth, and one in nearly every consumer's language.
- **A compact JWS, not a format of berth's own.** It is a standard any JOSE library verifies, it travels as one line
  of text in a header or a request body, and its header names the key, so rotation needs nothing more. berth writes
  and checks it with the standard library alone, and refuses any `alg` but `EdDSA`, so the old JWT pitfalls (`none`,
  an HMAC key confused for a public one) don't apply.
- **The container's id and its start.** A container that is recreated keeps its name and gets a new id and
  `started_at`. The consumer compares `container_started_at` with `GET /v1/orgs/{org}`'s `started_at`, which it
  already reads, so a token from a container that has been replaced doesn't count.
- **15 minutes, renewed 5 minutes before the end, by `berth serve`.** Short enough that a copied token is of little
  use, long enough that a server restart doesn't break a running process. The signing key never enters the
  container, so the host side does the renewal, and only `berth serve` runs there for as long as processes do. No
  other daemon is added for it.
- **Asked for per start, and refused without a refresher.** A process must never run believing it has a proof it
  hasn't. So a token is only for a start that asks (`placement: true`, `--placement`), and such a start is refused
  (409 `state`) when nothing would keep its token fresh: no `berth serve` that isn't `--read-only` for the state
  root (a start inside one has it; the command line reads its heartbeat, `<state>/placement-serve.json`: its pid
  and its last round, live while the pid runs and the round is less than two minutes old, removed when it stops),
  an org on a registered host whose own berth can't be reached (it signs there, through the host API, never over
  ssh without a token), or Apple's `container` (no start time to name). Which processes to refresh is recorded
  next to each one, by berth-proc: `BERTH_PLACEMENT_FILE` among its variables, so a `berth serve` started again
  finds them.
- **Keys rotate every 7 days**, and the next key is made and listed `TokenTTL` + 1 hour before it signs, so a
  consumer that fetched the keys in that time verifies the tokens after the rotation. A consumer refetches the keys
  once when a token names a `kid` it doesn't have, which covers keys fetched earlier. An old key stays listed until
  its last token has expired (`not_after`), so verification never fails across a rotation. The keys are in berth's
  configuration (`~/.config/berth/placement/keys.json`, mode 0600), outside the state root that backups copy and
  every org's directory.

## What it doesn't do

- It doesn't prove what the process is, only where it runs: a program in the same container, as the same user,
  can read the token too. The container is the boundary it attests to.
- An org on a registered host gets its tokens from the berth on that host; the operator's berth doesn't sign for it.
- It isn't given to a process that didn't ask for one, and a process that asked is never started without one.
