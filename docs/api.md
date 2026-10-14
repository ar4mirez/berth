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
  not a versioned file. A server that is running keeps its version until it is restarted, though. After
  `berth system upgrade`, it refuses every change, so it can't put an org back on the older release's image. Reads
  still work. Restart it with `systemctl --user restart berth-serve` or
  `launchctl kickstart -k gui/$(id -u)/dev.berth.serve`.
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
  | `admin` | also what can't be undone (destroying an org, still with `confirm`), and what takes a secret: a variable's value, a Claude token, the API's own tokens |

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

- **A secret is taken, and kept out of everything else.** A caller with the `admin` scope can set a variable, give
  an org its Claude token, or rotate a password; none of those values is ever in an operation's events or in the
  audit log, which has that it was set and not what to. Variables are listed by name. Two secrets are given
  back, each to a caller with the `admin` scope and to nobody else:
  - an **API token**, once, when it is made: berth keeps only its hash;
  - an org's **browser-terminal password** (`GET /v1/orgs/{org}/password`), when it is asked for: a dashboard
    links to that terminal, and the link is no use without it. Each time it is shown is in the audit log.
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
| `GET /v1/orgs/{org}/repos/audit` | An org's workspace checked against its registered repos: the policy, what isn't registered, what is in quarantine | read | `berth repo audit` |
| `POST /v1/orgs/{org}/repos/adopt` | Register folders already in an org's /workspace, or bring them back from its quarantine | write | `berth repo adopt` |
| `POST /v1/orgs/{org}/repos/new` | Create a new repository for an org and register it: on GitHub, or a local one to publish later | write | `berth repo new` |
| `POST /v1/orgs/{org}/repos/policy` | Set what happens to folders that aren't registered repos; a running org is restarted to apply it | restart | `berth repo policy` |
| `DELETE /v1/orgs/{org}/env/{name}` | Remove one of an org's custom variables; a running org is restarted unless `no_restart` | restart | `berth env unset` |
| `GET /v1/orgs/{org}/remote/logs` | The last lines of Remote Control's log in a running org | read | `berth account remote logs` |
| `POST /v1/orgs/{org}/logout` | Sign an org out of Remote Control; with `all`, remove its Claude token too | restart | `berth account logout` |
| `POST /v1/orgs/{org}/packages/build` | Build an org's image with its system packages now | write | `berth pkg build` |
| `POST /v1/schedule` | Back up every org on this machine each night | write | `berth backup schedule on` |
| `DELETE /v1/schedule` | Stop the nightly backups on this machine | write | `berth backup schedule off` |
| `POST /v1/schedule/run` | Run the nightly backup now, as the schedule would | write | `berth backup schedule run` |
| `POST /v1/image/pull` | Get berth's released image on this machine now; nothing restarts | write | `berth system pull` |
| `POST /v1/image/build` | Build berth's image on this machine from its own files | write | `berth system build` |
| `POST /v1/upgrade` | Install a verified release of berth and move to it, or go back to the one before; nothing restarts | write | `berth system upgrade` |
| `POST /v1/hosts` | Register another machine to run orgs on, over SSH | write | `berth host add` |
| `DELETE /v1/hosts/{host}` | Forget a registered host; refused while it has orgs, unless `force` | write | `berth host rm` |
| `POST /v1/hosts/{host}/rotate-access` | Replace berth's key on a registered host | write | `berth host rotate-access` |
| `GET /v1/hosts/{host}/guard` | The host guard on a registered host: installed or not, its state and rules | read | `berth host guard status` |
| `POST /v1/hosts/{host}/guard/on`, `/guard/off` | Install or remove the host guard | write | `berth host guard on`, `off` |
| `POST /v1/orgs/{org}/rehydrate` | Reinstall in a running org what backups leave out: repos, toolchains, dependencies | write | `berth org rehydrate` |
| `POST /v1/orgs/{org}/run` | Ask Claude something in a running org's /workspace, without a session; the prompt isn't kept in the audit log | write | `berth org run` |
| `POST /v1/orgs/{org}/repos/publish` | Create a GitHub repository for one of an org's local repos, and push it there | write | `berth repo publish` |
| `POST /v1/orgs/{org}/env/migrate` | Move an org's secrets and variables out of org.env into files, after a backup | write | `berth env migrate` |
| `POST /v1/backups/restore` | Restore an org from a backup in this machine's backups directory, named as `GET /v1/backups` shows it (never a path); `force` replaces an org of that name | restart, and `confirm` (the file's name) | `berth backup restore` |
| `POST /v1/orgs/{org}/migrate` | Move an org to a registered host: copied and checked while it runs, then stopped where it is and started there; `no_switch` only copies | restart, and `confirm` | `berth org migrate` |
| `PUT /v1/orgs/{org}/env/{name}` | Set one of an org's variables, a secret included: `value` is stored for the org and never shown again | admin, and `confirm` | `berth env set` |
| `GET /v1/orgs/{org}/env/pending` | The secrets waiting inside a running org to be taken, by name | read | `berth env accept --list` |
| `POST /v1/orgs/{org}/env/accept` | Take the secrets typed inside a running org with `berth-secret-drop`; their values never leave that machine | admin, and `confirm` | `berth env accept` |
| `PUT /v1/orgs/{org}/token` | Give an org its Claude token, as `claude setup-token` prints it | admin, and `confirm` | `berth account token --paste` |
| `POST /v1/orgs/{org}/password/rotate` | Give an org's browser terminal a new password. It is not returned | admin, and `confirm` | `berth org password rotate` |
| `GET /v1/orgs/{org}/password` | The user and password an org's browser terminal asks for; each showing is in the audit log | admin | `berth org password show` |
| `POST /v1/backups/key` | Make this machine's backup key; only its public half is returned | admin | `berth backup keygen` |
| `GET /v1/tokens` | This API's tokens: names, scopes and dates, never the tokens | read | `berth serve token ls` |
| `POST /v1/tokens` | Make a token. It is returned once, here; berth keeps only its hash | admin | `berth serve token add` |
| `DELETE /v1/tokens/{name}` | Remove a token: a running server refuses it from then on | admin | `berth serve token rm` |
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

## A terminal, for signing an org in

Signing an org in shows a link and waits for a code, so it needs a terminal. On the command line that is yours; for
a caller of the API, berth runs the command on a pseudo-terminal of its own, on the machine the org's berth is on.

```bash
curl -N --unix-socket "$s" -X POST -d '{"command":"signin","org":"acme"}' http://berth/v1/terminal
```

| Endpoint | | Needs |
|---|---|---|
| `POST /v1/terminal` | Run a sign-in: `{"command": "signin" \| "login" \| "gh", "org": "acme"}`, and optionally `cols` and `rows`. An event stream: `session` (its id), `output` (what it prints), `exit` (its exit code) | admin |
| `POST /v1/terminal/{id}/input` | Type into it: `{"data": "the code\r"}`. `cols` and `rows` tell it a new size | admin |

- **It runs berth's sign-ins, and nothing else**: `berth account signin`, `account login` and `account gh`, for an org
  named as one. It is not a shell, and there is no way to hand it a command.
- **What it prints is a terminal's.** `output` is text with a terminal's escape sequences in it. A sign-in can show
  the org's new token as it is made, as the command line does: the stream is for the person signing in.
- **It ends with the command, or with the caller**: closing the connection ends the sign-in. A server runs four at
  once, at most.
- **The audit log** has that a sign-in was started, and for which org: never what was typed.
- A read-only server, and a token without the `admin` scope, get none. No MCP server has one: it isn't a tool.

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
  no endpoint (`host create`, `org attach`), says so and doesn't run. `env set` has an endpoint and can't go
  this way all the same: it asks for the value, and there is no terminal on the server to ask in.
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

It has the same tools as `berth mcp`: the API's, without those no MCP server offers ([the list](guides/mcp.md)),
whatever the token. What they may do is the caller's: on TCP the token's scope (`read`, `write`,
`restart`), checked at every request; on the socket, everything `--read-only` allows. `--allow-writes` and
`--allow-restarts` are `berth mcp`'s flags, for stdio, and don't apply here.

## The OpenAPI document

[`api/openapi.json`](api/openapi.json) (OpenAPI 3.1) describes every endpoint, its arguments and its documents; a
running server has it at `/v1/openapi.json`. It is generated from the same table the server's routes are, and a
test keeps the committed copy current, so the document and the handlers can't disagree. Each operation carries
`x-berth-access` (`read`, `write`, `restart` or `admin`) and `x-berth-operation` (the catalog's name for it).

berth's own Go client (`internal/apiclient`) is generated from that table too. It is internal to this repository
for now: the documents' Go types are. From another program, generate a client from the OpenAPI document.
