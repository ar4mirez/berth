# PRD: replace `docker compose` with the Engine API (#43)

Status: **phases 1-3 done** (the API path is opt-in); phase 4 next · Issue:
[#43](https://github.com/ar4mirez/berth/issues/43) · Restarts orgs: no, by design (the next ordinary restart uses the
new path)

## The decision this needed

Two standing rules said not to do this yet:
- **CLAUDE.md:** "Keep shelling out to `docker compose`. Don't reimplement compose or the pty."
- **Plan decision 3:** the Engine API is for reads only; replacing compose is a "v2 hardening" phase of its own.

**Decided on 2026-10-14:** the owner asked for every open issue to be cleared, one at a time, which lifts that rule
for #43.
- CLAUDE.md now names the Engine API path as the one exception to compose.
- `TestEngineAPICompatibility` keeps the two interchangeable.
- Each phase is its own change.

**What was weighed before deciding** (the recommendation then was to stop after phase 1). The issue's own
case is "control and fewer moving parts, not a blocker", and every part of berth works today with compose, remote
hosts included. Against that:
- **The parity harness** is built on compose's command line. About 60-80 scenarios compare it with ccenv's, and
  about 15 tests assert it exactly. Moving to the API means an HTTP fake for the engine and reworking those.
- **Rootless Podman** maps its user namespace through compose today. Whether Podman's Docker-compatible API
  accepts `UsernsMode: keep-id` on create isn't verified (risks, below).

Reasons that would change the recommendation:
- a host where the compose plugin can't be installed;
- compose behaviour berth has to work around;
- wanting `host add` to need nothing on the host but the engine and sshd.

## What berth does with compose today

**One service, `claude`, in `compose.yml`.** compose builds or pulls its image and creates its container and
network.
- Project `claude-<org>` (the file's `name:`), container `claude-<org>`, hostname `<org>`.
- `restart: unless-stopped`, `init: false`, `cap_add: [NET_ADMIN, NET_RAW]`.
- `env_file: <org dir>/org.env`, plus `ORG`, `HOST_UID`, `HOST_GID`.
- Two ports on `${BIND_ADDR}`: SSH to 2222, ttyd to 7681.
- Eight bind mounts under the org's directory (two read-only).
- `mem_limit`, `cpus`, and `userns_mode`/`user` for rootless Podman.
- The network is compose's implicit `claude-<org>_default`.

**Every change goes through `App.compose`** (`internal/app/compose.go`), which runs `docker compose -f <state>/berth/compose.yml --env-file <org.env> …`:

| Command | compose verb |
|---|---|
| `up` | `up -d --build --force-recreate`, or without `--build` when the release's image was pulled |
| `restart`, `token`, `logout --all`, `repo policy`, `env set` and friends, `restore` | `up -d --force-recreate` |
| `down`, `restore --force` | `down` |
| `destroy` | `down --volumes --remove-orphans` |
| `logs` | `logs -f` |
| `host add` | `compose version --short` (the check that the host can run orgs) |

**Without compose already:** `build`, the per-org package images, `pull`, `attach`/`shell`/`claude`/`run`
(`docker exec`), backups and the host guard (`docker run`), and the reads.
- `host.Engine` is a small read-only HTTP client over the engine socket. It works locally, or through an SSH
  streamlocal channel on a registered host. Today it serves only the published ports.
- **Apple's `container`** has no compose and no Docker API, so `internal/host/apple.go` already turns
  `compose.yml` into one `container run` by hand.

**What depends on compose's labels:**
- the host guard selects bridge networks by `com.docker.compose.project=claude-*` (`internal/hosts/guard.sh`);
- legacy `ccenv down` finds an org's container and network by project;
- `TestComposeLifecycle` checks the label, and `guard_test.go` names `claude-<org>_default`.

## Design

### One description of the org's container
- **`orgSpec`** (`internal/app`): the container the org runs, from its `org.env` and berth's settings. It holds the
  image, hostname, env, ports, mounts, limits, capabilities, user namespace, restart policy and labels.
- It replaces `compose.yml` as the source. `compose.yml` stays shipped, generated from `orgSpec`
  (`gendocs compose`), so legacy ccenv and a user with compose can still run an org.
- The Apple path builds its `container run` from the same `orgSpec`, which removes the hand-written second copy.

### Writes through the engine
`host.Engine` gains:
- `POST /containers/create`, `/containers/{id}/start`, `/containers/{id}/stop`;
- `DELETE /containers/{id}`;
- `POST /networks/create`, `DELETE /networks/{id}`;
- `GET /containers/{id}/logs?follow=1` (multiplexed stream).

These go over the same local socket or SSH streamlocal channel as the reads, so a registered host no longer needs
the compose plugin.

### Compatible with compose, both ways
- **Labels:** the container and the network get compose's labels:
  - `com.docker.compose.project=claude-<org>`, `com.docker.compose.service=claude`,
    `com.docker.compose.container-number=1`, `com.docker.compose.oneoff=False`;
  - `com.docker.compose.network=default` on the network.

  So the host guard, legacy `ccenv down`, and compose itself find them.
- **The network** keeps the name `claude-<org>_default`.
- **Switching doesn't restart anything.** berth always recreates on purpose (`--force-recreate`), so a switch
  recreates nothing by itself: the container compose made keeps running until the next ordinary restart, which the
  API does. The other way round, compose's `--force-recreate` replaces an API-made container cleanly. berth
  doesn't try to match compose's `config-hash` label, because nothing in berth relies on it.

### What changes for users
- **`berth logs`:** `docker logs -f claude-<org>`. compose prefixes each line with the service, and this won't.
  That's a PARITY divergence, recorded.
- **`up --build`:** an explicit `App.Build` before create (it's what compose does with `build:`), then create.
- **Errors:** compose's messages become berth's own, such as the port already allocated and the image missing.
  The parity scenarios that assert compose's stderr would record the difference.

## Phases

1. **`orgSpec` and an equivalence test.**
   - Build `orgSpec` from `org.env` and compare it with `docker compose config --format json` for the same org, in
     the integration job.
   - It changes nothing users see. It proves that one description covers both, and gives the Apple path its spec.
   - **Done:** `host.OrgSpec` (`internal/host/orgspec.go`, `TestNewOrgSpec`). Apple's `container run` is built from
     it. `TestOrgSpecMatchesCompose` (integration) compares it with `docker compose config` field by field, for
     ccenv's variables and for berth's image with rootless Podman and a CPU cap.
2. **The API path behind `BERTH_ENGINE_API=1`.**
   - Write calls on `host.Engine`.
   - `up`, `restart`, `down`, `destroy` and `logs` through it.
   - **The compatibility test the plan asks for:** create with compose and recreate with the API, then the other
     way, comparing `docker inspect` for Binds, PortBindings, Memory, NanoCpus, CapAdd, RestartPolicy, Env,
     Hostname, Image and the compose project label. Check that either side's `down` leaves no orphan container or
     network.
   - The integration tests run on both paths.
   - **Done** (`internal/host/composeapi.go`):
     - With `BERTH_ENGINE_API=1` on a Docker host, an org's compose `up`, `down` and `logs` go to the engine: create,
       start, stop, remove, the network, and the log stream. They are made from `OrgSpec`, with compose's labels.
       Every other command is unchanged, and so are Podman and Apple's `container`.
     - `TestComposeThroughEngineAPI` runs against a fake engine.
     - `TestEngineAPICompatibility` runs against Docker. It covers both directions, the inspected fields, no
       orphans, and no recreate on the switch.
     - The `engine API path` CI job runs `TestRemoteLifecycle` with the flag set, here and on a registered host
       over SSH. It checks that the API made the container.
3. **The parity harness on the API path:** an engine fake that records requests as calls the way `fakebin` records
   argv, and the scenarios moved over. This is most of the work.
   - **Done, without moving the scenarios.** The comparison with ccenv is a comparison with compose, so it stays on
     compose: the harness pins `BERTH_ENGINE_API=0`, as it pins `BERTH_SERVICE_MANAGER`.
   - `TestBerthEngineAPIPath` replays, on the API path, every ported scenario in which berth runs an org's compose
     `up`, `down` or `logs`. That's 18 today; the test fails below 15. Each replay runs against a fake engine on
     a unix socket, which records its requests.
   - The output, exit code and files must be the same. The calls must be the same except the compose ones, which
     must have become the API requests that do the same. compose `up --build` must have become one `docker build`.
   - Scenarios that fake compose's own answers (a port taken, a log) are left out: they test compose's side.
4. **On by default.** compose then stays only for `BERTH_ENGINE_API=0`, for one release, and `host add` stops
   requiring the compose plugin.

## Risks

- **Rootless Podman:** whether its Docker-compatible create accepts `UsernsMode: keep-id`.
  - If it doesn't, the libpod API (`/libpod/containers/create`) does, as a second request shape for Podman.
  - Or keep compose for rootless Podman alone.
- **Podman's compat API on older versions** (berth needs ≥4.7 today for `podman compose`): the minimum may change.
- **`logs -f`'s stream:** the multiplexed format (stdout and stderr frames) must be demultiplexed. A TTY container
  sends raw output instead, but berth's container has no TTY.
- **Test rework (phase 3)** is large, and it is the parity harness's main signal for these commands.

## Acceptance (from the issue)

| Criterion | Checked by |
|---|---|
| The integration tests pass on the API path | phase 2: the integration job with `BERTH_ENGINE_API=1` |
| Switching an org from compose to the API doesn't recreate its container | phase 2: an org up with compose, then `berth ls`/`info` on the API path, then its container ID unchanged until `restart` |
| `logs -f`, build, attach/shell behave as before | phase 2: the lifecycle test; attach and shell don't change (`docker exec`) |
