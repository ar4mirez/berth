# JSON output

Commands that return data accept `--output json`. It's a global flag, so it goes before the command, as `--home` and
`--read-only` do:

```bash
berth --output json ls
berth --output json info acme
berth --output json whoami
berth --output json remote acme status
berth --output json env acme ls
berth --output json fw acme show
berth --output json host ls
berth --output json use
```

Asking for JSON from a command that doesn't return data fails right away, with
`--output json isn't available for "<command>" yet`, and changes nothing. `internal/ops` lists which commands return
data (`JSON: true` in the catalog). The remaining read commands follow in #54.

Each document below has a golden file in `test/parity/testdata/json/`, which the tests compare byte for byte.

## Rules
- **Every document names its schema and version:** `"schema": "berth.<kind>/v<N>"`.
  - Adding a field doesn't change the version.
  - Removing or renaming one, or changing its meaning, bumps it.
- **Secrets are never included.** Tokens show only as present or absent, and custom variables only by name.
- **Nothing extra on stdout.** stdout holds only the document, and diagnostics go to stderr, so
  `berth --output json … | jq` works.
- **The text output is unchanged.** It's the same data rendered as ccenv did, and the parity suite checks it.

## `berth.orgs/v1`: `ls`

```json
{
  "schema": "berth.orgs/v1",
  "orgs": [
    {
      "name": "acme",
      "manager": "berth",
      "state": "up",
      "ssh_port": 2201,
      "ttyd_port": 7701,
      "token": true,
      "remote": "on",
      "host": "local"
    }
  ],
  "unreachable": [],
  "destroyed": [ { "name": "initech", "host": "local", "at": "2026-09-30T20:00:00Z" } ]
}
```

| Field | Meaning |
|---|---|
| `manager` | `berth` or `ccenv` (an org with no `MANAGER` line is ccenv's) |
| `state` | `up` or `down` |
| `ssh_port`, `ttyd_port` | numbers, or `null` when `org.env` has no valid value |
| `token` | whether a Claude token or API key is set |
| `remote` | Remote Control: `-` (down), `off`, `login-needed`, `on`, `blocked-by-org` or `restarting` |
| `host` | where the org is: `local`, or a registered host's name (docs/hosts.md). This machine's orgs come first. |
| `unreachable` | the registered hosts whose orgs couldn't be listed: `{"host": "box1", "error": "…"}`. Always present, `[]` when none. |
| `destroyed` | orgs `destroy` removed (offboarded), where, and when (UTC), that don't exist again since. Always present, `[]` when none. |

## `berth.env/v1`: `env <org> ls`

```json
{ "schema": "berth.env/v1", "org": "acme", "vars": [ { "name": "OPENROUTER_API_KEY", "present": true } ] }
```

`present` is `false` when `CCENV_ENV_KEYS` lists the variable but `org.env` has no line for it. The text output
shows the same case as `(listed but missing)`.

## `berth.firewall/v1`: `fw <org> show`

```json
{
  "schema": "berth.firewall/v1",
  "org": "acme",
  "file": "/path/to/orgs/acme/config/firewall.txt",
  "entries": ["mode on", "@python", "pypi.org"],
  "live": "on 159"
}
```

- `entries` are the file's lines that aren't blank or comments, as written.
- `live` is the running container's `/run/firewall.status` (`on <N>` or `off`). It's `unknown` if it can't be read,
  and `null` when the org is down. A note in parentheses follows when the list applied with a problem:
  `on 187 (1 skipped)`, `on 187 (no DNS allowlisting)`.
- An empty allowlist is `"entries": []` with exit 0. The text output exits 1 in that case, as ccenv does.

## `berth.hosts/v1`: `host ls`

```json
{
  "schema": "berth.hosts/v1",
  "hosts": [
    { "name": "local", "kind": "local", "address": "", "home": "/home/op/.local/share/berth",
      "reachable": true, "engine": "docker", "docker": "27.3.1", "orgs": 3, "error": "" },
    { "name": "box1", "kind": "ssh", "address": "ops@box1.example:22", "home": "/home/ops/.local/share/berth",
      "reachable": false, "docker": "", "orgs": null, "error": "ssh ops@box1.example:22: dial tcp …: i/o timeout" }
  ]
}
```

- `local`, this machine, is always first.
- `engine` is the host's container engine, `docker` or `podman`; `docker` holds that engine's version (#57).
- `orgs` is `null` when it couldn't be counted. `error` says what failed, and is `""` otherwise.
- A host that can't be reached is still listed, and the exit code is 0.

## `berth.info/v1`: `info <org>`

```json
{
  "schema": "berth.info/v1",
  "org": "acme",
  "container": "claude-acme",
  "state": "running",
  "remote_url": "https://claude.ai/code?environment=env_0123",
  "address": "100.64.0.7",
  "local_only": false,
  "ssh_port": 2201,
  "ttyd_port": 7701,
  "user": "node",
  "ssh_host": "claude-acme",
  "git_public_key": "ssh-ed25519 AAAA… claude-acme@box",
  "tunnel": null
}
```

| Field | Meaning |
|---|---|
| `state` | `running` or `stopped` |
| `remote_url` | the org's claude.ai/code environment; `""` until Remote Control is set up, or when the org is stopped |
| `address` | where other devices reach the org's ports; `""` if its bind can't be resolved (the reason is on stderr) |
| `local_only` | `true` when the ports are bound to this machine only (`127.0.0.1`) |
| `ssh_port`, `ttyd_port` | numbers, or `null` when `org.env` has no valid value |
| `user`, `ssh_host` | the account SSH uses, and the `Host` alias berth suggests for `~/.ssh/config` |
| `git_public_key` | the org's git key; `""` if it can't be read (the reason is on stderr) |
| `tunnel` | `null`, or `{"connect": "acme@box1", "ssh_target": "ops@box1.example"}` for an org reached through an SSH tunnel: the argument for `berth connect`, and where to tunnel by hand (`""` when unknown) |

The browser terminal's password is never included: `berth password acme` prints it.

## `berth.whoami/v1`: `whoami [org...]`

```json
{
  "schema": "berth.whoami/v1",
  "orgs": [
    {
      "org": "acme",
      "running": true,
      "token": true,
      "github": "octo-acme",
      "claude": { "email": "dev@example.com", "organization": "Acme Corp", "organization_id": "org-acme" }
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `token` | whether a Claude token is set |
| `github` | the gh CLI's login in the container; `""` when it isn't signed in, or the org is down |
| `claude` | the Remote Control login; `null` when not logged in, or the org is down |

## `berth.remote/v1`: `remote <org> status`

```json
{ "schema": "berth.remote/v1", "org": "acme", "state": "on", "url": "https://claude.ai/code?environment=env_0123", "capacity": "Capacity: 1/8 sessions" }
```

| Field | Meaning |
|---|---|
| `state` | `login-needed`, `on`, `blocked-by-org` or `restarting` |
| `url`, `capacity` | when `on`: the environment's link and the service's last capacity line; `""` otherwise, or not logged yet |

The org must be running: otherwise the command fails as the text one does. Where the text output ends with exit 1
because the service hasn't logged a capacity line yet, the JSON has `"capacity": ""` and exit 0.

## `berth.default-org/v1`: `use`

```json
{ "schema": "berth.default-org/v1", "org": "acme@box1" }
```

`org` is `null` when no default org is set.

## `berth.image/v1`: `image-tag`

```json
{ "schema": "berth.image/v1", "tag": "berth/claude-env:0123456789ab" }
```
