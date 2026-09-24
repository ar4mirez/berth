# berth

Isolated Claude Code environments, one per organization, on your laptop, an on-prem box, or a cloud VM.

Each org gets its own container with its own Claude account, git identity and key, and `~/.claude` history. A
default-deny egress firewall and a repo allowlist are enforced in three layers. You connect over tmux, SSH, a browser
terminal, VS Code Remote-SSH, or Claude Remote Control (claude.ai/code).

> **Status: pre-alpha.** berth is a Go rewrite of `ccenv`, a Bash tool that already runs this model in production. Until
> berth reaches full parity, the reference implementation is [`legacy/ccenv`](legacy/ccenv) and its docs are in
> [`docs/ccenv-legacy.md`](docs/ccenv-legacy.md). The roadmap is in [`docs/plan.md`](docs/plan.md).

## Layout

| Path | What |
|---|---|
| `image/` | Container image: Dockerfile, entrypoint, firewall, repo guard, backup engine |
| `compose.yml` | Per-org container definition |
| `legacy/ccenv` | The Bash reference implementation (runs as-is, used by the parity harness) |
| `docs/plan.md` | Architecture decisions, parity work items, phases |
| `scripts/drift.sh` | Host-only: diff `image/` against a legacy checkout |

## Why a rewrite

berth adds remote hosts, meaning orgs that run on on-prem machines or cloud VMs you provision from the CLI. That needs
a host abstraction (files + exec + Docker over SSH), a provider layer, and a single static binary. None of those fit
well in Bash.
