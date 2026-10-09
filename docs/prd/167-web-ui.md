# PRD: a web UI in `berth serve`, and every action in both dashboards (#167)

Status: **phase 1 in progress** · Issue: [#167](https://github.com/ar4mirez/berth/issues/167) · Restarts orgs: no

## Problem

berth has a terminal dashboard (`berth tui`) and an HTTP API (`berth serve`), and nothing for a browser: on a
phone or a tablet the only ways to manage orgs are an SSH client or the API by hand. The dashboard that exists
does a part of what berth can: it starts, stops and restarts orgs, changes the firewall and repos, and takes a
backup. Creating an org, signing it in, its variables, packages, hosts and restores are still commands.

Two things are wanted:

1. **A web UI embedded in the binary**, served by `berth serve`, mobile-first.
2. **Both dashboards do everything berth does**, and the same things as each other.

## Decisions

2026-10-09, agreed with the operator.

- **`berth serve` serves it**, at `/`, from files embedded with `go:embed`. There is no second server, and no
  second set of rules: the UI is a client of `/v1`, so `--read-only`, token scopes, `confirm` and the audit log
  apply to it as to any caller.
- **No build step.** Hand-written ES modules, one stylesheet, and **Alpine.js** for state, vendored and pinned
  (`tools/pinbump` already checks npm integrity). Alpine's CSP build, so the page needs no `unsafe-eval`.
- **Not HTMX.** HTMX wants HTML fragments from the server: a second, HTML-rendering surface beside the JSON API,
  with its own routes to keep in step with the catalog. The API returns JSON documents already, and Alpine
  renders those.
- **A browser signs in with an API token**, kept in `sessionStorage` and sent as `Authorization: Bearer`. No
  cookie, so no CSRF. The static files are served without a token; they hold no data.
- **Streams are read with `fetch`**, not `EventSource`, which can't send a header.
- **`berth ui`** is the local way in: a server of its own on `127.0.0.1`, with a token made for that run and
  kept nowhere, and the browser opened at the UI with the token in the URL fragment (never sent to a server,
  removed from the address bar at load). It is plain HTTP, the one listener without TLS: nothing leaves the
  machine, and a certificate a browser warns about would teach the wrong habit. The browser is handed a 0600
  file that sends it on, not the link, because a command's arguments are visible to every user.
- **Mobile-first**: one column at 360px, the org list as cards, an org's tabs as a scrolling bar, touch targets
  of 44px, and light and dark from the system. Wider screens get the list beside the org's page.
- **Parity is kept by a test, not by care.** Every operation in `ops.Catalog` is either served (a tool and a
  route) or listed with the reason it isn't (`TestCatalogCoversEveryCommand`'s sibling). Each dashboard has the
  same test against its own actions.
- **A fourth token scope, `admin`**, above `restart`: what can't be undone (`org destroy`, `host destroy`,
  `restore --force`), anything that takes a secret, the terminal, and token management. Without it a `write`
  token could make itself a `restart` one.
- **Secrets can be typed into the UI.** Until now the API had no endpoint that sets a secret, on purpose. "Every
  action" needs `env set`, a pasted Claude token, and a backup passphrase: they are accepted, write-only, with
  the `admin` scope, never in the audit log's arguments and never returned.
- **A terminal in the browser, for sign-in only** (see below). attach, shell and claude stay a link to the
  org's own browser terminal, which exists for that and has its own password.
- **A tool may be marked as not for MCP**, because the API serves `internal/mcpsrv`'s tools and new ones would
  reach agents too. `org destroy`, host management and token management are.

## What "every action" means

| Kind | Operations | In the API today | Needs |
|---|---|---|---|
| Reads, and the dashboard's actions | lists, info, logs, firewall allow/deny/test, repo add/rm, backup, up/restart/down | yes | screens |
| Plain arguments | org create and destroy; firewall on/off/reload; repo new/adopt/sync/publish/policy/audit; packages; `env unset`; `remote restart`; `password rotate`; logout; schedule; restore, rehydrate, migrate; host add/rm/guard/rotate-access/reconcile/create/destroy; `use`; pull, build, upgrade; `secrets migrate` | no (some through `/v1/cli`) | a tool and a route each, then screens |
| Take or show a secret | `env set`, `account token --paste`, `password show`, `backup keygen` and a passphrase, `serve token add`, a cloud provider's key for `host create` | no | phase 3 |
| Print a link and wait | `account gh` (a device code) | no | an event stream; no terminal |
| Need a terminal | `account signin` and `login` (a code pasted back), attach, shell, claude, `fw edit` | no | phase 4 |
| Not for a dashboard | `tui`, `serve`, `mcp`, `completion`, `parity-check`, `connect`, `install`, `service`, takeover and handback | | the reason, in the parity table |

`fw edit` becomes an editor for the allowlist in the page (allow and deny, as a diff), not `$EDITOR`.

## Design

### Serving

- `internal/web`: `//go:embed all:static`, and a handler for `/` that serves it with a strict
  `Content-Security-Policy` (`default-src 'none'`, scripts, styles and requests from `'self'`),
  `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, and an `ETag` with `Cache-Control: no-cache`, so a
  page from one version never runs against another's API. Nothing is fetched from another origin.
- `api.Handler` serves it for a `GET` that isn't under `/v1` or `/mcp`. `--no-ui` leaves it out.
- **`Host` is not checked.** The draft had it, against DNS rebinding. There is nothing for a rebound page to
  spend: no cookie, and the token is in the session storage of berth's own origin, which another name doesn't
  share. A check would only have added a flag for every tailnet name and proxy.
- **The certificate.** berth's own is self-signed, and a browser warns. The guide says to compare the
  fingerprint, or to give berth a certificate from `tailscale cert` with `--tls-cert`.
- `GET /v1` already says what the caller may do: the UI hides what its token can't, and the server still refuses.

### Screens

The same shape as the terminal dashboard, so one guide describes both:

- **Orgs**: every org on every host, with state, Remote Control and the links in (`claude.ai/code`, the browser
  terminal, `ssh://`). Create an org.
- **An org**: Info · Firewall · Repos · Env · Packages · Backups · Logs · Account. Each tab has its operations.
- **Hosts**: the registry, each host's engine and guard. Add, remove, create at a provider.
- **System**: backups and the schedule, restore, berth's version and upgrade, API tokens.
- **Every operation, by name**: a sheet made from the OpenAPI document (arguments, access, what restarts), for
  whatever has no screen of its own yet. It is what makes "every action" true on the day a tool is added.

Confirmation follows the catalog, as in the terminal: an operation that restarts or removes shows what stops,
and is run only after the org's name is typed (the API's `confirm`). Long operations show their events as they
arrive.

### The terminal, for sign-in

`POST /v1/terminal` starts one of a fixed list of commands (`account signin`, `account login`) as `berth` itself
with a pseudo-terminal, as the terminal dashboard's handoff does. Output is an event stream; keys are posted
back. xterm.js, vendored, draws it. It needs `admin`, ends with the command, and is in the audit log.

This adds a pty dependency on the host side, which `CLAUDE.md` says not to reimplement: it allocates one and
runs berth in it, nothing more. Say so there.

### The terminal dashboard

`internal/tui` gets the same operations: forms for the ones with arguments (it has one prompt today), a secret
prompt that doesn't echo, and its handoff for the ones that need the terminal. Its `Backend.Run` switch becomes
a call into the same tools the API serves, so an operation added once is in both.

## Phases

Each is a PR that ships on its own.

1. **The UI, with what the dashboard does today.** `internal/web`, the token sign-in, `berth ui`, the orgs list,
   an org's tabs, hosts; the existing actions. Docs: `guides/web.md`. **Done**, but for `repo sync`, which has
   no endpoint: it comes with phase 2's tools. The web UI has two things the terminal one lacks until then: a
   packages tab and a firewall test, both endpoints that were there.
2. **Plain-argument operations**, in the API, the web UI and the terminal dashboard; the `admin` scope; the
   parity tests; the by-name sheet.
3. **Secrets.**
4. **Sign-in**: `account gh` as events, then the terminal.

## Acceptance criteria

| Criterion | How it is checked | |
|---|---|---|
| The UI is in the binary, and loads nothing from elsewhere | `internal/web`: `TestLoadsNothingFromElsewhere` (every `src`, `href` and `url()` is one of its own files; nothing inline), `TestPolicy`, `TestVendored` (the vendored script's hash) | phase 1 |
| No data without a token on TCP | `TestBerthUI`, `TestBerthServeWebUI`: the real binary; the files answer, every `/v1` path is 401, and nothing ran | phase 1 |
| It can't do more than its token | `test/web/smoke.mjs`: with a caller that may only read, no control that changes anything is shown; `TestBerthUI`: a write on a read-only server is 403 | phase 1 |
| Nothing restarts without the org's name typed | `test/web/smoke.mjs`: without the name, and with part of it, nothing is sent; with it, `confirm` is the name. `TestBerthUI`: without `confirm` the API refuses | phase 1 |
| Usable on a phone | **not checked by a test yet**: it needs a real browser (Playwright at 360×640: no horizontal scroll, every control reachable) | open |
| The policy lets the page run | **not checked by a test yet**: the same real browser. The page was run in a DOM, which enforces no policy | open |
| Both dashboards do everything | the parity tests: catalog against tools, tools against each dashboard's actions | phase 2 |
| A secret typed in is not kept anywhere but where it belongs | a test sets one and searches the audit log, the response and the server's log for it | phase 3 |

## Not in this

- Accounts for several people: a token is the identity, as for the API.
- Metrics, charts, or history. The UI shows what berth knows now.
- Windows.
