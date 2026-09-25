# claude-envs

One isolated Claude Code container per organization. Each org has its own Claude account,
git identity, repos, history and firewall, and you can reach it from any device.

```
claude-envs/
├── ccenv            # the CLI (`./ccenv install` puts it on PATH)
├── compose.yml      # generic template, parameterized by orgs/<org>/org.env
├── image/           # shared image: Claude Code, sshd, ttyd, tmux, firewall
└── orgs/<org>/      # ALL per-org state (gitignored)
    ├── org.env          token, git identity, ports
    ├── config/          mounted read-only, edits apply live
    │   ├── firewall.txt     egress allowlist (ccenv fw)
    │   ├── authorized_keys  devices allowed to SSH in
    │   └── secrets/         browser-terminal password (ccenv password)
    ├── workspace/       repos  -> /workspace
    ├── claude/          ~/.claude (settings, sessions, memory, CLAUDE.md)
    ├── mise/            toolchains, cargo/rustup, Go and build caches (persisted)
    ├── ssh/             org git key -> ~/.ssh
    └── sshd/            container host keys (stable fingerprints)
```

## New org

```bash
./ccenv install             # once per machine: ~/.local/bin/ccenv + bash completion
ccenv init globex --name "Your Name" --email you@globex.com
ccenv up globex
ccenv auth globex         # token + Remote Control login, run inside the container
# add orgs/globex/ssh/id_ed25519.pub to that org's GitHub (account key or deploy key), e.g.:
#   gh ssh-key add orgs/globex/ssh/id_ed25519.pub --title claude-globex
ccenv gh-login globex     # gh CLI (PRs, issues, API) signed in inside the container, persisted in home-config/
ccenv clone globex git@github.com:globex/repo.git
ccenv whoami                # confirm each org is signed in with the right account
```

### Several Claude accounts on one machine

Sign-in always runs **inside the org's container**, which has its own Claude config folder. Your host's `claude` session
and the other orgs are never touched. The only shared piece is your browser's claude.ai session, so open the sign-in link
in a private window (or a browser profile per org) and sign in with that org's account. `ccenv whoami` shows the
account each org's Remote Control login uses. The token doesn't report its account, so use the same private window
for both steps of `ccenv auth`. After signing in, `ccenv auth`/`ccenv login` warn if two orgs use the same Claude organization. They compare
organizations, not emails, so one email that belongs to several organizations is fine. If you share an account on
purpose, add `SHARED_ACCOUNT=1` to one of the orgs' `org.env` files to silence the warning. If you belong to several
Claude organizations, pick the right one on the organization screen during sign-in.

## Connecting

| From | How |
|---|---|
| This host | `ccenv attach <org>` (persistent tmux session `main`) |
| Any device on your tailnet | `ssh -t -p <SSH_PORT> node@<tailscale-ip> tmux new -A -s main` |
| A browser, phone or tablet | `http://<tailscale-ip>:<TTYD_PORT>` (credentials in org.env) |
| claude.ai/code or Claude app | Open the org's environment URL (from `ccenv remote <org>`) and start new sessions from the browser or phone |
| VS Code / Cursor | Remote-SSH to `claude-<org>` (snippet in `ccenv info`) |
| Scripts / cron | `ccenv run <org> "prompt"` (headless `claude -p`) |

The terminal methods all attach to the **same** tmux session, so you can start on your laptop and continue on your phone.

### Remote Control (claude.ai/code)

Each container runs `claude remote-control` as a supervised service. It restarts automatically and keeps the same
environment URL. In claude.ai/code or the Claude app, pick the org's environment and click **New session**. Sessions
run in `/workspace` on latest Opus with no permission prompts, and up to `REMOTE_CAPACITY` (default 8) can run at once.

It needs a one-time `ccenv login <org>` because long-lived tokens are inference-only. The login is saved in
`orgs/<org>/claude/`, so it survives restarts and moves. Use `ccenv remote <org> [status|logs|restart]` to manage it.
Ports bind to `BIND_ADDR=tailscale` by default, which means only your tailnet can reach them, never the public internet.

## Repos: only what you register

Each org has an allowlist, `orgs/<org>/config/repos.txt`. Only repos registered through `ccenv` may exist or be used in the
container, and the container can't change the list because it's mounted read-only.

```bash
ccenv repo add acme acme/webapp               # register + clone (owner/repo, ssh or https URL)
ccenv repo add acme acme/api --branch develop --dir api-dev
ccenv repo new acme acme/new-svc --private -d "New service" --gitignore Node   # create on GitHub + register + clone
ccenv repo new acme acme/new-app --template acme/app-template                 # from a template repo
ccenv repo new acme prototype --local           # no GitHub repo yet (git init); Claude can commit locally
ccenv repo publish acme prototype acme/prototype --private   # later: create it on GitHub, push, switch remote
ccenv repo ls acme                              # registered repos, branch, uncommitted changes, strays
ccenv repo rm acme api-dev [--delete]           # unregister (folder is quarantined unless --delete)
ccenv repo adopt acme <dir>|--all               # register folders already there / bring one back from quarantine
ccenv repo sync acme                            # clone registered-but-missing (also runs in rehydrate)
ccenv repo policy acme [enforce|warn|off]       # default enforce
```

New repos are created on GitHub from the **host** with your `gh` login, or with the container's own `ccenv gh-login` if
the host has no `gh`. Claude never gets the ability to create or add repos itself.

It's enforced in three independent layers:

| Layer | What it stops | How |
|---|---|---|
| **git** | clone, fetch or push of any unregistered repo, by anyone in the container (Claude, you over SSH, scripts) | The org key is mounted root-only. `git` reaches it only through `git-ssh-guard` (one sudo rule), which checks the repo against the list and rebuilds the ssh command itself, so injected options are refused. `https://github.com/...` is rewritten to go through the same guard. |
| **Claude** | cloning, `git remote add/set-url` to unlisted repos, tampering with git's transport, reading or editing unregistered `/workspace/*` folders, and reading credential files | A `PreToolUse` hook in managed settings (`repo-guard.js`). It runs even in bypass-permissions mode, blocks if it fails, and can't be edited from inside. |
| **workspace** | anything else placed in `/workspace` | Every 30s, unregistered items are moved to `orgs/<org>/quarantine/`. Nothing is deleted. |

For Claude, the hook also blocks code downloads from unregistered repos over the API or CDN: `gh api repos/o/r/tarball|contents`,
`codeload.github.com`, `raw.githubusercontent.com` and `github.com/o/r/archive`. Metadata such as `gh pr list` and
package registries still work.

Known limit: someone running commands by hand in the container, not Claude, could still pull an unregistered repo's
code with a signed-in `gh` token (`ccenv gh-login`) or a public tarball URL. The sweep then quarantines anything that lands in
`/workspace`, but the files could be kept elsewhere, such as `/tmp`. For the strictest setup, skip `gh-login` in that org.

## Environment variables (API keys)

```bash
ccenv env acme set OPENROUTER_API_KEY     # prompts for the value (hidden); or pipe it: ... | ccenv env acme set KEY
ccenv env acme                            # names only, never values
ccenv env acme unset OPENROUTER_API_KEY
```

Values are stored single-quoted in `orgs/<org>/org.env` (0600), so `$` and spaces are taken literally, and the container is
recreated to apply them (`--no-restart` to defer). They reach every kind of session: Remote Control, `attach`/`claude`/`run`,
the browser terminal and SSH. ccenv's own keys (tokens, ports, `GH_TOKEN`, ...) are refused, and values can't contain `'`.
Like the other tokens in `org.env`, anyone with Docker access on the host can read them with `docker inspect`.

## Installing software (mise)

Every container ships with [mise](https://mise.jdx.dev) plus the build dependencies it needs to compile tools like Ruby.
Claude is told to use it through a managed `CLAUDE.md` (`image/CLAUDE.md`), so it can install what a task needs:

```bash
mise use -g go@latest python@3.13 uv@latest rust@stable ruby@3   # org-wide
mise use node@lts                                                  # per repo (writes mise.toml)
mise use -g aqua:mikefarah/yq                                      # any CLI from aqua/ubi/npm/pipx
```

Tools live in `orgs/<org>/mise/`, so they survive restarts and rebuilds and move with the folder. Each org has its
own set. New orgs' firewalls allow `@mise @python @go @rust @ruby` by default, which covers toolchain downloads and
package registries. If Claude hits a blocked host, it tells you the `ccenv fw <org> allow ...` command to run.

## Model

Every session is pinned to `opus`, which always resolves to the latest Opus, through managed settings
(`image/managed-settings.json`). That includes sessions started from claude.ai.

## Security model

- Claude runs with `bypassPermissions` because the container is the sandbox.
- A default-deny egress firewall allows only Anthropic, GitHub and npm, plus whatever is in `config/firewall.txt`.
- Each org has its own git key, token and `~/.claude`, and nothing is shared across orgs.
- SSH is key-only (`authorized_keys`).
- The browser terminal password is 32 random characters, kept in a `0600` file. It never appears in `org.env`, the
  container environment, `ccenv info` output or the logs. Use `ccenv password <org>` to show it and `ccenv password <org> rotate` to change it.

## Firewall

```bash
ccenv fw acme                       # show the allowlist and live status
ccenv fw acme allow @python         # presets: python node go rust docker gitlab bitbucket aws gcp azure debian
ccenv fw acme allow api.stripe.com https://sentry.example.com/path   # domains or URLs
ccenv fw acme deny api.stripe.com
ccenv fw acme test pypi.org example.com
ccenv fw acme off | on              # temporary full access
ccenv fw acme edit                  # open firewall.txt in $EDITOR, then apply
```

Changes take effect immediately, with no restart. The allowlist is also re-resolved every 5 minutes because CDN IPs change.

## Backup, restore and moving machines

Backups skip anything that can be regenerated: mise toolchains, `node_modules`, `.venv`, cargo `target/`,
`vendor/bundle` and tool caches. They keep repos with their full `.git` history, uncommitted work, `.env` files, config,
keys, the token, and Claude's history and memory. A 2.1 GB org backs up to about 6.5 MB.

```bash
ccenv backup acme --plan                 # preview: what's kept, what's skipped and how big
ccenv backup acme                        # encrypted by default -> backups/acme-<date>.tar.zst.{gpg|age}
ccenv backup --all -o /mnt/nas/ccenv/      # every org
ccenv restore backups/acme-....age       # restores, starts, then reinstalls toolchains and deps
ccenv restore file --as acme-copy        # restore under a new name (free ports are picked automatically)
ccenv migrate acme me@newbox             # stream to another machine over ssh; installs claude-envs there if needed
ccenv rehydrate acme                     # reinstall mise tools and project deps (npm/pnpm/yarn/bun/uv/pip/bundler/go/cargo)
```

### Scheduled backups

```bash
ccenv schedule                             # nightly at 03:00, keep the newest 14 per org (needs: ccenv keygen)
ccenv schedule --at 01:30 --keep 30 -o /mnt/nas/ccenv
ccenv schedule status                      # next run and last run's output
ccenv schedule run                         # run it now
ccenv schedule off
ccenv backup --all --keep 7                # --keep also works on manual backups
```

This uses a systemd user timer where one is available, and cron otherwise. With `Persistent=true`, a run missed while the machine was off
happens at next boot. Pruning only touches `<org>-<date>.tar.zst*` files for that org and ignores everything else in the folder.

### Encryption

Every backup is encrypted unless you pass `--no-encrypt`, which prints a warning. Both methods are authenticated:
a wrong key, a corrupted file or any tampering fails the restore, and nothing is left behind.

| Method | When it's used | Good for |
|---|---|---|
| **Key** (age, X25519) | `ccenv keygen` created `~/.config/ccenv/backup.key`, or you pass `-r <age1...\|ssh-ed25519 ...\|file>` / set `CCENV_BACKUP_RECIPIENTS` | cron and unattended backups, with no prompts |
| **Passphrase** (GnuPG AES-256, MDC) | no key, `--passphrase`, or `CCENV_BACKUP_PASSPHRASE` set | one-off backups (at least 12 characters when prompted) |

```bash
ccenv keygen                               # once; then store a copy of ~/.config/ccenv/backup.key in your password manager
ccenv backup --all                         # now non-interactive: encrypted to that key
ccenv restore file.age -i ~/backup.key     # on another machine (or copy the key to ~/.config/ccenv/ first)
```

- **The key is the only way back.** Without `backup.key` or the passphrase, an encrypted backup can't be restored.
  Keep a copy somewhere that isn't the machine being backed up.
- Encryption and decryption run inside the image, so the host needs nothing but Docker. Secrets reach the engine
  through a private read-only temp mount, never through the command line, environment variables or the backups folder.
- Restore decrypts and extracts in one stream into a staging folder, which becomes the org folder only after the
  archive has been fully verified. No decrypted archive is ever written to disk.
- `backups/` and every backup file are `0700`/`0600`. `migrate` streams unencrypted but over ssh, which encrypts it.
- Backups run as root in a throwaway container with no network, so they can read root-owned files (sshd host keys). On
  restore, file ownership is set to whoever runs `ccenv` on that machine.
- After `migrate`, the source container keeps running. Stop it with `ccenv down <org>` once the copy works, so two containers
  don't share the same Remote Control login.

## Updating Claude Code

`ccenv build --no-cache`, then `ccenv restart <org>`. The auto-updater is off inside containers, so every org runs
the version baked into the image.
