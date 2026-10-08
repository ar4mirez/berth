# Container engines

berth drives a container engine through its command line, with Docker's commands. Each host has one engine:

- **Docker** (the default);
- **Podman**, rootful or rootless (#57);
- **Apple's `container`**, on this machine only, on Apple silicon (#96; [below](#apple-container)).

| Where | How to choose |
|---|---|
| This machine | `engine: podman` (or `container`) in `~/.config/berth/config.yaml`, or `BERTH_ENGINE=podman` |
| A registered host | detected at `berth host add` (Docker if it answers, else Podman), or `--engine podman`; recorded in `hosts.yaml` |

`berth host ls` shows each host's engine.

## What works where

| | Docker | Podman, rootful | Podman, rootless | Apple `container` |
|---|---|---|---|---|
| org lifecycle (`org create`, `up`, `down`, `restart`, `attach`, `shell`, `claude`, `org run`) | yes | yes | yes | yes, run by hand (below) |
| the egress firewall (allow and block) | yes | yes | yes | yes |
| the repo allowlist | yes | yes | yes | yes |
| files in the org's folders stay yours | yes | yes | yes (`keep-id`, below) | yes (virtiofs) |
| backup and restore | yes | yes | yes | yes |
| Remote Control, the browser terminal, SSH | yes | expected; not covered by CI | expected; not covered by CI | not verified: published ports (below) |
| registered hosts (`org@host`) | yes | yes | yes | no: this machine only |
| the host guard | yes | yes (#97) | no: its networks are out of the guard's reach (below) | – |
| orgs start again after a reboot | yes (`restart: unless-stopped`) | with `podman-restart.service` enabled | with `systemctl --user enable podman-restart.service`, and linger | no: `berth up` again |

CI runs the whole lifecycle on each Docker and Podman variant: the `docker` and `podman (rootful|rootless)` jobs,
`TestEngineLifecycle`. That covers start, the firewall allowing and blocking, file ownership, repo add, backup, restore
and down. The host guard is tested on a Docker host and on a rootful Podman host (`TestHostGuard`,
`TestHostGuardPodman`). Apple's `container` has no CI runner: what the table says of it was run by hand.

## Podman

**What you need:**

- **Podman** 4.7 or later, for `podman compose`.
- **A compose provider:** Docker's compose plugin or `docker-compose` (set `PODMAN_COMPOSE_PROVIDER` if it isn't on
  `PATH`).
- **Podman's API socket, which compose talks to:**
  - rootless: `systemctl --user enable --now podman.socket`;
  - rootful: `systemctl enable --now podman.socket`.

**Rootless.** Run berth as your own user. berth starts every container with `--userns=keep-id`, the orgs and its own
short-lived ones (the backup engine) alike, and with the user set to root:

- **`keep-id`:** your uid is the same inside, so everything the container writes in the org's folders is yours.
- **Root:** the entrypoint needs it for the firewall. It then runs Claude and your sessions as that same uid.

The container's memory and CPU limits (`MEM_LIMIT`, `CPUS`) need cgroup v2 with the cpu and memory controllers
delegated to your user, which is the default on current systemd distributions.

**Rootful.** Run berth as root. Everything else is as with Docker.

**Differences from Docker:**

- **The host guard** works on a rootful Podman host (#97): the same container, on Podman's socket, with its
  forwarding rules in `FORWARD` and DNS to the host let through (aardvark-dns listens on each bridge's gateway).
  On a **rootless** Podman host it isn't available, and `berth host add` says so: rootless networks live in the
  user's own network namespace, where a container with the host's network has no say, and the host is reached
  from there through pasta, not through the host's own firewall.
- **Restarts after a reboot** need `podman-restart.service` (above). With Docker, the daemon restarts `unless-stopped`
  containers itself.
- **Image names:** Podman keeps images under `localhost/…` and `docker.io/…` names. berth refers to its image by the
  short name, which Podman resolves to either.

## Apple `container`

Apple's `container` runs each container in its own lightweight VM, on Apple silicon, from macOS 26. berth drives it
on this machine with `engine: container` (or `BERTH_ENGINE=container`).

```bash
brew install container
container system start          # its service, and a kernel the first time
BERTH_ENGINE=container berth up acme
```

**How berth starts an org there.** `container` has no compose and no Docker API. berth runs the same commands as
with Docker, and maps each to what `container` has (`internal/host/apple.go`): what `compose.yml` sets becomes one
`container run` (the capabilities, `org.env`, the two ports on the bind address, the eight mounts, memory and
CPUs); `down` is stop and remove; the image is built with `container build` when it isn't there.

**Run by hand** on macOS 27 (Apple silicon) with `container` 1.5.0 (#95, 2026-10-08). There is no CI for it.

| Area | Result |
|---|---|
| Lifecycle | `up` (building the image), `restart`, `down`, `org destroy`, `ls`, `info`, `org exec`: work |
| Firewall | `NET_ADMIN` works in the VM: `iptables`, `ipset` and dnsmasq as on Linux. An allowlisted host answers, another times out, 169.254.169.254 is unreachable; `fw allow` and `fw test` apply live |
| Bind mounts | virtiofs. Files are yours on the Mac whoever writes them inside, and `node` gets your uid (the remap, #38). The mount itself takes no owner, mode or time, so the image no longer insists on them there |
| File watching | a change made inside the org is seen inside. **A change made on the Mac isn't**: no event crosses virtiofs |
| Exec and TTY | `container exec -it` gives a TTY; the tmux session is there; exit codes pass through |
| Images | berth's released image pulls and runs. Pulls from ghcr.io failed with HTTP/2 stream errors until limited to one download at a time, which berth does |
| Backups | `backup create` and `backup restore` work |
| Published ports | `container` lists them, and they listen. **On the test Mac no data passed**: its forwarder logged `connect failed: No route to host`, though the same address answered from a shell. That looks like macOS's Local Network permission for the forwarder; it wasn't resolved. The org's own address (`container ls`) answers directly from the Mac |

**What is different from Docker:**

- **No restart policy:** an org doesn't come back by itself after a reboot.
- **No hostname option:** the container's hostname is `claude-<org>`, not `<org>`.
- **CPUs are whole:** `CPUS=0.5` is rounded up to 1.
- **Each org has its own address** on a virtual network (192.168.64.x), reachable from the Mac whatever
  `BIND_ADDR` says. Binding to a Tailscale address wasn't verified, since published ports weren't.
- **This machine only:** a registered host is Docker or Podman.
