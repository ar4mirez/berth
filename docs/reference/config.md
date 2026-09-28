# Configuration and files

## On this machine

| Path | What |
|---|---|
| `~/.config/berth/config.yaml` | berth's config: `home:` is the state root (below). Unknown keys are an error. |
| `~/.config/berth/backup.key` | the age key backups encrypt to (`berth keygen`). Keep a copy elsewhere: it's the only way back. |
| `~/.config/berth/hosts.yaml` | the registered hosts (`berth host`) |
| `~/.config/berth/keys/<host>` | berth's own SSH key for each host (0600) |
| `~/.config/berth/known_hosts` | the hosts' pinned SSH keys (berth never uses `~/.ssh/known_hosts`) |
| `~/.config/berth/leases.yaml` | each org's active host, once hosts are registered |
| `~/.config/berth/context` | the default org (`berth use`) |
| `~/.local/opt/berth/<version>/berth` | installed versions (`berth install`, `berth upgrade`); `~/.local/bin/berth` links to the current one |

`$XDG_CONFIG_HOME` replaces `~/.config`, and `$XDG_DATA_HOME` replaces `~/.local/share`. A machine that ran ccenv keeps
using `~/.config/ccenv/backup.key` until berth has its own.

### config.yaml

```yaml
home: ~/Work/claude-envs     # the state root; absolute, or starting with ~/
engine: podman               # this machine's container engine: docker (the default) or podman
bind: iface:wg0              # BIND_ADDR for new orgs here (docs/networking.md); unset: tailscale if installed, else 127.0.0.1
```

## The state root

It's resolved from `--home`, then `$BERTH_HOME`, then `home:` in `config.yaml`, then `~/.local/share/berth`.

| Path | What |
|---|---|
| `orgs/<org>/` | everything an org has ([Concepts](../concepts.md#orgs)) |
| `backups/` | backups (0700), unless `-o` or `BERTH_BACKUP_DIR` says otherwise; `backups/.replaced/` holds orgs a `restore --force` replaced |
| `berth/` | berth's generated files (its compose file, the image's build context), the state lock, lease markers. Git-ignored. |

## Environment variables

| Variable | Effect |
|---|---|
| `BERTH_HOME` | the state root (after `--home`) |
| `BERTH_BIND` | BIND_ADDR for new orgs on this machine (before `bind:` in `config.yaml`) |
| `BERTH_ENGINE` | this machine's container engine, `docker` or `podman` (before `engine:` in `config.yaml`) |
| `BERTH_BACKUP_DIR` | where backups go by default |
| `BERTH_BACKUP_KEY` | the age identity for backups and restores (default `~/.config/berth/backup.key`) |
| `BERTH_BACKUP_RECIPIENTS` | public keys backups encrypt to (`age1…` or `ssh-…`, separated by spaces or commas) |
| `BERTH_BACKUP_PASSPHRASE` | a passphrase for passphrase-encrypted backups (no prompt) |
| `BERTH_PUBLISHED_IMAGE` | the image a release pulls instead of building; `none` always builds |
| `BERTH_IMAGE_DIR` | build the image from this directory instead of berth's own copy |
| `CLAUDE_CODE_VERSION` | the Claude Code version `berth build` installs (default: the image's pin) |
| `CCENV_*` | ccenv's names for the backup variables above, still accepted |
| `EDITOR` | the editor `berth fw <org> edit` opens (default `vi`) |
| `SSH_AUTH_SOCK` | your ssh-agent, used by `berth host add` to reach a new host |
| `DOCKER_HOST` | a `unix://` Docker socket other than `/var/run/docker.sock` |

## org.env

Each org's settings, in `orgs/<org>/org.env` (0600). `berth init` writes it with comments. Edit it, then
`berth restart <org>` to apply.

| Key | Meaning |
|---|---|
| `MANAGER` | `berth` for berth's orgs (ccenv refuses them); no line means ccenv's |
| `CLAUDE_CODE_OAUTH_TOKEN` | the org's 1-year Claude token (`berth token`) |
| `ANTHROPIC_API_KEY` | instead of the token: API (Console) billing |
| `GIT_USER_NAME`, `GIT_USER_EMAIL` | the org's git identity |
| `GH_TOKEN` | optional: `gh` and HTTPS git for this org |
| `BIND_ADDR` | where SSH and the browser terminal listen: `tailscale`, `iface:<name>`, `ip:<addr>` or an address, `localhost` (with `berth connect`), `127.0.0.1`, or `0.0.0.0` (berth warns) ([Networking](../networking.md)) |
| `SSH_PORT`, `TTYD_PORT` | this org's SSH and browser-terminal ports on the host |
| `REMOTE_CONTROL` | `1` runs the Remote Control service; `0` turns it off |
| `REMOTE_CAPACITY` | how many Remote Control sessions may run at once (default 8) |
| `REPO_POLICY` | `enforce` (default), `warn` or `off` ([Repos](../guides/repos.md#the-policy)) |
| `MEM_LIMIT`, `CPUS` | the container's memory and CPU limits (default `8g`, `4`) |
| `SHARED_ACCOUNT` | `1` silences the warning when two orgs sign in to the same Claude organization |
| `CCENV_ENV_KEYS` | the names of the org's custom variables (`berth env`) |

`berth env` refuses to set any of these; custom variables go in with `berth env <org> set`.

## Output formats

`--output json` gives structured data from the commands that return any: [JSON output](../json.md).
