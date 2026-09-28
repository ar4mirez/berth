# Container engines

berth drives a container engine through its command line, with Docker's commands. Each host has one engine:

- **Docker** (the default);
- **Podman**, rootful or rootless (#57).

| Where | How to choose |
|---|---|
| This machine | `engine: podman` in `~/.config/berth/config.yaml`, or `BERTH_ENGINE=podman` |
| A registered host | detected at `berth host add` (Docker if it answers, else Podman), or `--engine podman`; recorded in `hosts.yaml` |

`berth host ls` shows each host's engine.

## What works where

| | Docker | Podman, rootful | Podman, rootless | Apple `container` |
|---|---|---|---|---|
| org lifecycle (`init`, `up`, `down`, `restart`, `attach`, `shell`, `claude`, `run`) | yes | yes | yes | not yet (see below) |
| the egress firewall (allow and block) | yes | yes | yes | – |
| the repo allowlist | yes | yes | yes | – |
| files in the org's folders stay yours | yes | yes | yes (`keep-id`, below) | – |
| backup and restore | yes | yes | yes | – |
| Remote Control, the browser terminal, SSH | yes | expected; not covered by CI | expected; not covered by CI | – |
| registered hosts (`org@host`) | yes | yes | yes | – |
| the host guard | yes | no: it uses Docker's `DOCKER-USER` chain (#97) | no (#97) | – |
| orgs start again after a reboot | yes (`restart: unless-stopped`) | with `podman-restart.service` enabled | with `systemctl --user enable podman-restart.service`, and linger | – |

CI runs the whole lifecycle on each Docker and Podman variant: the `docker` and `podman (rootful|rootless)` jobs,
`TestEngineLifecycle`. That covers start, the firewall allowing and blocking, file ownership, repo add, backup, restore
and down.

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

- **The host guard** is Docker-only for now: `berth host add` skips it on a Podman host and says so.
- **Restarts after a reboot** need `podman-restart.service` (above). With Docker, the daemon restarts `unless-stopped`
  containers itself.
- **Image names:** Podman keeps images under `localhost/…` and `docker.io/…` names. berth refers to its image by the
  short name, which Podman resolves to either.

## Apple `container` (spike)

Apple's `container` runs each container in its own lightweight VM (Virtualization.framework) on Apple silicon, from
macOS 26. It isn't supported yet.

This is a **desk spike**. It's based on the tool's documentation and design, not on hardware: CI has no macOS 26
runner yet, and berth hasn't been run against it. The gaps are tracked in #95 (a hands-on spike on macOS 26) and #96
(starting orgs without compose).

| Area | What we know | The gap for berth |
|---|---|---|
| Orchestration | No compose, and no Docker API socket | berth would start orgs itself (the Engine-API path, #43, is the pattern), with `container run` flags standing in for `compose.yml` |
| Networking | Each container gets its own IP on a virtual network; ports are published with `--publish` | Ports and `BIND_ADDR` map to `--publish`. Tailscale binding needs checking. |
| Firewall | Each container has its own kernel, so `iptables`/`ipset` in the container should behave as on Linux | To verify: `NET_ADMIN`, and DNS through the VM's resolver |
| Bind mounts | Shared through virtiofs | To verify: ownership (the uid remap, #38) and file-watching performance |
| Exec and TTY | `container exec -it` exists | To verify: `attach`, `shell`, `claude` over tmux |
| Images | Pulls OCI images from registries; builds with BuildKit | Should pull berth's released image as is |
| Backups | Needs a root container with the org's folder mounted | Likely works once bind mounts do |

The next step is #95: a hands-on spike on a macOS 26 Mac, turning each "to verify" into a yes or an issue.
