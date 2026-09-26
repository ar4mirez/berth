# JSON output

Commands that return data accept `--output json`. It's a global flag, so it goes before the command, as `--home` and
`--read-only` do:

```bash
berth --output json ls
berth --output json env acme ls
berth --output json fw acme show
```

Asking for JSON from a command that doesn't return data yet fails right away, with
`--output json isn't available for "<command>" yet`, and changes nothing. `internal/ops` lists which commands return
data (`JSON: true` in the catalog). The rest follow in #54.

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
      "remote": "on"
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `manager` | `berth` or `ccenv` (an org with no `MANAGER` line is ccenv's) |
| `state` | `up` or `down` |
| `ssh_port`, `ttyd_port` | numbers, or `null` when `org.env` has no valid value |
| `token` | whether a Claude token or API key is set |
| `remote` | Remote Control: `-` (down), `off`, `login-needed`, `on`, `blocked-by-org` or `restarting` |

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
  and `null` when the org is down.
- An empty allowlist is `"entries": []` with exit 0. The text output exits 1 in that case, as ccenv does.

## `berth.hosts/v1`: `host ls`

```json
{
  "schema": "berth.hosts/v1",
  "hosts": [
    { "name": "local", "kind": "local", "address": "", "home": "/home/op/.local/share/berth",
      "reachable": true, "docker": "27.3.1", "orgs": 3, "error": "" },
    { "name": "box1", "kind": "ssh", "address": "ops@box1.example:22", "home": "/home/ops/.local/share/berth",
      "reachable": false, "docker": "", "orgs": null, "error": "ssh ops@box1.example:22: dial tcp …: i/o timeout" }
  ]
}
```

- `local`, this machine, is always first.
- `orgs` is `null` when it couldn't be counted. `error` says what failed, and is `""` otherwise.
- A host that can't be reached is still listed, and the exit code is 0.
