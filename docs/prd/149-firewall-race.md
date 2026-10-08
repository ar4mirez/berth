# PRD: firewall applies that overlap (#149)

Status: **done** · Issue: [#149](https://github.com/ar4mirez/berth/issues/149) · Restarts orgs: no (the fix reaches an org at its next restart)

## Problem

A `berth fw deny` about ten seconds after an org's first start failed once with
`dnsmasq: failed to create listening socket for 127.0.0.1: Address already in use`. Fourteen further tries didn't.

## Cause

`init-firewall.sh apply` had nothing to stop two runs at once: one from the host (`berth fw …`), and the
container's own (at start, and every five minutes). Two runs each stop dnsmasq and start their own; the second
finds the first one's on port 53. They also rebuild the same ipsets under each other (`The set with the given name
does not exist`). Eight applies at once reproduce both, every time.

Separately, a restart waited a fixed 0.2 s for the old dnsmasq to go, and trusted the pid file: a stale one names
whatever process has that pid now.

## Fix

- **One apply at a time**: a lock (`flock` on `/run/firewall.lock`) around the whole apply. A second one waits, up
  to two minutes, then fails without changing anything. dnsmasq is started without the lock's descriptor, or it
  would hold the lock for as long as it runs.
- **The restart waits for the old dnsmasq to exit** (up to 3 s, then `kill -9`), and tries the start once more if
  the port wasn't free yet.
- **A pid counts only while its process is dnsmasq**, so a stale pid file can't get another process killed.

## Tasks

- [x] T1. The lock, the wait, and the pid check in `image/init-firewall.sh`.
- [x] T2. `test/image/firewall_race.sh`, run in the image by CI: twenty changes back to back; three rounds of
      eight applies at once while the list changes; a later apply isn't kept waiting; a stale pid file; a
      dnsmasq that doesn't stop.
- [x] T3. The same test against the script before the fix: it fails, with the message from the issue.

## Acceptance criteria

| Criterion (from the issue) | How it is checked |
|---|---|
| The script waits for the old dnsmasq to exit (bounded), and two runs can't overlap | `firewall_race.sh`: concurrent applies all exit 0 and leave one dnsmasq on the last list; a stopped dnsmasq is replaced |
| A test that changes the firewall repeatedly, and during the container's own refresh | the same script, in `image.yml`. The refresh is an `apply` like any other, so the concurrent rounds are that case |

## Not explained

The failed `deny` in the issue ended with exit code 137. Neither race accounts for a kill, and the test never
produced one. With applies serialized, the conditions it happened under no longer exist.
