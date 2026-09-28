# Troubleshooting

## A host is blocked

**Symptom:** a download, API call or package install fails with a connection error inside the org.

1. **Test it.** `berth fw acme test <host>` shows whether the org can reach the host now.
2. **Allow it.** `berth fw acme allow <host>` (or a preset: `berth fw acme presets`). It applies at once.
3. **If it still fails,** the site may use other hosts, such as a CDN or an auth domain, or its addresses may have
   changed. Try `berth fw acme reload`.
4. **Still unsure?** `berth fw acme off`, then retry, tells you whether the firewall is the cause. Turn it back on
   after (`berth fw acme on`).

See [The firewall](guides/firewall.md).

## A repo is refused

**Symptom:** `git clone`/`fetch`/`push` fails with a message about an unregistered repo, or Claude is told the repo
isn't allowed.

- Register it: `berth repo add acme owner/repo`.
- **The URL form doesn't matter:** `git@github.com:Owner/Repo.git`, `https://github.com/owner/repo` and `owner/repo`
  are the same repo.
- **A non-default port, or an ssh-config alias, is refused** ([Repo policy](repo-policy.md)).
- **A folder vanished from `/workspace`:** it wasn't a registered repo and was quarantined. It's in the org's
  `quarantine/` folder. Bring it back with `berth repo adopt acme <dir>`.

## Remote Control

`berth ls` shows each org's Remote Control state, and `berth remote acme status` shows the details:

| State | Meaning | What to do |
|---|---|---|
| `-` | the container is down | `berth up acme` |
| `off` | `REMOTE_CONTROL=0` in `org.env` | set it to `1`, then `berth restart acme` |
| `login-needed` | no full login yet: the token alone can't run Remote Control | `berth login acme` |
| `on` | running | |
| `blocked-by-org` | the Claude organization's policy doesn't allow Remote Control | an admin of that organization must enable it |
| `restarting` | the service is restarting | wait a moment; then `berth remote acme logs` |

`berth remote acme restart` restarts the service, not the container.

## Signed in with the wrong account

`berth whoami` shows each org's accounts. Sign in again in a **private window**:
- `berth logout acme --all` signs Claude out, token included. It doesn't sign out `gh`: that login is in the org's
  `home-config/`.
- Then `berth auth acme` signs in again.

## A restore fails

| Message | Cause |
|---|---|
| `restore failed (wrong passphrase/key, or the backup is corrupted/tampered). Nothing was changed.` | The key or passphrase doesn't match the backup, or the file is damaged. Try the right key: `-i <key>`. |
| `key-encrypted backup: pass --identity <key>` | There's no `backup.key` here: copy yours to `~/.config/berth/`, or pass `-i`. |
| `<org> already exists` | Pick another name (`--as`), or replace it (`--force`: the old one is moved to `backups/.replaced/`). |
| `not a ccenv backup (no manifest)` | The file isn't a berth or ccenv backup (or a very old berth couldn't read what it extracted: upgrade). |

## An org won't start

- **Recent output:** `berth logs acme` shows the container's output.
- **A port taken by something else:** change `SSH_PORT`/`TTYD_PORT` in `org.env`, then `berth up acme`.
- **`… runs on <host> (it holds the lease)`:** the org is active on another host. Start it there, or move it with
  `berth up acme --take-lease`, which stops the other copy first ([Hosts](hosts.md#active-host-leases)).
- **`read-only mode`:** `--read-only` was given. It refuses every change.

## berth itself

- `berth --version` shows the version, and the image a release pulls.
- `berth upgrade --rollback` goes back to the previous version.
- **Where things are:** state in the [state root](concepts.md#the-state-root), and the host registry, keys and leases
  in `~/.config/berth/` ([Configuration](reference/config.md)).
