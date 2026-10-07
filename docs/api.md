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

## Who may call it

The API listens on a **Unix socket only you can open** (mode 0600, created that way). There is no token on the
socket: whoever can open it is you, and could run `berth` anyway. A socket left behind by a server that is gone is
replaced; a file that isn't a socket, or a socket that still answers, is not.

Nothing listens on the network. TCP, with tokens over TLS, is a separate step (#62).

## The rules

Each endpoint is one operation from berth's catalog, and the catalog decides what a caller needs:

| The operation | |
|---|---|
| reads | always answers |
| writes | refused with `--read-only` (403) |
| restarts a container | also needs the org's name again as `confirm` in the body, or it is refused (403) and says what would stop |

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
| 403 | `refused` | read-only, or a restart without `confirm` |
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
operation's progress as it goes: the [`berth.event/v1`](json.md#progress-berthevent-v1) events (`start`, `step`,
`output`, then `done` or `failed`), and after `done` a `result` event with the document.

```bash
curl -N --unix-socket "$s" -H 'Accept: text/event-stream' -X POST -d '{"confirm":"acme"}' http://berth/v1/orgs/acme/up
```

`GET /v1/orgs/{org}/logs/follow` is always a stream: one `output` event per log line, until you disconnect.

## The OpenAPI document

[`api/openapi.json`](api/openapi.json) (OpenAPI 3.1) describes every endpoint, its arguments and its documents; a
running server has it at `/v1/openapi.json`. It is generated from the same table the server's routes are, and a
test keeps the committed copy current, so the document and the handlers can't disagree. Each operation carries
`x-berth-access` (`read`, `write` or `restart`) and `x-berth-operation` (the catalog's name for it).

berth's own Go client (`internal/apiclient`) is generated from that table too. It is internal to this repository
for now: the documents' Go types are. From another program, generate a client from the OpenAPI document.
