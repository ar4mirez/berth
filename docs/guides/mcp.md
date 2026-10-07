# MCP: berth for agents

`berth mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server. With it, an agent such as Claude
Code or Claude Desktop on your machine works with your orgs through typed tools, with typed results, instead of
running `berth` and reading its text.

It runs on your machine, as you, over stdin and stdout: the client starts it and stops it. There is no network
listener.

## Set it up

=== "Claude Code"

    ```bash
    claude mcp add berth -- berth mcp                   # read-only
    claude mcp add berth -- berth mcp --allow-writes    # may also change the firewall, repos, and take backups
    ```

    Add `--scope user` to have it in every project. `claude mcp list` shows it, and `/mcp` inside Claude Code
    shows its tools.

=== "Claude Desktop"

    In Settings → Developer → Edit Config (`claude_desktop_config.json`), with the full path to berth
    (`which berth`), since the app doesn't have your shell's `PATH`:

    ```json
    {
      "mcpServers": {
        "berth": { "command": "/opt/homebrew/bin/berth", "args": ["mcp"] }
      }
    }
    ```

    Restart the app. For another state root, add `"--home", "/path"` before `"mcp"` in `args`.

## What an agent may do

**Read-only by default.** What a tool may do comes from berth's catalog of operations, the same one the command line
uses to refuse writes under `--read-only`.

| Started as | The agent can |
|---|---|
| `berth mcp` | read: hosts, orgs, an org's connection sheet, accounts, Remote Control, logs, the firewall, repos, variable names, packages, backups, the backup schedule |
| `berth mcp --allow-writes` | also change the firewall, add and remove repos, and take backups. Nothing it can call restarts a container |
| `berth mcp --allow-restarts` | also start, restart and stop orgs (and everything above) |

- **A restart needs a confirmation.** `org_up`, `org_restart` and `org_down` stop the work running in an org. Each
  call must repeat the org's name as `confirm`, and the tool's description tells the agent to ask you first. Without
  it the call is refused, and the refusal says what would stop.
- **A refused call says why**, and which flag would allow it, so the agent can tell you instead of looking for
  another way.
- **Secret values are never returned.** Variables are listed by name; the connection sheet has no password; tokens
  show only as present or absent.
- **`berth --read-only mcp`** refuses every write at a second level, whatever the flags; with `--allow-writes` it
  doesn't start.

## The tools

| Tool | What it does | Needs |
|---|---|---|
| `hosts_list` | this machine and every registered host | |
| `orgs_list` | every org: state, ports, token, Remote Control | |
| `org_info` | an org's connection sheet | |
| `org_accounts` | which Claude and GitHub accounts each org uses | |
| `remote_status` | Remote Control in a running org | |
| `org_logs` | the last lines of an org's container log (100 by default, at most 1000) | |
| `firewall_show`, `firewall_presets`, `firewall_test` | the allowlist and live status; the presets; whether the org can reach a host now | |
| `repos_list` | registered repos, their state, and what isn't registered | |
| `env_list` | the names of an org's variables | |
| `packages_list` | the system packages an org's image adds | |
| `backups_list`, `schedule_status` | the backup files here; the nightly schedule | |
| `firewall_allow`, `firewall_deny` | add or remove allowlist entries, applied live | `--allow-writes` |
| `repo_add`, `repo_remove` | register and clone a repo; unregister one (its folder goes to quarantine, not deleted) | `--allow-writes` |
| `backup_create` | an encrypted backup of orgs on this machine | `--allow-writes`, and a backup key (`berth backup keygen`) |
| `org_up`, `org_restart`, `org_down` | start, recreate or stop an org's container | `--allow-restarts`, and `confirm` |

An org is named as on the command line: `acme`, or `acme@box1` for one on a registered host.

Results are the same documents `--output json` prints ([JSON output](../json.md)), so their fields and schemas are
documented there. A tool that changes something also returns what berth printed doing it. A failure is a tool error
with berth's message and its hint.

What isn't there on purpose: setting a variable's value (a secret would pass through the agent), `org create`, `org destroy`,
sign-ins, and anything interactive.

## Resources

| URI | What |
|---|---|
| `berth://orgs` | every org and its status |
| `berth://orgs/{org}` | one org's connection sheet |
| `berth://docs/{page}` | berth's documentation, as in this site: `getting-started`, `guides/firewall`, `troubleshooting`, `PARITY`, … |

## The audit log

Every change asked for through MCP is in `audit.log` in the state root (`~/.local/share/berth/audit.log`), one JSON
object per line: when, which tool, its arguments, and whether it ran, failed or was refused.

```json
{"time":"2026-10-07T15:37:54Z","via":"mcp","tool":"firewall_allow","args":{"entries":["pypi.org"],"org":"acme"},"outcome":"ok"}
{"time":"2026-10-07T15:38:10Z","via":"mcp","tool":"org_restart","args":{"org":"acme"},"outcome":"refused","error":"org_restart restarts or stops a container …"}
```

Arguments are names and settings. No tool takes a secret value, so none is logged.

## Not yet

Access over the network (MCP's streamable HTTP transport) needs authentication, which comes with the API (#62).
Until then `berth mcp` is for a client on the same machine.
