# PRD: published ports on Apple `container`, and the host guard on a rootless Podman (#152, #153)

Status: **done** · Restarts orgs: no

## #152: published ports on Apple `container`

**They work.** Retested on the same Mac (macOS 27.0.1, `container` 1.5.0) after `container system stop` and
`start`:

| Check | Result |
|---|---|
| A plain container, `-p 127.0.0.1:2295:8000` | answers, with one container, with two, and after recreating one |
| berth's org: the browser terminal through `127.0.0.1` | 401 without the password, 200 with it |
| berth's org: SSH through `127.0.0.1` | a login as `node`, with the org's authorized key |
| `BIND_ADDR` set to the Mac's LAN address | SSH and the browser terminal answer there, and not on `127.0.0.1` |
| The forwarder's log | no `No route to host` |

**The first day's failure isn't explained.** Every attempt then was reset, with `connect failed: No route to
host` in the forwarder's log; none since. What changed in between was a restart of `container`'s service. It
wasn't macOS's Local Network permission, as #152 guessed: nothing was granted. The doc says what was seen and
what to do if it shows again.

Part of the first day's evidence was mine: `echo | nc` printed no SSH banner even when the port worked, so that
check proved nothing. The `curl` failures were real.

Not tried: a Tailscale address (the Mac has no Tailscale; a LAN address stands in for it), and Remote Control,
which needs a sign-in.

## #153: the host guard on a rootless Podman

**What an org can reach there** (Podman 5.8, pasta), measured in the fixture before any guard:

| From an org container | Result | Why |
|---|---|---|
| its bridge's gateway | not the host | the gateway is the user's network namespace |
| the host's own address | not the host | pasta gives the namespace that address too |
| `host.containers.internal` (169.254.1.2) | **the host** | pasta maps it |

So the one path to the host is inside 169.254.0.0/16, and the guard's existing forward rule cuts it, if it is
applied **inside the user's network namespace**.

**Design.** The guard stays a container, with the same script:

- It runs in the user's own namespace (`--userns=host`, not the nested one berth gives its other containers there)
  with `SYS_ADMIN`, and enters the rootless network namespace with `nsenter` for every rule.
- That namespace exists only while a bridge container runs, and its file is a mount inside Podman's own mount
  namespace: a container sees it only if it was there when the container started. (Tried and discarded: mount
  propagation, which doesn't reach it; and entering through a process that lives there, which the kernel
  refuses.) So:
  - a guard that finds the namespace up but out of its sight **ends**, and its restart policy starts it again
    with a fresh view;
  - berth restarts it when it starts an org and the apply fails for that reason.
- With no org network, there is nothing to guard, and it says so.

## Tasks

- [x] T1. #152: retest; `docs/engines.md`.
- [x] T2. #153: measure what a rootless org reaches; the design above in `guard.sh` and `guard.go`.
- [x] T3. The fixture gets a rootless user; `TestHostGuardPodmanRootless`.

## Acceptance criteria

| Criterion | How it is checked |
|---|---|
| #152: the cause confirmed, and what depends on published ports verified | by hand, the table above; the cause is not confirmed, and the doc says so |
| #153: `TestHostGuardPodman`'s checks pass for a rootless user on the fixture | `TestHostGuardPodmanRootless`: the same checks, with the host at 169.254.1.2; and the org restarted without berth, after which the rules come back by themselves and the host is still out of reach |
