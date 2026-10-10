# What berth does

Everything berth can do, on one page. Each part says what it is for, the commands you would type first, and where
to read more. Commands are laid out as `berth <group> <verb> [<org>]`; the everyday ones also work on their own
(`berth up acme` is `berth org up acme`). See [The command line](cli.md).

## Orgs

An org is one isolated Claude Code environment: a container with its own Claude account, git identity, history,
repos and toolchains ([Concepts](concepts.md#orgs)).

```bash
berth org create acme --name "Ada Lovelace" --email ada@example.com
berth up acme                 # start it; berth down, berth restart
berth ls                      # every org, on every host
berth org use acme            # a default org, so later commands can leave it out
berth org destroy acme        # remove it, with its history and secrets
```

## Signing in

Each org signs in to its own accounts, from inside its container ([Getting started](getting-started.md#your-first-org)).

```bash
berth account signin acme     # the org's Claude account, then Remote Control
berth account gh acme         # GitHub, for git and the gh CLI
berth account whoami          # which account each org uses
```

## Ways in

`berth info acme` prints every way to connect, with the org's own ports and addresses
([Getting started](getting-started.md#connecting)).

| From | How |
|---|---|
| This machine | `berth attach acme` (the org's tmux session), `berth shell acme`, `berth claude acme` |
| Any device | SSH, or the browser terminal |
| claude.ai/code, the Claude app | Remote Control |
| VS Code, Cursor | Remote-SSH |
| Scripts | `berth org run acme "your prompt"` |

Where the ports listen (Tailscale, another VPN, localhost only) is in [Networking](networking.md).

## The egress firewall

Nothing leaves an org's container unless it is allowed. Changes apply live ([The firewall](guides/firewall.md)).

```bash
berth fw show acme
berth fw allow acme @python pypi.org
berth fw deny acme pypi.org
berth fw test acme example.com
```

## The repo allowlist

Only registered repos can be cloned, kept or pushed to ([Repos](guides/repos.md)).

```bash
berth repo add acme acme/widgets
berth repo ls acme
berth repo rm acme widgets
```

## Secrets, packages and toolchains

```bash
berth env set acme API_KEY        # asks for the value; restarts a running org (--no-restart to wait)
berth env ls acme                 # names only: values are never shown
berth pkg add acme postgresql-client   # applied at the org's next restart
```

Read more: [Secrets and env vars](guides/env.md), [Secrets as files](secrets.md),
[System packages](guides/packages.md), [Toolchains and Claude Code](guides/tools.md),
[Profiles](guides/profiles.md) for orgs that start from a template.

## Backups

Encrypted, on a schedule, restorable here or on another machine ([Backups](guides/backups.md)).

```bash
berth backup keygen
berth backup create acme
berth backup schedule on
berth backup restore <file>
```

## Hosts

Run orgs on other machines over SSH. `acme@box1` is the org `acme` on the host `box1`, and every org command takes
that form ([Hosts](hosts.md)).

```bash
berth host add box1 ops@box1.example
berth org create acme@box1 --name "Ada Lovelace" --email ada@example.com
berth org migrate acme box1       # move an org, and the right to run it
berth host guard box1 status      # org containers can't reach the host itself
```

**Cloud hosts.** `berth host create box3 --provider hetzner` makes a VM at Hetzner Cloud that lets nothing in from
the internet and is reached over Tailscale; `berth host destroy` removes it
([Creating a host at a cloud provider](hosts.md#creating-a-host-at-a-cloud-provider)).

## Container engines

Docker, Podman (rootful and rootless) and Apple's `container` on Apple silicon ([Container engines](engines.md)).

## The dashboards

`berth tui` shows hosts and orgs in the terminal, with each org's firewall, repos, variables, backups and logs.
Anything that restarts a container asks first ([The dashboard](guides/tui.md)).

`berth ui` is the same in a browser, made for a phone first: on this machine with a link of its own, or from
another device through `berth serve --listen` and a token ([The web dashboard](guides/web.md)).

## For agents and scripts

| | |
|---|---|
| `berth mcp` | An MCP server: an agent manages orgs through typed tools, read-only unless you allow more ([MCP](guides/mcp.md)) |
| `berth serve` | The same operations over HTTP, on a local socket or over TLS with tokens ([The API](api.md)) |
| `berth system service install` | Keeps `berth serve` running, with systemd or launchd ([The API](api.md#running-it-as-a-service)) |
| `--output json` | Data from any command that returns some ([JSON output](json.md)) |
| `--read-only` | Refuses any command that would change state |

## berth itself

```bash
berth system upgrade              # to the newest signed release; --rollback goes back
berth system completion zsh       # shell completion
berth --version
```

Every release is signed, and checked on install and on upgrade ([Releases](releases.md)).
