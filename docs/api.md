# The API

`berth serve` serves berth's operations over HTTP, for tools that would otherwise run `berth` and read its output.
It is the same layer the command line, the dashboard and the MCP server use, under the same rules.

```bash
berth serve                 # listens on $XDG_RUNTIME_DIR/berth.sock (or ~/.config/berth/berth.sock)
berth --read-only serve     # the same, and nothing can change anything through it
```

```bash
curl --unix-socket "$XDG_RUNTIME_DIR/berth.sock" http://berth/v1/orgs
```

## Running it as a service

```bash
berth system service install      # berth serve now, and at each login
berth system service status
berth system service logs -f
berth system service uninstall
```

It installs `berth serve` as a service of your user: a **systemd user unit** (`berth-serve.service`) on Linux, a
**launchd agent** (`dev.berth.serve`) on macOS. It starts at once and at each login, and is restarted if it stops.

- **It survives upgrades**: the unit runs berth through the path you ran the install as (the link on your `PATH`),
  not a versioned file.
- **On Linux it lives with your login session.** Without lingering, a user unit stops at your last logout; the
  install says so, and how to change it: `loginctl enable-linger <you>`, which also starts it at boot.
- **It is given the few variables berth needs** when they are set where you install it (`DOCKER_HOST`,
  `XDG_CONFIG_HOME`, `BERTH_ENGINE`, `BERTH_SOCKET`, and on macOS your `PATH`): a service manager starts with
  almost nothing. Install it again after changing one.
- `berth system service install --listen 127.0.0.1:8443` has it serve [TCP](#over-the-network-tls-and-tokens) too.
- `status` ends with 0 when it is running, and 3 when it isn't or isn't installed.

With it running, `berth --via-daemon`, MCP over HTTP, and a berth elsewhere that has this machine as a
[registered host](hosts.md#a-host-that-runs-berth-serve) all have a server to talk to.

## Who may call it

The API listens on a **Unix socket only you can open** (mode 0600, created that way). There is no token on the
socket: whoever can open it is you, and could run `berth` anyway. A socket left behind by a server that is gone is
replaced; a file that isn't a socket, or a socket that still answers, is not.

Nothing listens on the network unless you ask.

### Over the network: TLS and tokens

```bash
berth serve token add ci --scope read          # prints the token once
berth serve --listen 127.0.0.1:8443            # the socket, and TCP
```

- **TLS only** (1.3). berth serves with the certificate you give (`--tls-cert`, `--tls-key`), or with one it makes the
  first time and keeps in `~/.config/berth/api/`. It prints the certificate's SHA-256 at start: a client pins that,
  or trusts `~/.config/berth/api/cert.pem` as its CA.
- **Every request needs a token**: `Authorization: Bearer <token>`. Without one, or with a wrong one, the answer is
  401, for a read too.
- **A token has a scope**, and does no more than it:

  | Scope | May |
  |---|---|
  | `read` (the default) | read |
  | `write` | also change things (firewall, repos, backups) |
  | `restart` | also start, restart and stop orgs, each still with `confirm` |
  | `admin` | also what can't be undone: destroying an org, still with `confirm` |

- **berth keeps only a token's hash** (`~/.config/berth/api/tokens.json`, 0600). `berth serve token ls` lists names,
  scopes and dates; `berth serve token rm <name>` removes one, and a running server refuses it at once.
- `--read-only` still wins: no token writes through a read-only server.

```bash
curl --cacert ~/.config/berth/api/cert.pem -H "Authorization: Bearer $TOKEN" https://127.0.0.1:8443/v1/orgs
```

Bind `--listen` to the address you mean: `127.0.0.1` for this machine, a VPN address for your tailnet. `0.0.0.0`
offers the API, behind its tokens, to every network the machine is on.

## The web dashboard

The same server has berth's [web dashboard](guides/web.md) at `/`: a page that is a client of this API and nothing
more. Its files are served without a token, because they hold no data; everything it shows it asks `/v1` for,
with the token you paste into it. `berth serve --no-ui` leaves it out, and `berth ui` serves it on this machine
alone, with a token of its own.

## The rules

Each endpoint is one operation from berth's catalog, and the catalog decides what a caller needs:

| The operation | |
|---|---|
| reads | always answers |
| writes | refused with `--read-only` (403) |
| restarts a container | also needs the org's name again as `confirm` in the body, or it is refused (403) and says what would stop |
| can't be undone (destroying an org) | also needs a caller that may do everything: the socket's owner, or a token with the `admin` scope |

- **Secret values are never returned.** Variables are listed by name; there is no endpoint that sets one.
- **Every change asked for is in the audit log** (`audit.log` in the state root) with `"via":"api"`: the endpoint's
  tool, its arguments, and whether it ran, failed or was refused.
- **An argument the endpoint doesn't have is refused** (400), in the query and in the body.

## Endpoints

Every path starts with `/v1`. Arguments in braces are in the path; `GET` endpoints take theirs in the query; the
others take a JSON body.

| Endpoint | | Needs | The command |
|---|---|---|---|
| `GET /v1/hosts` | This machine and every registered host: whether each is reachable, its container engine, and how many orgs it has | read | `berth host ls` |
| `GET /v1/orgs` | Every org, here and on registered hosts: whether it is up, its ports, whether it has a Claude token, and its Remote Control state | read | `berth ls` |
| `GET /v1/accounts` | Which Claude account and GitHub account each org is signed in to | read | `berth whoami` |
| `GET /v1/backups` | The backup files on this machine, newest first: org, when, size and how each is encrypted | read | `berth ` |
| `POST /v1/backups` | Take an encrypted backup of orgs on this machine, into the backups directory | write | `berth backup` |
| `GET /v1/schedule` | The nightly backup schedule on this machine: a systemd timer and its last runs, or a crontab line | read | `berth schedule status` |
| `GET /v1/orgs/{org}` | An org's connection sheet: its state, address and ports, the claude.ai/code link, and its git public key | read | `berth info` |
| `GET /v1/orgs/{org}/remote` | Remote Control in a running org: on, login-needed, blocked-by-org or restarting, with its link and capacity when on | read | `berth remote status` |
| `GET /v1/orgs/{org}/logs` | The last lines of a running org's container log (startup, firewall, Remote Control) | read | `berth logs` |
| `GET /v1/orgs/{org}/firewall` | An org's egress allowlist as written, and the live firewall status of its container | read | `berth fw show` |
| `GET /v1/orgs/{org}/firewall/presets` | The firewall's @presets and the hosts each one allows | read | `berth fw presets` |
| `POST /v1/orgs/{org}/firewall/test` | Whether a running org can reach each host or URL now, tried from inside its container | read | `berth fw test` |
| `POST /v1/orgs/{org}/firewall/allow` | Add entries to an org's egress allowlist | write | `berth fw allow` |
| `POST /v1/orgs/{org}/firewall/deny` | Remove entries from an org's egress allowlist, exactly as written there | write | `berth fw deny` |
| `GET /v1/orgs/{org}/repos` | An org's registered repos, with branch and uncommitted changes when its container reports them, and anything in its workspace that isn't registered | read | `berth repo ls` |
| `POST /v1/orgs/{org}/repos` | Register a repository for an org and clone it into /workspace | write | `berth repo add` |
| `DELETE /v1/orgs/{org}/repos/{dir}` | Unregister a repo from an org | write | `berth repo rm` |
| `GET /v1/orgs/{org}/env` | The names of an org's custom environment variables | read | `berth env ls` |
| `GET /v1/orgs/{org}/packages` | The system packages an org's image adds, and that image | read | `berth pkg ls` |
| `POST /v1/orgs/{org}/up` | Start an org: gets its image if needed, then recreates its container | restart | `berth up` |
| `POST /v1/orgs/{org}/restart` | Restart an org: recreates its container, applying pending changes (a new image, packages, variables) | restart | `berth restart` |
| `POST /v1/orgs/{org}/down` | Stop an org: stops and removes its container | restart | `berth down` |
| `POST /v1/orgs` | Create an org: its directory, keys and settings. It isn't started, and has no Claude account yet | write | `berth org create` |
| `DELETE /v1/orgs/{org}` | Remove an org for good: its container, workspace, history, keys, secrets, and its backups here | admin | `berth org destroy` |
| `POST /v1/orgs/{org}/firewall/on` | Turn an org's egress firewall on | write | `berth fw on` |
| `POST /v1/orgs/{org}/firewall/off` | Turn an org's egress firewall off: it can reach anything until it is on again | write | `berth fw off` |
| `POST /v1/orgs/{org}/firewall/reload` | Apply an org's allowlist again in its running container, resolving its names anew | write | `berth fw reload` |
| `POST /v1/orgs/{org}/repos/sync` | Clone an org's registered repos that aren't in its /workspace yet | write | `berth repo sync` |
| `GET /v1/packages/presets` | The @presets for system packages and the packages each one stands for | read | `berth pkg presets` |
| `POST /v1/orgs/{org}/packages` | Add system packages to an org's image, and build it; they are there after its next restart | write | `berth pkg add` |
| `POST /v1/orgs/{org}/packages/remove` | Remove entries from the system packages an org's image adds | write | `berth pkg rm` |
| `POST /v1/orgs/{org}/remote/restart` | Restart Remote Control inside a running org: the service, not the container | write | `berth account remote restart` |
| `GET /v1/orgs/{org}/logs/follow` | Follow an org's container log (an event stream) | read | `berth logs` |
| `GET /v1` | What is serving, and what this caller may do | read | |
| `GET /v1/openapi.json` | The OpenAPI document | read | |

An org is named as on the command line: `acme`, or `acme@box1` for one on a registered host.

Results are the documents `--output json` prints ([JSON output](json.md)). An endpoint that changes something also
returns what berth printed doing it.

```bash
s="$XDG_RUNTIME_DIR/berth.sock"
curl --unix-socket "$s" "http://berth/v1/orgs/acme/logs?lines=50"
curl --unix-socket "$s" -X POST -d '{"entries":["pypi.org","@python"]}' http://berth/v1/orgs/acme/firewall/allow
curl --unix-socket "$s" -X POST -d '{"confirm":"acme"}' http://berth/v1/orgs/acme/restart
```

## Errors

A failure is a `berth.error/v1` document, as `--output json` prints on stderr, with a status for its kind:

| Status | Kind | |
|---|---|---|
| 400 | `usage` | the arguments don't fit |
| 401 | `refused` | on TCP: no token, or a wrong one |
| 403 | `refused` | read-only, a token without the scope, or a restart without `confirm` |
| 404 | `not-found` | no such org, host or endpoint |
| 409 | `not-running`, `state` | the org isn't in a state for this |
| 500 | `command`, `failed` | the operation failed |

```json
{
  "schema": "berth.error/v1",
  "kind": "not-found",
  "code": 1,
  "message": "unknown org 'nope'",
  "hint": "run: berth org create nope"
}
```

## Progress, as events

An endpoint that changes something answers when it ends. Ask for `Accept: text/event-stream` and it sends the
operation's progress as it goes: the [`berth.event/v1`](json.md#progress-bertheventv1) events (`start`, `step`,
`output`, then `done` or `failed`), and after `done` a `result` event with the document.

```bash
curl -N --unix-socket "$s" -H 'Accept: text/event-stream' -X POST -d '{"confirm":"acme"}' http://berth/v1/orgs/acme/up
```

`GET /v1/orgs/{org}/logs/follow` is always a stream: one `output` event per log line, until you disconnect.

## The command line, through a running server

```bash
berth --via-daemon ls
berth --via-daemon fw allow acme pypi.org
berth --via-daemon --output json info acme
```

`--via-daemon` sends the command to the server on its socket, which runs it there and sends back what it prints and
its exit code: the same text, JSON, messages and hints as running it directly. `BERTH_SOCKET` names the socket when
the server was started with `--socket`.

- **What can go** is what the API has endpoints for: the reads; the firewall, repos and packages; backups; creating
  and destroying an org; and up, restart and down. What needs a terminal (`shell`, `attach`, `claude`, sign-ins, `tui`), or has
  no endpoint (`env set`, `host add`), says so and doesn't run.
- The server's rules apply: a read-only server refuses a write. Typing the command is the confirmation, so a
  restart needs no `confirm` here.
- A write through it is in the audit log, as `cli <operation>` with its arguments.

`POST /v1/cli` is the endpoint behind it (`{"args": […], "output": "text"}`, answered as `stdout`, `stderr` and
`exit` events). It is for berth's own command line: other programs should use the endpoints above.

## MCP over HTTP

The [MCP server](guides/mcp.md) is also at `/mcp`, for a client that speaks MCP's streamable HTTP transport:

```bash
claude mcp add --transport http berth https://127.0.0.1:8443/mcp --header "Authorization: Bearer $TOKEN"
```

It has the same tools as `berth mcp`: the API's, without what can't be undone (`org_destroy`), which no MCP server
offers, whatever the token. What they may do is the caller's: on TCP the token's scope (`read`, `write`,
`restart`), checked at every request; on the socket, everything `--read-only` allows. `--allow-writes` and
`--allow-restarts` are `berth mcp`'s flags, for stdio, and don't apply here.

## The OpenAPI document

[`api/openapi.json`](api/openapi.json) (OpenAPI 3.1) describes every endpoint, its arguments and its documents; a
running server has it at `/v1/openapi.json`. It is generated from the same table the server's routes are, and a
test keeps the committed copy current, so the document and the handlers can't disagree. Each operation carries
`x-berth-access` (`read`, `write`, `restart` or `admin`) and `x-berth-operation` (the catalog's name for it).

berth's own Go client (`internal/apiclient`) is generated from that table too. It is internal to this repository
for now: the documents' Go types are. From another program, generate a client from the OpenAPI document.
