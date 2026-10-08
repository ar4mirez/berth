# PRD: Apple `container`, and the host guard on Podman (#95, #96, #97)

Status: **done** (with what wasn't verified listed below) · Restarts orgs: no (the image changes reach an org at its next restart)

## #95: a hands-on spike of Apple `container`

Run on macOS 27.0.1, Apple silicon, `container` 1.5.0 (Homebrew), with berth's image. Each item of the issue:

| To verify | Result |
|---|---|
| The firewall: `NET_ADMIN`, `iptables`/`ipset`, DNS | **Yes.** berth's own script runs unchanged: 151 networks allowlisted, dnsmasq as the resolver, allow and block as on Linux |
| Bind mounts: ownership with the uid remap | **Yes**, after two image fixes: virtiofs refuses a chown, chmod or utime of the mount itself (its files are the host user's anyway). The entrypoint and the restore now accept that on virtiofs only |
| Bind mounts: file watching | **Partly.** Changes made inside are seen; changes made on the Mac send no event |
| `container exec -it` for attach, shell, claude | **Yes**: a TTY, tmux, exit codes |
| Port publishing and `BIND_ADDR` | **Not verified.** The ports listen but no data passed on the test Mac (the forwarder: `No route to host`; likely macOS's Local Network permission). Tailscale binding therefore not verified either |
| Pulling berth's released image | **Yes**, one download at a time: concurrent downloads from ghcr.io ended in HTTP/2 stream errors |
| Backups with the org folder mounted | **Yes**, create and restore (after the restore fix above) |

Also found: no `--restart` and no `--hostname`; a missing bind-mount source is an error (Docker creates it; berth
already creates them); `docker save` archives don't load (`container image load` wants one platform and no
attestation manifest).

## #96: starting orgs without compose

- **`engine: container`** (`BERTH_ENGINE=container`), this machine only.
- **`internal/host/apple.go`**: berth keeps running Docker's argv, and the engine layer maps each command to
  `container`'s, as it does for Podman by name. `compose up` becomes one `container run` with everything
  `compose.yml` sets; `compose down` is stop and remove; `compose logs`, `exec`, `run`, `rm`, `image inspect`,
  `pull`, `tag`, `build`, `logs`, `ps`, `inspect` and `version` each have their form. A command with no mapping is
  an error, never a guess. Published ports come from `container ls --format json`.
- compose builds a missing image, so `up` does too (`container build`).

## #97: the host guard on Podman

- **Rootful:** the same guard container, on Podman's socket (`/run/podman/podman.sock`).
  - Org bridges come from Podman's own API (`/libpod/networks/json`), which names each network's interface.
  - There is no `DOCKER-USER`: `BERTH-FORWARD` hangs from `FORWARD`. netavark keeps its rules in its own nftables
    table (or iptables chains); a drop in the filter table holds whatever they accept, so no `nft` is needed and
    the image is unchanged.
  - **DNS is let through**: Podman's containers resolve names through aardvark-dns on the bridge's gateway, which
    is the host. Without that the guard would have cut every org's DNS.
- **Rootless: not available**, and `host add` says why. Rootless networks live in the user's network namespace;
  a container with the host's network can't set rules there, and the host is reached from that namespace through
  pasta rather than through the host's firewall. Protecting it means rules inside that namespace, applied by
  something other than a container. Not done.

## Tasks

- [x] T1. The spike (#95), and `docs/engines.md` with its results.
- [x] T2. Image: the entrypoint and the restore accept a virtiofs mount (`image/entrypoint.sh`, `image/archive.sh`).
- [x] T3. The `container` engine (#96): `internal/host/apple.go`, its unit tests, and a run of berth on it.
- [x] T4. The guard on a rootful Podman (#97): `internal/hosts/guard.sh`, `internal/app/guard.go`.
- [x] T5. A Podman host fixture (sshd and a rootful Podman) and `TestHostGuardPodman`.

## Acceptance criteria

| Criterion | How it is checked |
|---|---|
| #95: each item is a yes in `docs/engines.md` or an issue | the table above and `docs/engines.md`; published ports and Tailscale binding are the "not verified" |
| #96: an org starts with `container run` flags standing in for `compose.yml`, ports read from `container ls` | `TestAppleStartsAnOrgAsComposeWould`, `TestAppleTranslatesDockersCommands`; by hand: `up`, `restart`, `down`, `ls`, `info`, `org exec`, `fw allow`/`test`, `repo ls`, `backup create`/`restore`, `org destroy` on a `t-*` org |
| #97: `TestHostGuard`'s checks pass on a Podman host (sshd + podman fixture) | `TestHostGuardPodman`: the same checks on the new fixture, plus DNS from the org's network and the `FORWARD` hook |

## Not done

- **Published ports on Apple `container`**: unverified, so SSH, the browser terminal and Remote Control through
  them are too.
- **The guard on a rootless Podman host.**
- **CI for Apple `container`**: there is no runner. Its unit tests run everywhere; the rest was by hand.
- `attach`, `shell` and `claude` on Apple `container` were checked as `exec -it` with a TTY, not typed at.
