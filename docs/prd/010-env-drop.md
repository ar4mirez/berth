# PRD: setting a secret without typing it on the host (#10)

Status: **done** · Issue: [#10](https://github.com/ar4mirez/berth/issues/10) · Restarts orgs: yes (image)

## Where it stood

#37 delivered most of the issue: `env set`, `unset` and `ls`, values read hidden or from stdin, secrets as files
that `docker inspect` can't show, and variables that reach every kind of session. One thing was left: **a
remote-safe way to set a value**, for when the secret is on a phone or another machine and not on the host. The
workaround was to write it to a file in the org and have the host operator copy it into `org.env`.

## Decisions (2026-10-07)

- **Drop inside, accept on the host.** The value is typed in the org's own terminal; the host takes it with one
  command. The host still decides what the org gets, and when it restarts. A self-service path with no host step,
  and a timer that accepts by itself, were both turned down: they let whatever runs in the org change its own
  variables.
- **No `--from op://` flag.** `op read … | berth env <org> set KEY` already works through stdin, and is documented.

## Design

### In the image: `berth-secret-drop`

`berth-secret-drop KEY`, run as the org's user, reads the value hidden (or one line of stdin) and writes it to
`~/.config/berth-drop/KEY`, mode 0600 in a 0700 folder. It checks the name and refuses an empty value or one with
a single quote, as `env set` does. `--list`, `--show KEY` and `--cancel KEY` are what the host and the user need.
Only a regular file is a drop: a link is never listed or shown.

`repo-guard.js` adds the drop folder to the paths that are off-limits to Claude.

### On the host: `berth env <org> accept`

- It asks the container for the waiting names (`docker exec … berth-secret-drop --list`) and each value
  (`--show KEY`). **The drop is read inside the container, never as a file of the host's**: the folder is writable
  from the org, and a link planted there must not make the host read a file of its own.
- Nothing from the container is trusted. Each name goes through `env set`'s checks (a variable name, not one berth
  manages). Each value must be non-empty, one line, without control characters or a single quote, and at most
  16 KiB. **If any is refused, none is accepted.**
- Then it is `env set`: the value goes to `org.env` or the org's secrets files, the name to `CCENV_ENV_KEYS`, the
  drop is removed, and a running org is recreated unless `--no-restart`.
- `accept --list` shows the names and which would be refused; `accept KEY…` takes only those.
- Values never appear in berth's output or on a command line.

## Tasks

- [x] T1. `image/berth-secret-drop`, shipped on PATH; the hook keeps Claude out of the drop folder.
- [x] T2. `berth env <org> accept [KEY…] [--list] [--no-restart]`.
- [x] T3. Tests: `TestSecretDrop` (the script), `TestBerthEnvAccept` (the command), contract checks, an image step.
- [x] T4. Docs: `guides/env.md`, `PARITY.md`, the in-container instructions.

## Acceptance criteria

| Criterion (the issue's remaining item) | How it is checked |
|---|---|
| A value can be set without typing it on the host | `TestBerthEnvAccept`: dropped values are stored and listed, and never appear in output or argv |
| It runs in the org's own terminal | `TestSecretDrop`; the image step runs it as the org's user |
| The host picks it up, and stays in control | `TestBerthEnvAccept`: reserved names, multi-line and oversized values are refused, and then nothing is accepted |
| Parity: `org.env` stays as it was for existing keys | `env set`, `unset` and `ls` are unchanged; the parity suite passes with no scenario edited |

## Not done

- **A fully remote path** (no host command at all). It needs something on the host acting for the org: the
  `berthd` agent of #62.
- **`--from op://…`**, by decision.

## Live-org impact

`berth-secret-drop` is in the image, so an org has it after a host upgrade and that org's next restart, which stops
the work running in it. Until then `berth env <org> accept` says the container has no such command.
