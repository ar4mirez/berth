# PRD: `remote restart` during the blocked-by-policy wait (#7)

Status: **done** · Issue: [#7](https://github.com/ar4mirez/berth/issues/7) · Restarts orgs: yes (image)

## Problem

When `claude remote-control` is refused by the Claude organization's policy, the supervisor in the container waits
an hour before it tries again. `remote <org> restart` only killed the service, and during that hour there is no
service to kill. So after an admin enabled Remote Control, the org stayed `blocked-by-org` for up to an hour,
through a re-login and a `remote restart`, until the container was recreated.

Killing the wait's `sleep` by hand made it worse: the supervisor was a subshell of the entrypoint, under its
`set -e`, so the killed `sleep` ended the loop for good.

## Objectives

1. `remote restart` and `login` always force an attempt, whatever the supervisor is waiting for.
2. `remote restart` says so when nothing restarted.
3. Nothing that fails or is killed under the supervisor ends it.
4. The hourly retry while blocked stays.

## Design

### In the image

`image/rc-supervisor.sh` replaces the loop in the entrypoint. It is its own script, without `set -e`. Its waits (5
seconds after an exit, an hour while blocked, 15 seconds with no login) are made of two-second steps, and each
step checks for `/run/rc-retry`: when the file is there, the wait ends and the service is started again. Only root
can write in `/run`, so the request comes from the host.

### In the CLI

- `remote restart`: ccenv's pkill, then `touch /run/rc-retry` in the container, then ccenv's line. It then watches
  the log for a new `starting remote-control` line, for up to ten seconds, and fails if none shows: with
  `it has no login` when that is why, else pointing at the logs. With `REMOTE_CONTROL=0` it refuses at once.
- `login`, `auth` and `logout` make the same request after their own pkill.

A full process supervisor (the issue's third suggestion) isn't added: the loop can no longer be ended by what runs
under it, which was the failure seen.

## Tasks

- [x] T1. `image/rc-supervisor.sh`; the entrypoint and Dockerfile use it.
- [x] T2. `remote restart`: the retry request and the check.
- [x] T3. `login`, `auth`, `logout`: the retry request.
- [x] T4. Tests: `TestBerthRemoteRestart`, contract checks, an `image.yml` step with a stand-in `claude`.
- [x] T5. Docs: troubleshooting; `PARITY.md`.

## Acceptance criteria

| Criterion (from the issue's fix direction) | How it is checked |
|---|---|
| The backoff is interruptible; `remote restart` and `login` signal the supervisor | The image step: a retry during the blocked hour starts the service within seconds. `TestBerthRemoteRestart`: the request follows the pkill, for `remote restart` and for `logout`. |
| `remote restart` confirms a new `starting remote-control` line, and says when there is none | `TestBerthRemoteRestart` |
| The loop can't disappear silently | The image step kills the service and the wait's `sleep` under the supervisor: it is still there and still retries. |
| berth keeps the hourly backoff | `RC_BLOCKED_WAIT` defaults to 3600, and the log still says `retrying in 1h`. |

## Live-org impact

The supervisor is in the image. It reaches an org after a host upgrade and that org's next restart, which stops the
work running in it. On a container started before that, the retry request is ignored: `remote restart` works as
before when the service is running (the pkill), and reports that nothing restarted when it is in its blocked wait.
