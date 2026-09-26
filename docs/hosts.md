# Hosts

berth keeps a registry of the machines it manages (#44). This machine is always there, as `local`. Other hosts are
added with `berth host add` and reached over SSH: files through SFTP, commands through SSH sessions, and Docker
through its socket (docs/plan.md, decision 2). A host needs only sshd, a POSIX shell, Docker and compose.

An org on a host is addressed as `org@host` (#45). A bare org name stays on this machine, exactly as before.

## org@host

```bash
berth init acme@box1 --name "Ada" --email ada@example.com   # the org lives in box1's state root
berth up acme@box1
berth fw acme@box1 allow pypi.org
berth repo add acme@box1 acme/widgets
berth attach acme@box1                        # interactive commands go through your ssh binary (ssh -t)
berth ls                                      # this machine's orgs, then each host's, with a HOST column
```

- **Where things run.** Everything the org needs runs on its host: its files (`org.env`, `firewall.txt`,
  `repos.txt`, secrets), `docker compose`, the image (pulled, or built there), and the state lock.
- **What stays on this machine.** Your own files are read here, whichever host the org is on: the SSH public keys `init`
  authorizes into the org, and berth's host registry and keys. Prompts and pasted values come from your terminal.
- **Addressing forms.**
  - `acme@local` is the same as `acme`.
  - An unknown host is refused before anything connects.
  - `whoami` takes several orgs, all on one host.
- **What `ls` shows.**
  - `ls` lists every host's orgs and never hides one it can't reach: it says so on stderr, and
    `--output json` lists it under `unreachable`.
  - With no hosts registered, its output is exactly as before.
- **Completion.** After an `@`, completion offers the registered hosts. It reads the registry and never connects.
- **Not remote yet.** `backup`, `restore`, `migrate`, `secrets migrate` and `parity-check` refuse an
  `org@host`, because they read your backup key or write your files. Host-side backups are #49, and moving orgs
  between hosts is #50. `build`, `pull` and `schedule` act on this machine.
- **`fw edit` on a host.** It opens the editor on that host, over `ssh -t`.

## Commands

```bash
berth host add box1 ops@box1.example           # [user@]host[:port]; the user defaults to $USER, the port to 22
berth host ls                                  # every host: reachable, Docker version, number of orgs
berth --output json host ls                    # the same, as berth.hosts/v1 (docs/json.md)
berth host rotate-access box1                  # replace berth's key on box1
berth host rm box1                             # forget box1 (refused while it has orgs; --force overrides)
```

None of these start, stop or restart anything on any host.

### `host add`

`host add <name> <[user@]host[:port]>` does the following, in order. If any step fails, it undoes the earlier ones:
the host ends up registered completely or not at all.

1. **Connects with your own SSH access:** your ssh-agent, or a key file given with `--identity <file>` (repeatable;
   a key with a passphrase must be in the agent).
2. **Pins the host's key** in berth's own `known_hosts`. berth never reads or writes `~/.ssh/known_hosts`.
   - On a terminal, it shows the key's fingerprint and asks you to confirm it.
   - Without a terminal (a script, an agent), it prints the fingerprint and stops. Run it again with
     `--fingerprint SHA256:…` once you've checked it.
   - To check a fingerprint, run `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the host (or the key type
     berth names).
   - `--accept-new-host-key` trusts whatever key the host presents. Use it only on a network you trust.
   - A host whose key has changed is always refused, whatever the flags.
3. **Checks that the host can run orgs:**
   - `docker version` works for that user (which usually means the user is in the `docker` group);
   - `docker compose` is installed.

   It also reports the UID and GID, the architecture, the Tailscale IP and the number of ports in use.
4. **Gives berth its own key for the host.** It makes an ed25519 key at `~/.config/berth/keys/<name>` (0600),
   adds its public half to the host's `~/.ssh/authorized_keys` (the line ends `berth:<name>`), and checks that the
   key logs in. From then on, berth uses only that key for the host, not your agent. The host's access can then be
   rotated or revoked on its own.
5. **Records the host** in `~/.config/berth/hosts.yaml`. `--home <dir>` sets the state root on the host; the
   default is `~/.local/share/berth` there.

### `host ls`

`host ls` lists `local` first, then each registered host. For each one it shows whether berth can log in, the
Docker engine's version, and how many orgs are under its state root (directories under `orgs/` holding an
`org.env`). When a host fails, the reason goes to stderr: unreachable, a changed host key, Docker not answering.

### `host rotate-access`

`host rotate-access <name>` replaces berth's key on the host, without ever losing access:
1. it makes a new key and adds it to `authorized_keys`;
2. it checks that the new key logs in;
3. only then does it switch to the new key locally, and remove the old key over a connection made with the new one.

If the new key doesn't work, the old one stays and nothing changes. If removing the old key fails, berth says so and
names its fingerprint, so you can remove that line by hand.

### `host rm`

`host rm <name>` refuses while the host has orgs, or while berth can't reach it to check. `--force` overrides both.
It then:
- removes berth's line from the host's `authorized_keys`, when it can reach the host;
- removes the host from `hosts.yaml` and from berth's `known_hosts`;
- deletes berth's key for the host.

It never stops or deletes anything on the host: its orgs keep running there. If the host was unreachable, berth
reminds you to remove the `berth:<name>` line by hand.

## Files

| File (on this machine) | What |
|---|---|
| `~/.config/berth/hosts.yaml` | the registry (0600). Unknown keys are an error, so a typo can't drop a host. |
| `~/.config/berth/keys/<name>`, `<name>.pub` | berth's key for each host (0600, unencrypted, like a default `ssh-keygen` key) |
| `~/.config/berth/known_hosts` | the hosts' pinned keys |

`$XDG_CONFIG_HOME` replaces `~/.config` when it's set.

## Security

- **What a host receives:** only berth's public key. Your `backup.key` never goes to a host; hosts only ever get
  age recipients (docs/plan.md, decision 4).
- **Access to a host:** anyone who can read `~/.config/berth/keys/` on this machine can log in to your hosts as
  their users, just as with `~/.ssh`. `host rotate-access` replaces a key; `host rm` revokes it.
- **What berth trusts:** a host key only after you confirm its fingerprint (or pass `--accept-new-host-key`), and
  after that only that key.
