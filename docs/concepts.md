# Concepts

## Orgs

An **org** is one isolated Claude Code environment: a container named `claude-<org>`, its files, and its settings in
`org.env`. Orgs share nothing with each other: not the Claude account, git key, `~/.claude`, repos, toolchains or
firewall.

Everything an org has lives in one folder, `<state root>/orgs/<org>/`:

```
orgs/<org>/
├── org.env          settings: identity, ports, Remote Control, repo policy, resources (0600)
├── config/          mounted read-only at /config; edits apply live
│   ├── firewall.txt     the egress allowlist (berth fw)
│   ├── repos.txt        the repo allowlist (berth repo)
│   ├── authorized_keys  devices allowed to SSH in
│   └── secrets/         the browser-terminal password; tokens and custom variables (berth env migrate)
├── workspace/       /workspace: the registered repos
├── claude/          ~/.claude: settings, sessions, memory, the Remote Control login
├── mise/            toolchains and build caches
├── home-config/     ~/.config: the gh login and other CLI config
├── ssh/             the org's git key (root-only inside)
├── sshd/            the container's SSH host keys (stable fingerprints)
└── quarantine/      whatever was removed from /workspace for not being a registered repo
```

The container can be rebuilt or recreated at any time, and nothing is lost: all of this is on the host.

## The state root

berth's **state root** holds `orgs/` and `backups/`. It's resolved in this order:

1. `--home`;
2. `$BERTH_HOME`;
3. `home:` in `~/.config/berth/config.yaml`;
4. `~/.local/share/berth`.

A machine that ran ccenv points `home:` at the old checkout, which then keeps working as berth's state root
([Cutover](cutover.md)). berth also keeps its generated files there (its compose file and the image's build context),
in `<state root>/berth/`, which is git-ignored.

## Hosts

A **host** is a machine that runs orgs:

- **`local`:** this machine, always there.
- **Registered hosts:** machines added with `berth host add`, reached over SSH with a key of berth's own. They need
  sshd, a POSIX shell, Docker and compose (or Podman), and nothing of berth's. `berth host create` makes one at a
  cloud provider.

`acme@box1` names the org `acme` on `box1`. Every org command takes that form. See [Hosts](hosts.md).

Once hosts are registered, each org has an **active-host lease**: the one host allowed to run it. Two running copies
would share one Claude login and git identity. `migrate` moves an org, and its lease, safely.

## The image

Every org runs berth's image. It holds:

- Claude Code, at a pinned version;
- sshd, ttyd and tmux;
- the firewall, the repo guards and mise;
- the backup engine.

**Releases:** each release publishes it, signed, as `ghcr.io/ar4mirez/berth-image`, and berth pulls it by digest.
Development builds build it locally.

**Updates:** a new image reaches an org only when that org's container is recreated (`up` or `restart`). berth never
restarts an org you didn't name. See [Image rollout](image-update.md).

## The egress firewall

Each container starts with **default-deny egress**. What's always allowed:

- Anthropic;
- GitHub;
- npm;
- whatever the org's `config/firewall.txt` lists: domains, IPs or CIDRs, and presets such as `@python` or `@go`.

The allowlist is re-resolved every five minutes, because CDN addresses change. Changes apply live, with no restart.
See [Firewall](guides/firewall.md).

On a registered host, the [host guard](hosts.md#the-host-guard) adds a second layer on the host itself. Org containers
can't reach the host's own services or cloud metadata (`169.254.169.254`).

## The repo allowlist

An org may only use the repos registered in `config/repos.txt`. It's enforced in three independent layers:

| Layer | What it stops |
|---|---|
| **git** | Clone, fetch or push of any unregistered repo, by anyone in the container. The org's key is root-only, and git reaches it only through a guard that checks the list. With a gh login, git uses that login's token over HTTPS instead, through a second guard that checks the same list ([Repos](guides/repos.md#how-git-reaches-github)). |
| **Claude** | Cloning, re-pointing remotes, reading unregistered `/workspace` folders, credential files, and code downloads from unregistered repos. A managed hook enforces it and can't be edited from inside. |
| **workspace** | Anything else placed in `/workspace`: moved to `quarantine/` every 30 seconds. Nothing is deleted. |

The container can't change the list, because `/config` is read-only. See [Repos](guides/repos.md) and, for how a repo
reference is matched, [Repo policy](repo-policy.md).

## The security model

- **The container is the sandbox.** Claude runs with `bypassPermissions`, because egress, repos and credentials are
  fenced at the container.
- **Nothing is shared across orgs:** not the Claude login, git key, `gh` login, history or toolchains.
- **Tokens and custom variables** can be kept as files that are mounted read-only, instead of in the container's
  environment, so `docker inspect` can't show them ([Secrets](secrets.md)).
- **SSH is key-only.** The browser terminal has a random 32-character password, kept in a 0600 file.
- **Ports bind to your tailnet** (or `127.0.0.1`), never to the public internet by default. Other VPNs and SSH tunnels
  work too ([Networking](networking.md)).
- **Tenancy.** Anyone who can run Docker on a host can read what its containers hold. An org is isolated from other
  orgs, not from the host's administrators. Run orgs for different clients on hosts you control.

## Backups and keys

Backups are encrypted by default:

- **With an age key:** `berth backup keygen` makes one at `~/.config/berth/backup.key`.
- **With a passphrase:** when no key is set up, or with `--passphrase`.

They skip what can be regenerated (toolchains, `node_modules`, caches) and keep everything else: repos with their
history, uncommitted work, config, keys and Claude's memory.

**The private key never leaves your machine.** A registered host that runs its own backup schedule only gets your
public key. See [Backups](guides/backups.md).
