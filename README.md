# berth

Isolated Claude Code environments, one per organization, on your laptop, an on-prem box, or a cloud VM.

Each org gets its own container with its own Claude account, git identity and key, and `~/.claude` history. A
default-deny egress firewall and a repo allowlist are enforced in three layers. You connect over tmux, SSH, a browser
terminal, VS Code Remote-SSH, or Claude Remote Control (claude.ai/code).

> **Status: pre-alpha.** berth is a Go rewrite of `ccenv`, a Bash tool that already runs this model in production. Until
> berth reaches full parity, the reference implementation is [`legacy/ccenv`](legacy/ccenv) and its docs are in
> [`docs/ccenv-legacy.md`](docs/ccenv-legacy.md). The roadmap is in [`docs/plan.md`](docs/plan.md).

## Trying berth (read-only)

The commands berth has so far only read ([`PARITY.md`](PARITY.md) tracks them). You can point them at an existing
ccenv checkout without changing anything there. Get a build that CI made: every `ci` run on `main` uploads the
linux/darwin archives, kept for 14 days. Don't use a workspace build.

```bash
run=$(gh run list -R ar4mirez/berth --workflow ci --branch main --status success --limit 1 --json databaseId,headSha -q '.[0]')
gh run download "$(jq -r .databaseId <<<"$run")" -R ar4mirez/berth -n "berth-$(jq -r .headSha <<<"$run")" -D berth-dl
cd berth-dl && sha256sum -c --ignore-missing checksums.txt    # macOS: shasum -a 256 -c --ignore-missing checksums.txt
tar xzf berth_*_linux_amd64.tar.gz berth                       # or _linux_arm64, _darwin_amd64, _darwin_arm64

./berth --read-only --home <legacy-checkout> ls                # also: info, whoami, repo ls, fw show
```

`--read-only` refuses every command that writes, and every file write underneath. The default state root
(`~/.local/share/berth`) is empty on purpose, so without `--home`, berth sees no orgs.

## Layout

| Path | What |
|---|---|
| `cmd/berth`, `internal/` | The Go CLI: `cli` (commands), `app` (behaviour), `host` (local and ssh), `org`, `repopolicy`, `config` |
| `test/parity` | Runs `legacy/ccenv` and berth on the same fixtures and diffs everything they do |
| `PARITY.md` | One row per ccenv command, subcommand and flag: what it does, and whether berth matches |
| `image/` | Container image: Dockerfile, entrypoint, firewall, repo guard, backup engine |
| `compose.yml` | Per-org container definition |
| `legacy/ccenv` | The Bash reference implementation (runs as-is, used by the parity harness) |
| `docs/plan.md` | Architecture decisions, parity work items, phases |
| `scripts/drift.sh` | Host-only: diff `image/` against a legacy checkout |

## Why a rewrite

berth adds remote hosts, meaning orgs that run on on-prem machines or cloud VMs you provision from the CLI. That needs
a host abstraction (files + exec + Docker over SSH), a provider layer, and a single static binary. None of those fit
well in Bash.
