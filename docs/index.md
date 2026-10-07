# berth

**Isolated Claude Code environments, one per organization**, on your laptop, an on-prem box or a cloud VM.

Each org gets its own container, with its own:

- Claude account and Remote Control login;
- git identity and key;
- `~/.claude` history and memory;
- repos and toolchains.

Claude runs there without permission prompts, because the container is the sandbox:

- a **default-deny egress firewall** decides where it can connect;
- a **repo allowlist**, enforced in three layers, decides which repos it can touch.

You connect however suits you: tmux on the host, SSH from any device, a browser terminal, VS Code or Cursor over
Remote-SSH, or Claude Remote Control from claude.ai/code and the Claude app.

```bash
berth org create acme --name "Ada Lovelace" --email ada@example.com
berth up acme
berth account signin acme        # the org's Claude account, then Remote Control
berth repo add acme acme/widgets
berth attach acme                # or: berth info acme, for every other way in
```

## Where to start

| | |
|---|---|
| [Getting started](getting-started.md) | Install berth, create your first org, connect to it |
| [Concepts](concepts.md) | Orgs, hosts, the state root, the firewall, the repo allowlist, the security model |
| Guides | [Repos](guides/repos.md), [the firewall](guides/firewall.md), [secrets and env vars](guides/env.md), [backups](guides/backups.md), [hosts](hosts.md), [toolchains](guides/tools.md) |
| [Command reference](reference/commands/berth.md) | Every command, generated from `berth --help` |
| [Troubleshooting](troubleshooting.md) | Blocked hosts, Remote Control states, restore errors |
| [Coming from ccenv](host-install.md) | berth is ccenv's successor and runs the same orgs |

## What runs where

berth is a single binary that runs on your machine. It drives Docker with `docker compose`, either here or on a
registered host over SSH. Each org's files live under berth's **state root**; the container sees them through bind
mounts, so everything survives a restart or a rebuild, and moves with the org.
