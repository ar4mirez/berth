# Parity: legacy `ccenv` → `berth`

berth must behave exactly like `legacy/ccenv` (plan, "Parity first"), except where this file records
an intentional divergence and its reason. It has one row per command, subcommand, flag and alias of
`legacy/ccenv`. `test/parity/inventory_test.go` extracts that list from the script and fails if a
row is missing, so a legacy change (like `ccenv env` in #11) can't slip past.

The comparison itself is `test/parity` (see its package doc). It runs both tools on the same
synthetic fixtures, with fake `docker`/`tailscale`/`gh`/`ssh`/`systemctl`/`crontab` on PATH, and
diffs stdout, stderr (with the `ccenv:`/`berth:` prefix normalized), exit codes, the fake-tool calls
(argv and the compose env), and the resulting file tree with modes. A row is **done** only when its
scenarios pass that comparison in CI.

**Status:** `done` · `partial` (what's missing is noted) · `not started` · `berth-only` (an
intentional addition) · `(pN)` is the plan phase that ports it.

**Conventions in the cells:**
- `O` is `orgs/<org>`.
- `compose X` is ccenv's `compose()`: it creates `O/home-config` and `O/quarantine` (0700) if
  missing, then runs `docker compose -f <root>/compose.yml --env-file O/org.env X` with `BIND_ADDR`
  (resolved; see `resolve_bind`), `ORG`, `ORG_DIR`, `HOST_UID` and `HOST_GID`.
- `need_org` dies with `ccenv: missing <org>` or `ccenv: unknown org '<org>' (run: ccenv init <org>)`
  and exit 1, and refuses orgs with `MANAGER=berth`.
- `need_up` dies unless `claude-<org>` is in `docker ps`.
- `running` is `docker ps --format {{.Names}}`.

## Grammar and global behaviour

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `(no arguments)` | usage on stdout, exit 0 | – | – | – | partial: exit code matches; the help text differs until the commands exist (p1) |
| `(unknown command)` | usage on stdout, nothing on stderr, exit 1 | – | – | – | partial: exit code and empty stderr match; help text as above (p1) |
| `help` / `-h` / `--help` | usage on stdout, exit 0 | – | – | – | partial: same as no arguments (p1) |
| `(error prefix)` | errors are `ccenv: <msg>` on stderr, exit 1 | – | – | – | done: berth prints `berth: <msg>`, the one intended difference, normalized by the harness |
| `--version` | – | – | – | – | berth-only: `berth <version> (commit, date)`. No `-v`, which is kept free for a future verbose flag |
| `--home <dir>` | – | – | – | – | berth-only: state root; `--home` > `$BERTH_HOME` > `config.yaml` > `~/.local/share/berth` (empty on purpose) |
| `--read-only` | – | – | – | – | berth-only: refuses every writing command, and every `host.FS` write |

## Setup

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `install [bin-dir]` | `Linked …`, `Completion installed …`, and a `NOTE` if bin-dir isn't on PATH | symlink `<bin-dir>/ccenv` → the script (default `~/.local/bin`); `${XDG_DATA_HOME:-~/.local/share}/bash-completion/completions/ccenv` | – | – | not started (p1). The plan also has `berth install --alias ccenv` |
| `completion` | the bash completion script | – | – | the script finds orgs through `readlink $(command -v ccenv)/../orgs` | not started (p1): planned divergence, cobra `ValidArgsFunction` over the state root, with the preset list |
| `init <org>` | `Created O`, then the next steps (with the git public key); `ccenv: org name must be lowercase [a-z0-9-]` or `<org> already exists` | `O/{workspace,claude,ssh,sshd,mise,home-config,config/secrets}`: O, ssh, claude, home-config and config/secrets are 0700, the rest follow the umask; `ssh/id_ed25519{,.pub}`; `config/authorized_keys` (`~/.ssh/*.pub`, or empty); `config/secrets/ttyd_credential` 0600 `node:<32 alnum>`; `config/firewall.txt` (template); `org.env` 0600 (template) | `ssh-keygen -q -t ed25519 -N '' -C claude-<org>@<hostname> -f …`; `git config --global user.name/user.email`; `hostname` | `BIND_ADDR=tailscale` if `tailscale` is on PATH, else `127.0.0.1`. `SSH_PORT`/`TTYD_PORT` from `next_port` (2201/7701). No `quarantine/`: the first `compose` creates it | not started (p2) |
| `init --name <name>` / `--email <email>` | `ccenv: unknown flag <f>` for anything else | defaults from `git config --global` | | | not started (p2) |
| `auth <org>` | `== Step 1/2 …`, then `token`, `== Step 2/2 …`, then `login` | as token + login | as token + login | interactive | not started (p2) |
| `token <org>` | the browser hint; a hidden prompt `Paste CLAUDE_CODE_OAUTH_TOKEN for <org>:` if capture fails; `Token saved for <org>.`; dies unless the token is `sk-ant-*` | `setval CLAUDE_CODE_OAUTH_TOKEN` (in place, keeps inode and mode) | `docker exec -it -u node -w /workspace claude-<org> bash -c '… script … claude setup-token …'` (captured in `/dev/shm`, then deleted); if running, `compose up -d --force-recreate`, then `sleep 3` | needs the container up (without `--paste`) | not started (p2) |
| `token --paste` / `--no-restart` | `--paste`: skip the capture and read from stdin. `--no-restart`: no recreate | | | | not started (p2) |
| `login <org>` | the browser hint, the Remote Control status, `Signed in as: …`, and a WARNING if another org uses the same Claude organization (unless `SHARED_ACCOUNT=1`) | – | `docker exec` logout plus `rm .credentials.json`; `docker exec -it … claude auth login`; `pkill -f "claude remote-control"`; polls up to 20×2s | interactive | not started (p2) |
| `logout <org> [--all]` | `Logged out <org> …`; with `--all`, also `Token removed too.` and `Restarted claude-<org>.`; always exit 0 | stopped: `rm O/claude/.credentials.json`. `--all`: `setval CLAUDE_CODE_OAUTH_TOKEN ""` | running: `docker exec` logout, `pkill`. `--all` and running: `compose up -d --force-recreate` | – | not started (p2) |
| `gh-login <org> [--force]` | `gh already signed in as X` (exit 0) unless `--force`; device-code instructions; dies if sign-in didn't complete | `O/home-config/gh` (through the container) | `docker exec -u node … gh api user -q .login`; `docker exec -it -e BROWSER=true … gh auth login --hostname github.com --git-protocol ssh --skip-ssh-key --web --scopes read:org,repo,workflow` | interactive | not started (p2) |
| `whoami [org...]` | table `ORG TOKEN GITHUB (gh) REMOTE-CONTROL LOGIN (account)`; `(container down)` for stopped orgs. With no args, all orgs | – | running: `docker exec -u node … claude auth status` (piped to `jq`), plus `gh api user` | an unknown or berth-owned org dies mid-table | not started (p1) |
| `up <org>` | compose output; then the last 5 lines of `docker logs claude-<org>` | `O/home-config`, `O/quarantine` 0700 if missing | `compose up -d --build --force-recreate`; `sleep 3`; `docker logs claude-<org>` | – | not started (p2) |
| `down <org>` | compose output | as `compose` | `compose down` | dies if `BIND_ADDR=tailscale` and Tailscale is down (resolved even though down doesn't need it) | not started (p2): planned fix, don't resolve for down/logs |
| `restart <org>` | compose output | as `compose` | `compose up -d --force-recreate` | – | not started (p2) |
| `build [docker-build-args...]` (e.g. `--no-cache`) | docker build output | – | `docker build -t claude-env --build-arg USER_UID=$(id -u) --build-arg USER_GID=$(id -g) "$@" <root>/image` | – | not started (p2): planned divergence, berth tags `berth/claude-env:<hash>` and never touches `claude-env:latest` |

## Use

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `ls` | table `ORG STATE SSH TTYD TOKEN REMOTE` (`%-14s %-6s %-6s %-6s %-8s %s`), every dir with an `org.env` (berth-owned ones too); REMOTE is `-`, `off`, `login-needed`, `on`, `blocked-by-org` or `restarting` | – | `docker ps`; running orgs: `docker exec` `test -f …/.credentials.json`, `pgrep -f "claude remote-control"`, and the remote-control.log tail | – | not started (p1); harness scenario `ls` |
| `info <org>` | the connection sheet (Remote Control URL, SSH, browser terminal, VS Code, ssh config, git public key) | – | `docker ps`; running: the `rc_url` grep | quirk: exits 1 with no output when org.env has no `SSH_PORT`/`TTYD_PORT` (`sp=$(envval …)` under `set -e -o pipefail`) | not started (p1); scenarios `info …` |
| `attach <org>` | tmux | – | `docker exec -it -u node claude-<org> tmux new-session -A -s main -c /workspace` | TTY; exit code passed through | not started (p2) |
| `shell <org>` | bash | – | `docker exec -it -u node -w /workspace claude-<org> bash -l` | TTY | not started (p2) |
| `claude <org> [args...]` | claude | – | `docker exec -it -u node -w /workspace claude-<org> claude "$@"` | TTY; passthrough, so berth uses `DisableFlagParsing` | not started (p2) |
| `run <org> "<prompt>" [args...]` | claude's output; `ccenv: missing prompt` | – | `docker exec -i -u node -w /workspace claude-<org> claude -p "<prompt>" "$@"` | passthrough, `DisableFlagParsing` | not started (p2) |
| `clone <org> <owner/repo\|url> [...]` | as `repo add` | as `repo add` | as `repo add` | alias of `repo add` | not started (p3) |
| `logs <org>` | `compose logs -f` | as `compose` | `compose logs -f` | follows until interrupted; resolves `BIND_ADDR` needlessly (as `down`) | not started (p1) |
| `env <org> [ls\|list]` | the names in `CCENV_ENV_KEYS`, with `(listed but missing)` if the line is gone; `(no custom variables; …)` | – | – | – | not started (p2) |
| `env <org> set <KEY> [--no-restart]` | `set: KEY`, then `claude-<org> recreated; …` or `applies on: ccenv restart <org>`. Dies on a bad or reserved key (`… is managed by ccenv`), an empty value, or a value containing `'` | `setval KEY "'<value>'"`; `setval CCENV_ENV_KEYS` | running and no `--no-restart`: `compose up -d --force-recreate` | value from a hidden prompt on a TTY, else one line of stdin; the entrypoint snapshots `CCENV_ENV_KEYS` for SSH sessions | not started (p2); scenarios `env …` |
| `env <org> unset\|rm <KEY> [--no-restart]` | `removed: KEY` …; dies with `KEY is not set for <org>` | every `KEY=` line removed (awk, `cat tmp > f`); `setval CCENV_ENV_KEYS` | as set | `--no-restart` is recognized only as the 4th argument | not started (p2) |
| `remote <org> [status]` | `Remote Control: running` with the URL, `Or: …` and the last `Capacity` line; or `not logged in`, `BLOCKED by the Claude organization's policy …`, `restarting` | – | `need_up`; `docker exec` `test -f`, `pgrep`, grep/sed of remote-control.log | – | not started (p2) |
| `remote <org> logs` | the last 40 log lines, ANSI stripped | – | `docker exec … sed … remote-control.log` | – | not started (p2) |
| `remote <org> restart` | `Restarting; back in ~5s.` | – | `docker exec claude-<org> pkill -f "claude remote-control"` | issue #7: a no-op while the service is in its blocked-by-policy backoff | not started (p2) |
| `password <org> [show]` | `user: node` and `pass: …`; dies if the file is empty | – | – | – | not started (p2); scenario `password show` |
| `password <org> rotate` | `Rotated; …` and `pass: …` | `O/config/secrets/ttyd_credential` rewritten `node:<32 alnum>` (umask 077 → 0600) | running: `docker exec claude-<org> pkill -x ttyd` | – | not started (p2) |

## Firewall

`fw <org>` writes the `config/firewall.txt` template first if it's missing, for every subcommand
(`show` included). Changes are applied live (`fw_apply`): if running, `docker exec claude-<org>
init-firewall.sh apply`; otherwise `(saved; applies on: ccenv up <org>)`.

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `fw <org> [show]` | the file path, the entries (no comments or blanks), and `live: …` if running | the template if missing | running: `docker exec … cat /run/firewall.status` | – | not started (p1); scenario `fw show, running` |
| `fw <org> allow\|add <entry>...` | `allowed: X` / `already allowed: X`; usage error with no entries | appended with `>>` (inode and mode kept) | `fw_apply` | URLs are reduced to the host (`https://x:443/p` → `x`) | not started (p3); scenario `fw allow, stopped` |
| `fw <org> deny\|remove\|rm <entry>...` | `removed: X` / `not in list: X` | rewritten (`cat tmp > f`) | `fw_apply` | exact match only, with no URL reduction (asymmetric with allow) | not started (p3); scenario `fw deny` |
| `fw <org> on\|off` | – | every `mode on/off` line removed, `mode <x>` appended | `fw_apply` | – | not started (p3) |
| `fw <org> edit` | – | edited by `$EDITOR` (default `vi`) | `fw_apply` | TTY | not started (p3) |
| `fw <org> reload` | – | – | `fw_apply` | – | not started (p3) |
| `fw <org> presets` | the preset list | – | running: `docker exec … init-firewall.sh presets`; else `docker run --rm --entrypoint init-firewall.sh claude-env presets` | – | not started (p1) |
| `fw <org> test [host\|url...]` | `ALLOWED  <url>` / `blocked  <url>`; default api.anthropic.com, github.com, example.com | – | `need_up`; `docker exec -u node … curl -s -o /dev/null --max-time 6 <url>` | – | not started (p3) |

## Repos

`repo <sub> <org>` checks the subcommand first (`add|new|create|publish|ls|list|rm|remove|adopt|sync|audit|policy`),
then runs `need_org`, creates `config/repos.txt` with its header if missing (even for `ls`), and
sources `image/repo-policy.sh`, whose rules are in `docs/repo-policy.md`.

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `repo add <org> <owner/repo\|url> [--dir d] [--branch\|-b b] [--no-clone]` | `Registered <canon> as /workspace/<dir>` …, `Cloned into …`; dies on an unparseable spec, a bad dir, or a dir registered to another repo; a failed clone rolls the registration back | `repos.txt` appended; on rollback rewritten | running: `docker exec … test -e`; `docker exec -u node -w /workspace … git clone [--branch b] <url> <dir>` | – | not started (p3) |
| `repo new\|create <org> <owner/repo> [--private\|--public\|--internal] [-d\|--description] [--template\|-p] [--readme\|--add-readme] [--gitignore\|-g] [--license\|-l] [--dir d]` | `Created https://<canon> (<vis>)`, then as `repo add` | as `repo add` | host `gh repo create …` if signed in, else `docker exec … gh repo create` | `sleep 3` after `--template` | not started (p3) |
| `repo new <org> <name> --local [--dir d]` | `Created local repo /workspace/<dir> (no remote) …` | `repos.txt` gets `<dir> local` | `need_up`; `docker exec … git init -q -b main <dir>` | – | not started (p3) |
| `repo publish <org> <dir> <owner/repo> [--private\|--public\|--internal]` | `Published …`; dies unless the dir is local-only with a commit and host gh is signed in | `repos.txt` line rewritten by awk (fields re-joined with single spaces) | host `gh repo create … --source … --remote origin --push`; `git -C … remote set-url origin` | – | not started (p3) |
| `repo ls\|list <org>` | table `DIR REPO BRANCH STATUS`, then `audit --quiet` | `repos.txt` if missing | running: `docker exec … test -d …/.git`, git branch/status | quirk: exit 2 before the audit output on an org never started (no `quarantine/`) | not started (p1); scenarios `repo ls, …` |
| `repo rm\|remove <org> <dir> [--delete]` | `Unregistered …`, then `Deleted …` or the quarantine note | `repos.txt` rewritten | running and `--delete`: `docker exec -u node … rm -rf /workspace/<dir>` | – | not started (p3) |
| `repo adopt <org> <dir>...\|--all` | `Registered …` / `Restored … from quarantine …` / `skip …` | `repos.txt` appended; quarantine → workspace `mv` | `git -C <dir> remote get-url origin` (host) | the registered URL is `repo_url(origin)`: an origin with no canonical form registers `git@:.git` | not started (p3) |
| `repo sync <org>` | `Cloned d` / `FAILED d` / `SKIP d …` / `All registered repos are present.` | – | `need_up`; `docker exec … test -e`, `git clone` | – | not started (p3) |
| `repo audit <org> [--quiet]` | `Not allowed in /workspace (policy: …): …`, `Quarantined (…)` list, `Workspace clean …` | – | – | glob `*` and `.[!.]*`; `.claude` exempt | not started (p1) |
| `repo policy <org> [enforce\|warn\|off]` | `REPO_POLICY=<v>` (the default shown as `enforce`); dies on another value | `setval REPO_POLICY` | running: `compose up -d --force-recreate` | quirk: exits 1 after succeeding when the org is stopped | not started (p3); scenario `repo policy set, stopped` |

## Backup, restore, migrate

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `backup <org>...\|--all` | progress on stderr (`== <org>`, the plan lines, `Wrote <file> (<size>, <kind>)`); usage error with no orgs; `-o` must be a dir for several orgs | `<backups>/<org>-YYYYmmdd-HHMMSS.tar.zst[.gpg\|.age]` 0600, dir 0700 | `ensure_image` (`docker image inspect claude-env`, `docker run … command -v age && command -v gpg`, else `build`); `docker run --rm -i --network none --entrypoint bash -e ORG -e SRC_HOST -e ENC -v <root>/image/archive.sh:/archive.sh:ro -v O:/src:ro claude-env /archive.sh create` | encryption: explicit flag > `CCENV_BACKUP_RECIPIENTS` > `CCENV_BACKUP_PASSPHRASE` > key file > prompt. `--all` skips berth-owned orgs | not started (p4) |
| `backup --plan\|--dry-run` | `== <org>` and `archive.sh plan` (sizes kept and skipped) | – | as above, `plan` | – | not started (p4) |
| `backup -o\|--output <file\|dir\|->` | `-`: the archive on stdout, which stays clean | the file, or a file in the dir | | | not started (p4) |
| `backup --encrypt` | a no-op, kept for compatibility | | | | not started (p4) |
| `backup --passphrase` | prompts on `/dev/tty` (confirm; at least 12 chars), or `CCENV_BACKUP_PASSPHRASE` | secrets in a private temp dir, mounted read-only | gpg AES256, S2K | | not started (p4) |
| `backup --recipient\|-r <age1…\|ssh-key\|file>` | dies on something that isn't a recipient | | age | | not started (p4) |
| `backup --no-encrypt` | a `WARNING: …` on stderr | plain `.tar.zst` | | | not started (p4) |
| `backup --keep N` | `Pruned <f> (keeping newest N)` on stderr | deletes older `<org>-[0-9]{8}-[0-9]{6}.tar.zst(.gpg\|.age)?` | – | only when writing to the backups dir or `-o` dir | not started (p4) |
| `keygen` | `Created <keyfile>`, `Public key: age1…`, the safety note; dies if it exists | `${CCENV_BACKUP_KEY:-$XDG_CONFIG_HOME/ccenv/backup.key}` 0600, dir 0700, in age-keygen's 3-line format | `ensure_image`; `docker run --rm --network none --entrypoint age-keygen claude-env` | – | not started (p4) |
| `schedule [--at HH:MM] [--keep N] [-o\|--output dir]` | systemd: `Scheduled (systemd user timer): daily at …`, the catch-up note, and a linger note if needed. cron: `Scheduled (cron): …`. Dies without a key or `CCENV_BACKUP_RECIPIENTS`, or on a bad `--at`/`--keep` | systemd: `~/.config/systemd/user/ccenv-backup.{service,timer}`. cron: the crontab line `M H * * * <self> backup --all --keep N [-o dir] >> <backups>/cron.log 2>&1  # ccenv-backup` | `systemctl --user show-environment` (to pick systemd); `systemctl --user daemon-reload`, `enable --now ccenv-backup.timer`; `loginctl show-user <user> -p Linger --value`; or `crontab -l`, `crontab -` | **legacy bug:** cron with no existing crontab (or only ccenv's line) installs an empty table and exits 1 without a message | not started (p4): planned divergence, unit `berth-backup`, switched over at cutover. Scenarios `schedule …` |
| `schedule status` | systemd: `list-timers` and the journal lines; cron: the line and log path; else `No backup schedule. …` | – | `systemctl --user list-timers …`, `journalctl --user -u ccenv-backup.service …`; or `crontab -l` | – | not started (p4) |
| `schedule run\|now` | `Ran. See: ccenv schedule status`; dies without a systemd timer | – | `systemctl --user start ccenv-backup.service` | – | not started (p4) |
| `schedule off\|--off` | `Backup schedule removed.` | unit files removed | `systemctl --user disable --now …`, `daemon-reload`; `crontab -l \| grep -v … \| crontab -` | – | not started (p4) |
| `restore <file\|-> [--as name] [--identity\|-i key] [--force] [--no-start] [--no-rehydrate]` | `Restoring '<org>' from <host> (<date>, <format>) as '<name>'`, port/Tailscale notes, `Restored to O`, then `ls`; dies (and changes nothing) on a wrong key or tampered file, on "not a ccenv backup (no manifest)", or on an existing org without `--force` | staged in `orgs/.restore-XXXXXX`, then `mv` to O (0700); `--force` moves the old org to `<backups>/.replaced/<name>-<date>`; ports reset (`setval 0`, then `next_port`) if taken; `BIND_ADDR=127.0.0.1` if Tailscale is down | `ensure_image`; `archive.sh extract <uid> <gid>` (`sshd/` stays 0:0, `*_key` 0600); `compose up -d --force-recreate`; `sleep 5`; `rehydrate` | `-` reads a plain or encrypted stream from stdin (the `migrate` contract). Format sniffed from the first byte | not started (p4) |
| `rehydrate <org>` | `== Rehydrating …`, `repo sync`, the mise and dependency steps | – | `need_up`; `docker exec -i -u node -w /workspace … bash -s` (script on stdin) | – | not started (p4) |
| `migrate <org> <[user@]host> [--as name] [--remote-dir dir]` | `== Installing claude-envs …` (first time), `== Streaming …`, and the stop-it-here note | – | `ssh <host> docker info`; `ssh <host> command -v ccenv …`; `rsync -a --exclude … <root>/ <host>:<dir>/`, `ssh <host> …/ccenv install`; `archive.sh create \| ssh <host> "<ccenv> restore - [--as]"` | frozen contract: a plain zstd tar on stdin; test all four Go/Bash pairings | not started (p4) |

## Internal contracts (no command of their own)

| Item | Behaviour | Status |
|---|---|---|
| `org.env` reader (`envval`) | `grep ^KEY= \| tail -1 \| cut -d= -f2-`: last match wins, raw value | done: `internal/org.Lookup`, checked against the real function (#8) |
| `org.env` writer (`setval`) | replaces every match or appends; rewrites in place (inode and mode kept) | done: `internal/org.Set`, `Orgs.Set` (#8) |
| `next_port` | max+1 over `"$ORGS"/*/org.env` (`cut -f2`, no `tail -1`) | done, with two divergences: no bash `integer expression expected` noise on stderr for a skipped file, and a leading-zero value is read as decimal (bash would read it as octal or fail) (#8) |
| repo canonical form (`repo_canon`, `canon`) | `docs/repo-policy.md` | done: one table for bash, node and Go (#9). The fixes are in `image/`, so legacy (through the `legacy/image` symlink) and berth share them |
| `resolve_bind` | `BIND_ADDR=tailscale` → `tailscale ip -4 \| head -1`, dies if that's empty; default 127.0.0.1 | not started (p2); planned fix: not for down/logs |
| compose invocation | `docker compose -f <root>/compose.yml --env-file O/org.env` with the env above; labels `com.docker.compose.project=claude-<org>` | not started (p2); the contract is pinned by `test/integration/compose_test.go` (#3) |
| `MANAGER` guard | legacy refuses `MANAGER=berth` orgs (`need_org`, `backup --all`); berth must refuse `MANAGER` missing or `ccenv` | not started (p2) |
| in-container contracts | `/config` file formats, the env vars the entrypoint reads (including `CCENV_ENV_KEYS`), `/run/firewall.status`, the log strings the host greps for (`blocked by organization policy`, `Capacity`), tmux session `main` | not started (`internal/contract`) |

## Legacy quirks found so far

Each has a harness scenario with the real behaviour, in `test/parity/scenarios_test.go`. For each
one, berth either mirrors it or records a divergence here when the command is ported.

1. **`repo ls` on an org that was never started exits 2** before its audit output: `qn=$(ls quarantine | wc -l)` under `pipefail`, and `init` doesn't create `quarantine/`.
2. **`repo policy <org> <mode>` exits 1 after succeeding** when the org is stopped (the function ends with `running "$org" && …`).
3. **`schedule` via cron, with no existing crontab (or only ccenv's own line), installs an empty crontab and exits 1 with no message**: `crontab -l | grep -v …` fails inside a `set -e` subshell before the job line is echoed. This is a real bug; berth should fix it and record the divergence.
4. **`info <org>` exits 1 with no output** when org.env has no `SSH_PORT` or `TTYD_PORT` (`sp=$(envval …)` under `set -e -o pipefail`).
5. `fw <org> show` and `repo ls <org>` create `firewall.txt` and `repos.txt` when they're missing, so reads have side effects.
6. `env … --no-restart` is only recognized as the 4th argument.

## Intentional divergences

| Where | Divergence | Why |
|---|---|---|
| all commands | the error prefix is `berth:` | the tool's name; normalized by the harness |
| global | `--version`, `--home`, `--read-only`, `$BERTH_HOME`, `config.yaml` | berth-only; the state root is explicit and empty by default, and read-only mode protects the live orgs (plan, decision 6) |
| global | no `-v` | reserved for a future verbose flag |
| `next_port` | no stderr noise for a skipped file; leading zeros are decimal | see Internal contracts |
| repo policy | stricter canonical form (for example non-default ports and dotless hosts are rejected) | `docs/repo-policy.md`. It applies to legacy too, through `image/` |
| ssh hosts | over SSH, a missing binary is exit 127 from the remote shell, not a start error | inherent to remote exec (#6) |
