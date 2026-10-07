# Troubleshooting

## A host is blocked

**Symptom:** a download, API call or package install fails with a connection error inside the org.

1. **Test it.** `berth fw test acme <host>` shows whether the org can reach the host now.
2. **Allow it.** `berth fw allow acme <host>` (or a preset: `berth fw presets acme`). It applies at once.
3. **If it still fails,** the site may use other hosts, such as a CDN or an auth domain, or its addresses may have
   changed. Try `berth fw reload acme`.
4. **Still unsure?** `berth fw off acme`, then retry, tells you whether the firewall is the cause. Turn it back on
   after (`berth fw on acme`).

See [The firewall](guides/firewall.md).

## Nothing resolves

**Symptom:** every name fails inside the org (`Could not resolve host`, `EAI_AGAIN`), Remote Control shows
`restarting`, and `berth fw show acme` shows `live: on 12 (DNS not answering)`.

The org's lookups go to the resolver Docker gave its container, and to no other.

1. **See what berth says.** `berth fw reload acme` prints `firewall: WARNING DNS isn't answering …` with the
   resolvers it tried, and ends with exit code 3.
2. **Check that resolver from the host.** If it is down, or a VPN that provided it is disconnected, DNS comes back
   when it does, at the next rebuild (within five minutes, or `berth fw reload acme`). The addresses that were
   allowed stay allowed meanwhile.
3. **An org still on the image of berth 0.3.0 to 0.4.1** on a host whose Docker forwards to a resolver that isn't loopback
   (a `dns` setting in `daemon.json`, a LAN or VPN resolver) has no DNS at all. Restart it onto the current image
   (`berth restart acme`), or allow the resolver until you can: `berth fw allow acme <resolver>/32`.

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

`berth ls` shows each org's Remote Control state, and `berth account remote status acme` shows the details:

| State | Meaning | What to do |
|---|---|---|
| `-` | the container is down | `berth up acme` |
| `off` | `REMOTE_CONTROL=0` in `org.env` | set it to `1`, then `berth restart acme` |
| `login-needed` | no full login yet: the token alone can't run Remote Control | `berth account login acme` |
| `on` | running | |
| `blocked-by-org` | the Claude organization's policy doesn't allow Remote Control | an admin of that organization must enable it; then `berth account remote restart acme` |
| `restarting` | the service is restarting | wait a moment; then `berth account remote logs acme` |

`berth account remote restart acme` restarts the service, not the container, and waits until it has started again. While
the organization's policy blocks Remote Control, the service tries again once an hour; a restart (or
`berth account login acme`) makes it try now, so there is no hour to wait after an admin enables it.

If the restart says `Remote Control didn't start again`, `berth account remote logs acme` shows why. A container started
by berth 0.4.1 or older can't be asked to retry: `berth restart acme` recreates it, which stops the work running in
it.

## Signed in with the wrong account

`berth account whoami` shows each org's accounts. Sign in again in a **private window**:
- `berth account logout acme --all` signs Claude out, token included. It doesn't sign out `gh`: that login is in the org's
  `home-config/`.
- Then `berth account signin acme` signs in again.

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
- `berth system upgrade --rollback` goes back to the previous version.
- **Where things are:** state in the [state root](concepts.md#the-state-root), and the host registry, keys and leases
  in `~/.config/berth/` ([Configuration](reference/config.md)).

## `berth system upgrade` says it isn't available

**Symptom:** `berth: upgrade isn't available in this build`.

berth 0.3.0 to 0.4.1 can't upgrade themselves: the command was never connected to the release client. Run the
install script once, which verifies the same signature and checksum; `berth system upgrade` works from then on.

```bash
curl -fsSLO https://github.com/ar4mirez/berth/releases/latest/download/install.sh
less install.sh
sh install.sh
```

An install from Homebrew, mise or a system package is upgraded there, and `berth system upgrade` says so.

