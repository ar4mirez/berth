# Backups and restore

Backups **skip what can be regenerated**:
- mise toolchains;
- `node_modules`, `.venv`, cargo `target/`, `vendor/bundle`;
- tool caches.

They **keep everything else**:
- repos with their full history, and uncommitted work;
- `.env` files, config and keys;
- the token;
- Claude's history and memory.

A 2 GB org typically backs up to a few MB.

```bash
berth backup create acme --plan                  # what's kept and skipped, and how big
berth backup create acme                         # encrypted, to <state root>/backups/acme-<date>.tar.zst.age
berth backup create --all -o /mnt/nas/berth/     # every org berth manages
berth backup create acme -o - > acme.tar.zst.age # to stdout
berth backup create --all --keep 7               # and prune to the newest 7 per org
```

## Encryption

Every backup is encrypted, unless you pass `--no-encrypt`, which prints a warning. Both methods are authenticated: a
wrong key, a corrupted file or any tampering fails the restore, and nothing is left behind.

| Method | Used when | Good for |
|---|---|---|
| **Key** (age, X25519) | `berth backup keygen` made `~/.config/berth/backup.key`, or you pass `-r <age1…\|ssh-ed25519 …\|file>`, or `BERTH_BACKUP_RECIPIENTS` is set | unattended backups, no prompts |
| **Passphrase** (GnuPG, AES-256) | there's no key, or `--passphrase`, or `BERTH_BACKUP_PASSPHRASE` is set | one-off backups |

```bash
berth backup keygen      # once; then keep a copy of ~/.config/berth/backup.key somewhere safe (a password manager)
```

**The key is the only way back.** Without `backup.key` or the passphrase, an encrypted backup can't be restored. Keep
a copy away from the machine being backed up.

Things to know:
- **Where it runs:** encryption and decryption happen inside berth's image. The host needs nothing but Docker.
- **How secrets reach it:** through a private read-only mount, never the command line or environment.
- **How restore works:** it decrypts and extracts in one stream into a staging folder. That folder becomes the org
  folder only once the whole archive has checked out.

## Restore

```bash
berth backup restore backups/acme-20260101-030000.tar.zst.age            # restores, starts it, reinstalls toolchains and deps
berth backup restore file.age --as acme-copy                               # under another name (free ports are picked)
berth backup restore file.age -i ~/backup.key                              # with a key from elsewhere
berth backup restore - < file.age                                          # from stdin
berth backup restore file.age --no-start --no-rehydrate                    # restore only
berth org rehydrate acme                                                # reinstall mise tools and project dependencies
```

The restored org is berth's, with its ports and bind address fitted to this machine. `--force` replaces an existing
org of that name: it's stopped, and moved to `backups/.replaced/`.

## Scheduled backups

```bash
berth backup schedule on                              # nightly at 03:00, keep the newest 14 per org (needs a key)
berth backup schedule on --at 01:30 --keep 30 -o /mnt/nas/berth
berth backup schedule status                       # next run, and the last runs' output
berth backup schedule run                          # run it now
berth backup schedule off
```

**How it runs:** through a systemd user timer where there is one, and cron otherwise. A run missed while the machine
was off happens at the next boot.

**Logged out:** runs happen only while you're logged in, unless linger is on (`sudo loginctl enable-linger $USER`).
berth tells you when it's off.

**Pruning:** it only touches that org's `<org>-<date>.tar.zst*` files.

**On a registered host:** `berth backup schedule on --host box1` sets up the same schedule on that host, with only your public
key ([Hosts](../hosts.md#backups-on-a-host)).

## Moving an org to another machine

`berth org migrate acme box1` moves an org to a registered host, with a rehearsal first and an announced switch
([Hosts](../hosts.md#moving-an-org-between-hosts)). To a machine that isn't registered, `berth org migrate acme
me@newbox` streams it to berth there over SSH (ccenv's way). Stop the old copy afterwards (`berth down acme`), so two
containers don't share one Remote Control login.
