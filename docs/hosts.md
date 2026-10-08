# Hosts

berth keeps a registry of the machines it manages (#44). This machine is always there, as `local`. Other hosts are
added with `berth host add` and reached over SSH: files through SFTP, commands through SSH sessions, and Docker
through its socket (docs/plan.md, decision 2). A host needs only sshd, a POSIX shell, Docker and compose.

An org on a host is addressed as `org@host` (#45). A bare org name stays on this machine, exactly as before.

## org@host

```bash
berth org create acme@box1 --name "Ada" --email ada@example.com   # the org lives in box1's state root
berth up acme@box1
berth fw allow acme@box1 pypi.org
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
  - `account whoami` takes several orgs, all on one host.
- **What `ls` shows.**
  - `ls` lists every host's orgs and never hides one it can't reach: it says so on stderr, and
    `--output json` lists it under `unreachable`.
  - With no hosts registered, its output is exactly as before.
- **Completion.** After an `@`, completion offers the registered hosts. It reads the registry and never connects.
- **Not remote yet.** `backup create`, `backup restore`, `env migrate` and `system parity-check` refuse an
  `org@host`, because they read your backup key or write your files. For those, use `backup schedule on --host` and `org migrate`
  (below). `system build` and `system pull` act on this machine. `backup schedule` takes `--host` (below).
- **`fw edit` on a host.** It opens the editor on that host, over `ssh -t`.

## Commands

```bash
berth host add box1 ops@box1.example           # [user@]host[:port]; the user defaults to $USER, the port to 22
berth host add box2 box2.example --engine podman   # the engine is detected (Docker, else Podman) unless given
berth host add box3 ops@203.0.113.7 --bind localhost   # new orgs there bind to localhost: reach them with berth org connect
berth host ls                                  # every host: reachable, Docker version, number of orgs
berth --output json host ls                    # the same, as berth.hosts/v1 (docs/json.md)
berth host rotate-access box1                  # replace berth's key on box1
berth host guard box1 [on|off|status]          # the host guard (on from host add)
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
5. **Starts the host guard** (below), unless `--no-guard`.
6. **Records the host** in `~/.config/berth/hosts.yaml`. `--home <dir>` sets the state root on the host; the
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

## Moving an org between hosts

`berth org migrate acme box1` moves `acme` from this machine to `box1`. `berth org migrate acme@box1 local` moves it back,
and `acme@box1 box2` moves it between hosts (#50). There are two stages.

**1. Rehearsal (no downtime).**

- The org is streamed into a **stopped** copy on the target, while it keeps running where it is. The stream is
  the backup format, unencrypted, over berth's SSH connections.
- The copy's ports and bind address are fitted to the target, as `backup restore` does.
- The copy is then checked: `org.env` (except what `backup restore` adjusts), every file under `config/`, and every
  file's path and size except the regenerable data backups skip. Files that changed while the org ran are
  reported, not counted against it.

**2. Switch (an announced restart).**

- berth asks first. It says the org will stop where it is, and how long the rehearsal took (the final copy takes
  about as long).
- Then it:
  1. stops the org;
  2. copies it again, so nothing written since the rehearsal is lost;
  3. checks this copy **exactly**;
  4. moves the lease;
  5. starts the org on the target, and rehydrates it there.
- **Without a terminal:** `--yes` gives the go-ahead. With neither a terminal nor `--yes`, berth stops after the
  rehearsal. `--no-switch` always stops there. Run it again with `--yes` to switch; the earlier copy is replaced.

**If anything fails:**

- **Before the stop:** nothing changes where the org runs.
- **After it:** berth starts the org where it was again, with its lease.

**The old copy stays**, stopped, where it was, until you remove it. `up` refuses to start it there, because the
target holds the lease.

**`--as <name>`** gives the copy another name.

**The older form still works:** `migrate <org> <[user@]host>`, ccenv's stream to berth on any SSH host. It's used
when the target isn't a registered host.

## Backups on a host

`berth backup schedule on --host box1` gives a host its own nightly `backup --all` (#49). It takes the same flags as a local
schedule (`--at`, `--keep`, `-o <dir on the host>`, `status`, `run`, `off`).

`backup schedule on --host` does the following:

1. **Puts berth on the host.** For a release, that's the signed release binary for the host's architecture, verified
   as `berth system upgrade` does. It goes in `~/.local/opt/berth/<version>/`, linked from `~/.local/bin/berth` there.
   (A development build can only be pushed to a host of its own platform, unverified, and berth says so.)
2. **Runs the host's own `berth backup schedule on`** with `BERTH_BACKUP_RECIPIENTS` set to your public key: the one in
   `$BERTH_BACKUP_RECIPIENTS`, or `backup.key`'s public half. The host sets up a systemd user timer, or cron where
   it has no systemd user manager, just as a local schedule does.

Things to know:

- **Only public keys reach the host.** Its backups are encrypted to your key, and your private `backup.key` never
  leaves this machine. The host can make backups but can't read them.
- **Upgrades keep it working.** The job calls `~/.local/bin/berth`, so upgrading berth on the host (running
  `backup schedule on --host` again with a newer berth, or `berth system upgrade` there) keeps it running.
- **Where backups go:** the host's `<state root>/backups/` by default, or `-o`.
- **Logged-out runs:** with systemd, the host reports whether linger is on. Without it, runs happen only while that
  user is logged in (`sudo loginctl enable-linger <user>` there).

### Getting backups off the host

A backup that stays on the host is lost with the host. Copy them somewhere else regularly. They're encrypted, so
any storage will do:

```bash
rsync -a ops@box1.example:.local/share/berth/backups/ ~/berth-backups/box1/        # to this machine
rclone copy ops@box1:.local/share/berth/backups remote:berth/box1                  # or object storage
```

Restore one here with `berth backup restore <file>`, which uses your key. Automatic pulls may come later.

## The host guard

Each org has its own egress firewall inside its container. On a registered host, berth adds a second layer
underneath it on the host itself (#48). This matters most on a cloud VM:

- **The host:** an org container can't open connections to the host, whether through its bridge gateway or any of
  the host's addresses. That covers anything listening there (sshd, databases, the Docker API on TCP).
- **Cloud metadata:** an org container can't reach `169.254.0.0/16`, which includes the metadata endpoint
  `169.254.169.254`.

Replies to connections the org opened still get through, and so does allowlisted egress.

### Scope

The rules cover only org networks: the bridges of compose projects named `claude-<org>`. Other containers on the
host are left alone.

### How it runs

- **The container:** it runs as `berth-host-guard`, from berth's image, with the host network, `NET_ADMIN` and the
  Docker socket. Anyone in the host's `docker` group has that much access already, so it needs no root login and
  installs nothing on the host.
- **The chains:** it keeps two chains in the host's filter table, `BERTH-INPUT` (from `INPUT`) and `BERTH-FORWARD`
  (from Docker's `DOCKER-USER`).
- **On a rootful Podman host** it is the same container on Podman's socket. `BERTH-FORWARD` hangs from `FORWARD`,
  since there is no `DOCKER-USER`, and DNS to the host is let through: Podman's containers resolve names through
  aardvark-dns on their bridge's gateway. On a rootless Podman host the guard keeps its rules inside the user's
  network namespace instead ([Container engines](engines.md#podman)).
- **Updates:** it rebuilds both chains every 20 seconds, and at once when berth starts an org there. New orgs are
  covered without a restart.
- **Reboots:** Docker's restart policy brings it back after a reboot.

### Turning it on and off

- `berth host add` starts it (`--no-guard` skips it).
- `berth host guard <name> on|off|status` turns it on or off later, or shows its rules. Either way, nothing restarts.
- `berth host rm` takes the rules out and removes the container.
- If a host can't be reached when it's removed, run `docker rm -f berth-host-guard` on it. Its rules then last until
  the host reboots.

### This machine

The guard is only for registered hosts. This machine's own orgs are unchanged.

## Active-host leases

Once hosts are registered, the same org can exist on more than one of them, after a `migrate` or a restore
elsewhere. Two running copies would share one Remote Control login and git identity. So each org has a **lease**
that names the one host allowed to run it (#47).

- **First start:** an org on a single host gets the lease there on its first `up` or `restart`. Nothing else changes,
  and nothing restarts because of it.
- **Other hosts:** `up` and `restart` on any other host refuse, naming the host that holds the lease.
- **Moving the lease:** `berth up acme@box2 --take-lease` moves it. The copy on the old host is **stopped first**,
  and berth says so: that's a restart there, and the work running in it stops. If the old host can't be reached,
  the lease stays. A lease on a host that was removed (`host rm`) moves without stopping anything.
- **No lease yet, org on several hosts:** berth refuses and asks which host runs it (`--take-lease` on that one).
- **Where leases live:** in `~/.config/berth/leases.yaml`. The holding host also gets a marker at
  `<state root>/berth/lease/<org>`.
- **No registered hosts:** there are no leases at all. Nothing is read or written, and `up` behaves exactly as before.

To move an org to another host, use `migrate` (below). It hands the lease over as its last step.

## Files

| File (on this machine) | What |
|---|---|
| `~/.config/berth/hosts.yaml` | the registry (0600). Unknown keys are an error, so a typo can't drop a host. |
| `~/.config/berth/keys/<name>`, `<name>.pub` | berth's key for each host (0600, unencrypted, like a default `ssh-keygen` key) |
| `~/.config/berth/known_hosts` | the hosts' pinned keys |
| `~/.config/berth/leases.yaml` | each org's active host (0600) |

`$XDG_CONFIG_HOME` replaces `~/.config` when it's set.

## Security

- **What a host receives:** only berth's public key. Your `backup.key` never goes to a host; hosts only ever get
  age recipients (docs/plan.md, decision 4).
- **Access to a host:** anyone who can read `~/.config/berth/keys/` on this machine can log in to your hosts as
  their users, just as with `~/.ssh`. `host rotate-access` replaces a key; `host rm` revokes it.
- **What berth trusts:** a host key only after you confirm its fingerprint (or pass `--accept-new-host-key`), and
  after that only that key.

## A host that runs `berth serve`

When a registered host runs its own [`berth serve`](api.md), berth asks it for whole operations instead of
driving it one ssh command at a time (#148). It forwards the host's API socket over the ssh connection it already
has, so nothing listens on the host's network, and the host's berth runs the command there.

- **What goes that way:** what is the same wherever it runs: `fw show|allow|deny|presets|test`,
  `repo ls|add|rm`, `env ls`, `pkg ls`, `account remote status`.
- **What keeps the ssh path:** what depends on this machine: `up`, `restart`, `down` (the active-host lease, the
  host guard), `info` and `ls` (the registry, the tunnel), backups (your key), and anything interactive.
- **Nothing to set up here.** berth looks for the socket at each command; a host with no server, or one that
  doesn't answer, is driven over ssh as before. `BERTH_HOST_API=off` keeps everything on ssh.
- **On the host**, run `berth serve` as the user berth logs in as: `berth system service install` there keeps
  it running ([as a service](api.md#running-it-as-a-service)). A change that
  comes through it is in that host's `audit.log`, with `"via":"api"`.

Output, messages and exit codes are the command's own, from the host's berth: keep it at the version you run here.

## Creating a host at a cloud provider

`berth host create` makes a VM and registers it, closed to the internet from the first second (#52). The provider
is [Hetzner Cloud](decisions/051-first-cloud-provider.md).

```bash
export HCLOUD_TOKEN=…               # an API token (read and write) of a project for berth's hosts alone
export BERTH_TAILSCALE_AUTHKEY=…    # a single-use, tagged Tailscale auth key
berth host create box3 --provider hetzner            # cax21 (4 vCPU arm64, 8 GB) in fsn1, Ubuntu 24.04
berth host create box4 --provider hetzner --size cax31 --region hel1
```

Both secrets come from the environment, never the command line.

**What it does:**

1. Makes an ssh host key for the server, and a firewall with **no rules**: nothing comes in from the internet.
2. Creates the server behind that firewall, with cloud-init that installs the host key, an `ops` user for berth,
   Docker (the newest 28.x, from Docker's own repository) and Tailscale, turns password logins off, and joins your
   tailnet as `berth-<name>`.
3. Waits for it on the tailnet, then registers it as `host add` does, the host guard included. The host key is
   pinned before the first connection: there is no trust on first use.

- **This machine must be on the tailnet**, since that is the only way in.
- **Everything is labelled** `berth.managed=true` and `berth.host=<name>`.
- **A failure at any step removes what was created.**

```bash
berth host destroy box3       # forgets the host and deletes its server and firewall; refused while it has orgs
berth host reconcile          # what carries berth's labels, against the registry (exit 3 if something is left)
berth host reconcile --prune  # deletes what belongs to no registered host
```

**Know before you use it:**

- **Use an auth key that is tagged and not reusable.** It stays readable in the server's user data, so a reusable
  one could enrol other machines. A host that joins without a tag has a key that expires, and is out of reach when
  it does: `host create` warns when it sees that, with the date.
- **`host destroy` doesn't touch your tailnet**: berth has no credential for it. It names the machine
  (`berth-<name>`) for you to remove in the Tailscale admin console.
- The token can do anything in its project: give berth a project of its own.
- The server's user data (its host key, and the used auth key) can be read back from the VM's metadata service.
  Org containers can't reach it: the host guard and each org's firewall both block 169.254.0.0/16.

### Testing against Hetzner

`host create` is tested against a stand-in for Hetzner's API in the ordinary test run, and against Hetzner itself by
one acceptance test (#52). It makes a real server (billed by the hour, deleted at the end whatever happens), an org
on it, checks that no TCP port of the public address is open and that the server presents the pinned host key, then
destroys both and checks that nothing labelled is left.

```bash
# from a machine on the tailnet, with both variables set as above
go test -tags cloud -count=1 -timeout 30m -v ./test/cloud/
```

The `cloud` workflow runs the same test, started by hand (`gh workflow run cloud`). It needs three repository
secrets: `HCLOUD_TOKEN`, and `TS_OAUTH_CLIENT_ID` and `TS_OAUTH_SECRET` of a Tailscale OAuth client that owns
`tag:ci` and `tag:berth` and may write auth keys and devices. The tailnet's policy must let `tag:ci` reach
`tag:berth`. The workflow makes a single-use tagged key for each run and removes the test's machine from the
tailnet afterwards.

