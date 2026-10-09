# berth

**Isolated Claude Code environments, one per organization**, on your laptop, a server of your own, or a cloud VM.

**Documentation: <https://ar4mirez.github.io/berth/>**

Each organization ("org") gets its own container, with its own:

- Claude account and Remote Control login;
- git identity and key;
- `~/.claude` history and memory;
- repos and toolchains.

Claude runs there without permission prompts, because the container is the sandbox:

- a **default-deny egress firewall** decides where it can connect;
- a **repo allowlist**, enforced in three layers, decides which repos it can touch.

```bash
berth org create acme --name "Ada Lovelace" --email ada@example.com
berth up acme
berth account signin acme        # the org's Claude account, then Remote Control
berth repo add acme acme/widgets
berth attach acme                # or: berth info acme, for every other way in
```

## Install

berth runs on Linux (amd64, arm64) and macOS (Apple silicon, Intel), and needs a container engine: Docker with
compose, Podman, or Apple's `container`.

```bash
brew install --cask ar4mirez/tap/berth        # macOS
mise use -g github:ar4mirez/berth             # anywhere mise runs
```

There is also an install script and apt, rpm and Arch packages. Every channel installs a signed release:
[Install berth](https://ar4mirez.github.io/berth/getting-started/#install-berth).

## What it does

| | |
|---|---|
| **Orgs** | Create, start, stop and destroy them; one command line for all of it: `berth <group> <verb> [<org>]`, with shortcuts for the everyday ones (`berth up`, `berth attach`, `berth ls`) |
| **Ways in** | tmux on the host, SSH from any device, a browser terminal, VS Code or Cursor over Remote-SSH, Claude Remote Control from claude.ai/code and the Claude app |
| **Egress firewall** | Default deny; allow hosts, ranges and presets (`@python`, `@go`, …), applied live |
| **Repo allowlist** | Only registered repos can be cloned, kept or pushed to |
| **Secrets** | Per-org variables and tokens; berth lists their names, never their values |
| **Backups** | Encrypted, scheduled, restorable on another machine |
| **Hosts** | Run orgs on other machines over SSH, move an org between them, and keep each host's containers away from the host itself |
| **Cloud hosts** | `berth host create` makes a VM at Hetzner Cloud that is closed to the internet and reached over Tailscale |
| **Engines** | Docker, Podman (rootful and rootless), Apple `container` |
| **Dashboards** | `berth tui` in the terminal and `berth ui` in the browser, made for a phone first: hosts, orgs, their firewall, repos, backups and logs |
| **For agents and scripts** | `berth mcp` (an MCP server with typed tools), `berth serve` (an HTTP API, as a service if you like), `--output json` |
| **Releases** | Signed, verified on install and on `berth system upgrade` |

## Documentation

| | |
|---|---|
| [Getting started](https://ar4mirez.github.io/berth/getting-started/) | Install berth, create your first org, connect to it |
| [What berth does](https://ar4mirez.github.io/berth/features/) | Every feature on one page, with where to read more |
| [Concepts](https://ar4mirez.github.io/berth/concepts/) | Orgs, hosts, the state root, the firewall, the repo allowlist, the security model |
| [The command line](https://ar4mirez.github.io/berth/cli/) | How commands are laid out, and the [reference](https://ar4mirez.github.io/berth/reference/commands/berth/) for each one |
| Guides | [Repos](https://ar4mirez.github.io/berth/guides/repos/), [the firewall](https://ar4mirez.github.io/berth/guides/firewall/), [secrets](https://ar4mirez.github.io/berth/guides/env/), [backups](https://ar4mirez.github.io/berth/guides/backups/), [hosts](https://ar4mirez.github.io/berth/hosts/), [engines](https://ar4mirez.github.io/berth/engines/), [networking](https://ar4mirez.github.io/berth/networking/), [the dashboard](https://ar4mirez.github.io/berth/guides/tui/), [the web dashboard](https://ar4mirez.github.io/berth/guides/web/), [MCP](https://ar4mirez.github.io/berth/guides/mcp/), [the API](https://ar4mirez.github.io/berth/api/) |
| [Troubleshooting](https://ar4mirez.github.io/berth/troubleshooting/) | Blocked hosts, DNS, Remote Control states, restore errors |

The sources are in [`docs/`](docs).

## Development

```bash
mise install                 # Go and the linters this repo pins
go build ./cmd/berth
go test ./...                # unit and scenario tests
golangci-lint run
```

Tests that need a real container engine, the image, or a cloud account run in CI (`.github/workflows`).
[`CLAUDE.md`](CLAUDE.md) has the rules for changes; [`docs/plan.md`](docs/plan.md) and the GitHub milestones have
the roadmap.

| Path | What |
|---|---|
| `cmd/berth`, `internal/` | The Go CLI: `cli` (commands), `app` (behaviour), `ops` (the operations the dashboard, MCP and API share), `host` (local and SSH), `hosts` (the registry), `cloud` |
| `image/`, `compose.yml` | The container image (firewall, repo guards, backup engine) and the per-org container definition |
| `test/` | `parity` (scenarios on fixtures), `integration` (real engines), `image` (inside the image), `cloud` (a real cloud host) |
| `docs/`, `mkdocs.yml` | The docs site; `docs/reference/commands` is generated (`go run ./tools/gendocs`) |

## License

[Apache-2.0](LICENSE).
