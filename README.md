# berth

Isolated Claude Code environments, one per organization, on your laptop, an on-prem box, or a cloud VM.

Each org gets its own container, with its own:

- Claude account;
- git identity and key;
- `~/.claude` history;
- repos and toolchains.

A default-deny egress firewall and a repo allowlist are enforced in three layers. You connect over tmux, SSH, a
browser terminal, VS Code or Cursor Remote-SSH, or Claude Remote Control (claude.ai/code).

**Documentation: <https://ar4mirez.github.io/berth/>**, including [getting started](docs/getting-started.md),
[concepts](docs/concepts.md), guides, the [command reference](docs/reference/commands/berth.md) and
[troubleshooting](docs/troubleshooting.md). The sources are in [`docs/`](docs).

```bash
berth init acme --name "Ada Lovelace" --email ada@example.com
berth up acme
berth auth acme                  # the org's Claude account, then Remote Control
berth repo add acme acme/widgets
berth attach acme                # or: berth info acme, for every other way in
```

## Status

berth is the Go successor to `ccenv`, a Bash tool that ran this model in production, and it now runs those orgs.

- **Parity:** [`legacy/ccenv`](legacy/ccenv) stays in the repo as the reference. The parity suite runs both on the
  same fixtures, and [`PARITY.md`](PARITY.md) records every intended difference.
- **Beyond ccenv:** remote hosts, signed releases and self-upgrade.
- **Roadmap:** [`docs/plan.md`](docs/plan.md), and the GitHub milestones.

Install a signed release: [getting started](docs/getting-started.md#install-berth). Coming from ccenv:
[install next to it](docs/host-install.md), then [cut over](docs/cutover.md).

## Layout

| Path | What |
|---|---|
| `cmd/berth`, `internal/` | The Go CLI: `cli` (commands), `app` (behaviour), `host` (local and ssh), `hosts` (the registry), `org`, `repopolicy`, `config` |
| `image/`, `compose.yml` | The container image (firewall, repo guards, backup engine) and the per-org container definition |
| `test/parity`, `PARITY.md` | ccenv and berth on the same fixtures, diffed; one row per command and flag |
| `test/integration` | Real Docker (compose, sshd + dind hosts, backups), run in CI |
| `docs/`, `mkdocs.yml` | The docs site; `docs/reference/commands` is generated (`go run ./tools/gendocs`) |
| `legacy/ccenv` | The Bash reference implementation |
