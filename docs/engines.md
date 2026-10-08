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
| Remote Control, the browser terminal, SSH | yes | expected; not covered by CI | expected; not covered by CI | SSH and the browser terminal: yes, by hand. Remote Control: not tried (it needs a sign-in) |
| registered hosts (`org@host`) | yes | yes | yes | no: this machine only |
| the host guard | yes | yes (#97) | yes (#153, below) | – |
| orgs start again after a reboot | yes (`restart: unless-stopped`) | with `podman-restart.service` enabled | with `systemctl --user enable podman-restart.service`, and linger | no: `berth up` again |

CI runs the whole lifecycle on each Docker and Podman variant: the `docker` and `podman (rootful|rootless)` jobs,
`TestEngineLifecycle`. That covers start, the firewall allowing and blocking, file ownership, repo add, backup, restore
and down. The host guard is tested on a Docker host and on a Podman host, rootful and rootless (`TestHostGuard`,
`TestHostGuardPodman`, `TestHostGuardPodmanRootless`). Apple's `container` has no CI runner: what the table says of it was run by hand.

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

- **The host guard** works on a Podman host, with two differences from Docker:
  - **Rootful** (#97): the same container, on Podman's socket, with its forwarding rules in `FORWARD` and DNS to
    the host let through (aardvark-dns listens on each bridge's gateway).
  - **Rootless** (#153): org networks live in your user's own network namespace, and the host is reached from
    there through pasta, at `host.containers.internal` (169.254.1.2). The guard keeps the same rules inside that
    namespace, where the drop of 169.254.0.0/16 is what cuts an org off from the host. That namespace exists only
    while an org runs, and a container sees it only if it was there when the container started: so the guard
    restarts itself when it finds the namespace up but out of its sight (within about 20 seconds of an org
    coming back without berth, after a reboot, say), and berth restarts it when it starts an org. It runs with
    `SYS_ADMIN` in your user's namespace, which is no more than your user already has.
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
| Published ports | Work: an SSH login as `node` and the browser terminal (with its password) through `127.0.0.1`, and through a specific address when `BIND_ADDR` names one (then not through `127.0.0.1`). On the first day of testing they listened but passed no data (the forwarder: `connect failed: No route to host`); after `container system stop` and `start` they worked every time. If you see that, restart the service (#152) |

**What is different from Docker:**

- **No restart policy:** an org doesn't come back by itself after a reboot.
- **No hostname option:** the container's hostname is `claude-<org>`, not `<org>`.
- **CPUs are whole:** `CPUS=0.5` is rounded up to 1.
- **Each org has its own address** on a virtual network (192.168.64.x), which answers from the Mac itself whatever
  `BIND_ADDR` says. `BIND_ADDR` decides where the published ports listen, as with Docker; a Tailscale address
  wasn't tried (the test Mac has no Tailscale), a LAN address was.
- **This machine only:** a registered host is Docker or Podman.
