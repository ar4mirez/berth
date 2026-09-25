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
| `completion` | the bash completion script | – | – | ccenv's script finds orgs through `readlink $(command -v ccenv)/../orgs` | **done, divergent by design** (#16): `berth completion [bash\|zsh\|fish\|powershell]` prints cobra's script (default bash). Orgs are completed from the state root (`--home`, `$BERTH_HOME`, config), with subcommands per position, for the commands berth has. The `fw allow` preset list comes with `fw allow` (p3) |
| `init <org>` | `Created O`, then the next steps (with the git public key); `ccenv: org name must be lowercase [a-z0-9-]` or `<org> already exists` | `O/{workspace,claude,ssh,sshd,mise,home-config,config/secrets}`: O, ssh, claude, home-config and config/secrets are 0700, the rest follow the umask; `ssh/id_ed25519{,.pub}`; `config/authorized_keys` (`~/.ssh/*.pub`, or empty); `config/secrets/ttyd_credential` 0600 `node:<32 alnum>`; `config/firewall.txt` (template); `org.env` 0600 (template) | `ssh-keygen -q -t ed25519 -N '' -C claude-<org>@<hostname> -f …`; `git config --global user.name/user.email`; `hostname` | `BIND_ADDR=tailscale` if `tailscale` is on PATH, else `127.0.0.1`. `SSH_PORT`/`TTYD_PORT` from `next_port` (2201/7701). No `quarantine/`: the first `compose` creates it | **done** (#20): scenarios `init …`. The org is berth's: `MANAGER=berth` is the first line of org.env (`TestBerthInitOwnsTheOrg`); `command -v tailscale` is asked on the host |
| `init --name <name>` / `--email <email>` | `ccenv: unknown flag <f>` for anything else | defaults from `git config --global` | | | **done** (#20). berth parses the arguments itself, as ccenv does (org first; `unknown flag --x`). A `--name` with no value prints bash's `$2: unbound variable` without bash's `<script>: line N:` prefix |
| `auth <org>` | `== Step 1/2 …`, then `token`, `== Step 2/2 …`, then `login` | as token + login | as token + login | interactive | **done** (#21): scenarios `auth …`; needs `MANAGER=berth` |
| `token <org>` | the browser hint; a hidden prompt `Paste CLAUDE_CODE_OAUTH_TOKEN for <org>:` if capture fails; `Token saved for <org>.`; dies unless the token is `sk-ant-*` | `setval CLAUDE_CODE_OAUTH_TOKEN` (in place, keeps inode and mode) | `docker exec -it -u node -w /workspace claude-<org> bash -c '… script … claude setup-token …'` (captured in `/dev/shm`, then deleted); if running, `compose up -d --force-recreate`, then `sleep 3` | needs the container up (without `--paste`) | **done** (#21): scenarios `token …`; needs `MANAGER=berth`. The capture file keeps its wire name, `/dev/shm/.ccenv-token`. Quirk, mirrored: a pasted line without a trailing newline ends the command with exit 1, unsaved (`read` under set -e). A failed recreate ends it with compose's exit code, silently |
| `token --paste` / `--no-restart` | `--paste`: skip the capture and read from stdin. `--no-restart`: no recreate | | | | **done** (#21). Parsed as ccenv does (anywhere; the other word is the org) |
| `login <org>` | the browser hint, the Remote Control status, `Signed in as: …`, and a WARNING if another org uses the same Claude organization (unless `SHARED_ACCOUNT=1`) | – | `docker exec` logout plus `rm .credentials.json`; `docker exec -it … claude auth login`; `pkill -f "claude remote-control"`; polls up to 20×2s | interactive | **done** (#21): scenarios `login …`. The account check evaluates ccenv's jq program itself (`select(.loggedIn) | …`), and like ccenv warns on grep's error for a running dir without org.env. It ends with `remote status`'s exit code (quirk 7) |
| `logout <org> [--all]` | `Logged out <org> …`; with `--all`, also `Token removed too.` and `Restarted claude-<org>.`; always exit 0 | stopped: `rm O/claude/.credentials.json`. `--all`: `setval CLAUDE_CODE_OAUTH_TOKEN ""` | running: `docker exec` logout, `pkill`. `--all` and running: `compose up -d --force-recreate` | – | **done** (#21): scenarios `logout …`; needs `MANAGER=berth`. With `--all` and a running org, a failed recreate ends the command with compose's exit code (inside `running && { … }`, set -e still applies) |
| `gh-login <org> [--force]` | `gh already signed in as X` (exit 0) unless `--force`; device-code instructions; dies if sign-in didn't complete | `O/home-config/gh` (through the container) | `docker exec -u node … gh api user -q .login`; `docker exec -it -e BROWSER=true … gh auth login --hostname github.com --git-protocol ssh --skip-ssh-key --web --scopes read:org,repo,workflow` | interactive | **done** (#21): scenarios `gh-login …`; needs `MANAGER=berth` |
| `whoami [org...]` | table `ORG TOKEN GITHUB (gh) REMOTE-CONTROL LOGIN (account)`; `(container down)` for stopped orgs. With no args, every org dir with an org.env; named orgs in the given order | – | running: `docker exec -u node … sh -c 'env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status 2>/dev/null; true'` piped to jq on the host, and `docker exec -u node … sh -c 'gh api user -q .login 2>/dev/null; true'` | an unknown org dies mid-table (rows before it are printed); a docker failure appends `not logged in` (pipefail) | **done** (#15): scenarios `whoami …`. berth evaluates the jq program itself (jq 1.6 semantics), so it doesn't need jq on the host; like the other read commands it accepts berth-owned orgs |
| `up <org>` | compose output; then the last 5 lines of `docker logs claude-<org> 2>&1`; ends with docker logs' exit code (a compose failure ends it earlier with compose's) | `O/home-config`, `O/quarantine` 0700 if missing | `compose up -d --build --force-recreate`; `sleep 3`; `docker logs claude-<org>` | – | **done** (#19): scenarios `up …`. berth needs `MANAGER=berth` and builds and runs its own image (see `build`) |
| `down <org>` | compose output; ends with docker's exit code | as `compose` | `compose down` | with Tailscale down: prints the `resolve_bind` error, then runs compose anyway with `BIND_ADDR=` empty, exit 0 | **done** (#18): scenarios `down`, `down, …`. berth needs `MANAGER=berth` |
| `restart <org>` | compose output; ends with docker's exit code | as `compose` | `compose up -d --force-recreate` | – | **done** (#18): scenarios `restart …`. berth needs `MANAGER=berth` |
| `build [docker-build-args...]` (e.g. `--no-cache`) | docker build output; docker's exit code | – | `docker build -t claude-env --build-arg USER_UID=$(id -u) --build-arg USER_GID=$(id -g) "$@" <root>/image` | – | **done, divergent by design** (#19): berth tags `berth/claude-env:<12-hex content hash>` and builds from its embedded `image/`, written to `<state>/berth/image` (0755 where git has it). `BERTH_IMAGE_DIR` overrides the dir (tag = hash of that dir). berth never tags `claude-env`. Arguments pass through (`DisableFlagParsing`) |

## Use

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `ls` | table `ORG STATE SSH TTYD TOKEN REMOTE` (`%-14s %-6s %-6s %-6s %-8s %s`), every dir with an `org.env` (berth-owned ones too); REMOTE is `-`, `off`, `login-needed`, `on`, `blocked-by-org` or `restarting` | – | `docker ps` per org; running orgs: `docker exec` `test -f …/.credentials.json`, `pgrep -f "claude remote-control"`, and the remote-control.log tail | a failing `docker ps` counts as not running (pipefail) | **done** (#14): scenarios `ls`, `ls, …` |
| `info <org>` | the connection sheet (Remote Control URL, SSH, browser terminal, VS Code, ssh config, git public key); a missing public key prints cat's error and an empty key | – | `tailscale ip -4` (if `BIND_ADDR=tailscale`), `hostname` (if `0.0.0.0` and no `CCENV_HOST`); `docker ps` twice; running: the `rc_url` grep | quirks, mirrored: exit 1 with no output when org.env has no `SSH_PORT`/`TTYD_PORT`; with Tailscale down, the `resolve_bind` error and then an empty host, exit 0 | **done** (#14): scenarios `info …`. berth also honours `BERTH_HOST` (before `CCENV_HOST`) |
| `attach <org>` | tmux | – | `docker ps`; `docker exec -it -u node claude-<org> tmux new-session -A -s main -c /workspace` | TTY (the terminal is handed to docker); exit code passed through; `claude-<org> is not running (… up <org>)` if down | **done** (#18): scenarios `attach …`. berth needs `MANAGER=berth` |
| `shell <org>` | bash | – | `docker ps`; `docker exec -it -u node -w /workspace claude-<org> bash -l` | TTY; exit code passed through | **done** (#18): scenario `shell, exit code`. berth needs `MANAGER=berth` |
| `claude <org> [args...]` | claude | – | `docker ps`; `docker exec -it -u node -w /workspace claude-<org> claude "$@"` | TTY; every argument after the org goes to claude, flags included (`DisableFlagParsing`) | **done** (#18): scenarios `claude …`. berth's own flags go before the command (`berth --read-only claude …`); `TraverseChildren` makes sure they're parsed, not passed to claude |
| `run <org> "<prompt>" [args...]` | claude's output; `ccenv: missing prompt` (after the up check) | – | `docker ps`; `docker exec -i -u node -w /workspace claude-<org> claude -p "<prompt>" "$@"` | stdin passed through (`-i`, no TTY); exit code passed through; `DisableFlagParsing` | **done** (#18): scenarios `run …`. As for `claude` |
| `clone <org> <owner/repo\|url> [...]` | as `repo add` | as `repo add` | as `repo add` | alias of `repo add` | not started (p3) |
| `logs <org>` | `compose logs -f`; ends with docker's exit code | as `compose` (creates `home-config`/`quarantine` 0700 if missing) | `compose logs -f` | follows until interrupted; with Tailscale down, warns and runs compose anyway | **done** (#15): scenarios `logs …`. Under `--read-only`, berth doesn't create the two dirs (only `up` needs them) |
| `env <org> [ls\|list]` | the names in `CCENV_ENV_KEYS`, with `(listed but missing)` if the line is gone; `(no custom variables; …)` | – | – | – | **done** (#20): scenarios `env ls …` |
| `env <org> set <KEY> [--no-restart]` | `set: KEY`, then `claude-<org> recreated; …` or `applies on: ccenv restart <org>`. Dies on a bad or reserved key (`… is managed by ccenv`), an empty value, or a value containing `'` | `setval KEY "'<value>'"`; `setval CCENV_ENV_KEYS` | running and no `--no-restart`: `compose up -d --force-recreate` | value from a hidden prompt on a TTY, else one line of stdin; the entrypoint snapshots `CCENV_ENV_KEYS` for SSH sessions | **done** (#20): scenarios `env set …`; needs `MANAGER=berth`. berth also reserves `CLAUDE_ENV_IMAGE` and `CLAUDE_ENV_IMAGE_DIR`, the variables it passes to compose, and its message says `managed by berth` |
| `env <org> unset\|rm <KEY> [--no-restart]` | `removed: KEY` …; dies with `KEY is not set for <org>` | every `KEY=` line removed (awk, `cat tmp > f`); `setval CCENV_ENV_KEYS` | as set | `--no-restart` is recognized only as the 4th argument | **done** (#20): scenarios `env unset …`, `env rm …`; needs `MANAGER=berth` |
| `remote <org> [status]` | `Remote Control: running` with the URL, `Or: …` and the last `Capacity` line; or `not logged in`, `BLOCKED by the Claude organization's policy …`, `restarting` | – | `need_up`; `docker exec` `test -f`, `pgrep`, grep/sed of remote-control.log | – | **done** (#20): scenarios `remote status …`. Quirk, mirrored: with no `Capacity` line in the log, status exits 1 after the URL (grep under pipefail + set -e) |
| `remote <org> logs` | the last 40 log lines, ANSI stripped | – | `docker exec … sed … remote-control.log` | – | **done** (#20): scenario `remote logs` |
| `remote <org> restart` | `Restarting; back in ~5s.` | – | `docker exec claude-<org> pkill -f "claude remote-control"` | issue #7: a no-op while the service is in its blocked-by-policy backoff | **done** (#20): scenario `remote restart`; needs `MANAGER=berth`, refused under `--read-only`. Issue #7 is unchanged: it's a Remote Control matter |
| `password <org> [show]` | `user: node` and `pass: …`; dies if the file is empty | – | – | – | **done** (#20): scenarios `password …` |
| `password <org> rotate` | `Rotated; …` and `pass: …` | `O/config/secrets/ttyd_credential` rewritten `node:<32 alnum>` (umask 077 → 0600) | running: `docker exec claude-<org> pkill -x ttyd` | – | **done** (#20): scenarios `password rotate …` (the new password masked); needs `MANAGER=berth`, refused under `--read-only` |

## Firewall

`fw <org>` writes the `config/firewall.txt` template first if it's missing, for every subcommand
(`show` included). Changes are applied live (`fw_apply`): if running, `docker exec claude-<org>
init-firewall.sh apply`; otherwise `(saved; applies on: ccenv up <org>)`.

| Item | stdout / stderr / exit | Files (modes) | Docker / host calls | Other side effects | Status |
|---|---|---|---|---|---|
| `fw <org> [show]` | the file path, the entries (`grep -vE '^\s*(#\|$)'`, indented two spaces), and `live: …` if running (`… \|\| echo unknown`) | the template (mode 0666 minus the umask) if missing | running: `docker exec … cat /run/firewall.status` | quirk, mirrored: a file with no entries ends `show` with exit 1 right after the path (grep selects nothing, pipefail + set -e) | **done** (#15): scenarios `fw show …`. Under `--read-only`, berth shows the template without writing it |
| `fw <org> allow\|add <entry>...` | `allowed: X` / `already allowed: X`; usage error with no entries | appended with `>>` (inode and mode kept) | `fw_apply` | URLs are reduced to the host (`https://x:443/p` → `x`) | **done** (#22): scenarios `fw allow …`; needs `MANAGER=berth`. Appends raw, as `echo >> f` does: a file without a final newline gets the entry joined to its last line |
| `fw <org> deny\|remove\|rm <entry>...` | `removed: X` / `not in list: X` | rewritten (`cat tmp > f`) | `fw_apply` | exact match only, with no URL reduction (asymmetric with allow) | **done** (#22): scenarios `fw deny …`; needs `MANAGER=berth`. Quirk, mirrored: removing the last remaining line exits 1 and leaves the file unchanged |
| `fw <org> on\|off` | – | every `mode on/off` line removed, `mode <x>` appended | `fw_apply` | – | **done** (#22): scenarios `fw on …`, `fw off`; needs `MANAGER=berth` |
| `fw <org> edit` | – | edited by `$EDITOR` (default `vi`) | `fw_apply` | TTY | **done** (#22): scenarios `fw edit …` (`$EDITOR`, default `vi`, with the terminal); needs `MANAGER=berth` |
| `fw <org> reload` | – | – | `fw_apply` | – | **done** (#22): scenarios `fw reload …`; needs `MANAGER=berth` |
| `fw <org> presets` | the preset list; ends with docker's exit code | the template if missing | running: `docker exec claude-<org> init-firewall.sh presets`; else `docker run --rm --entrypoint init-firewall.sh claude-env presets` | – | **done** (#15): scenarios `fw presets …`. The image is still `claude-env` until berth builds its own (p2) |
| `fw <org> test [host\|url...]` | `ALLOWED  <url>` / `blocked  <url>`; default api.anthropic.com, github.com, example.com | – | `need_up`; `docker exec -u node … curl -s -o /dev/null --max-time 6 <url>` | – | **done** (#22): scenarios `fw test …`. It only probes, so it counts as reading: it works on any org and under `--read-only` |

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
| `repo ls\|list <org>` | table `DIR REPO BRANCH STATUS` (`cloned`, `cloned, N changed`, `cloned (container down)`, `MISSING (… repo sync …)`; REPO `-` for a URL with no canonical form, `(local only, not published)`), `(no repos registered; …)`, then `audit --quiet` | `repos.txt` with its header if missing (mode 0666 minus the umask) | per entry: `docker ps`, and if running `docker exec claude-<org> test -d /workspace/<dir>/.git`, then `repo_dirty` (`docker exec -u node -w /workspace/<dir> … git branch/status …`, first line, `? ?` on failure) | quirk, mirrored: exit 2 before the audit output on an org never started (no `quarantine/`) | **done** (#16): scenarios `repo ls …`, `repo list, stopped`. Under `--read-only` a missing `repos.txt` is treated as empty, not written |
| `repo rm\|remove <org> <dir> [--delete]` | `Unregistered …`, then `Deleted …` or the quarantine note | `repos.txt` rewritten | running and `--delete`: `docker exec -u node … rm -rf /workspace/<dir>` | – | not started (p3) |
| `repo adopt <org> <dir>...\|--all` | `Registered …` / `Restored … from quarantine …` / `skip …` | `repos.txt` appended; quarantine → workspace `mv` | `git -C <dir> remote get-url origin` (host) | the registered URL is `repo_url(origin)`: an origin with no canonical form registers `git@:.git` | not started (p3) |
| `repo sync <org>` | `Cloned d` / `FAILED d` / `SKIP d …` / `All registered repos are present.` | – | `need_up`; `docker exec … test -e`, `git clone` | – | not started (p3) |
| `repo audit <org> [--quiet]` | `Not allowed in /workspace (policy: …): …` and the adopt hint; `Workspace clean …` unless `--quiet`; `Quarantined (…)` with the visible entries and the adopt hint | `repos.txt` if missing | – | workspace globs `*` then `.[!.]*`, each sorted, broken symlinks and `.claude` skipped; the policy shows as `enforce` when empty and as nothing when the key is missing; exit 2 without `quarantine/` | **done** (#16): scenarios `repo audit …`. berth takes `--quiet` anywhere (a flag), ccenv only as the 3rd argument |
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
| `resolve_bind` | `BIND_ADDR=tailscale` → `tailscale ip -4 2>/dev/null \| head -1`; empty → the error `BIND_ADDR=tailscale but tailscale is not up on this host`; unset → 127.0.0.1. Every caller runs it inside `$( )`, where bash turns `set -e` off, so the error is printed and the caller carries on with an empty address (compose, info) | done (#14): `app.resolveBind`/`bindOrWarn`, mirrored, checked by `info, tailscale down` |
| compose invocation | `docker compose -f <root>/compose.yml --env-file O/org.env` with the env above; labels `com.docker.compose.project=claude-<org>` | done (#15, #19): `app.compose`. berth's compose.yml is its embedded copy in `<state>/berth/`, and berth also sets `CLAUDE_ENV_IMAGE=berth/claude-env`, `IMAGE_TAG=<hash>` and `CLAUDE_ENV_IMAGE_DIR`. Under `--read-only` it uses that copy if it's already there, else the state root's `compose.yml` (a ccenv checkout's), without the image variables. `test/integration` checks that the variables resolve, and that without them compose.yml is unchanged for ccenv |
| `MANAGER` guard | legacy refuses `MANAGER=berth` orgs (`need_org`, `backup --all`) | **done for berth's side** (#18): berth's writing commands refuse an org whose `MANAGER` isn't `berth` (missing means ccenv): `org 'acme' is managed by ccenv (MANAGER=…); use: ccenv ... acme`, before any call or write (`TestBerthRefusesLegacyOrgs`). Read commands accept either (see Intentional divergences). The harness gives the berth side's fixtures `MANAGER=berth` as the first line of each org.env and drops it before comparing |
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
7. **`remote <org> status` exits 1** after printing the URL when the Remote Control log has no `Capacity` line (`rc_log | grep -a Capacity | …` under pipefail and set -e).
8. **`token <org> --paste` exits 1 without saving** when the pasted input doesn't end with a newline (`read` fails under set -e).
9. **`fw <org> deny <entry>` exits 1 and changes nothing** when that entry is all that's left in the file (`grep -vxF` selects nothing under set -e).

## To port to the legacy checkout at cutover

berth's `image/` and `compose.yml` changes reach the live orgs only when they are copied to the
legacy checkout on the host. Per the plan, and by the owner's decision, that happens at cutover
(phase 5), not before.

| PR | Files | What |
|---|---|---|
| #9 | `image/repo-policy.sh`, `image/repo-guard.js`, the git guard | canonical repo form, and the reader fixes |
| #19 | `compose.yml` | `image:` and `build.context` take `CLAUDE_ENV_IMAGE`/`CLAUDE_ENV_IMAGE_DIR`, with ccenv's old values as defaults |

## Intentional divergences

| Where | Divergence | Why |
|---|---|---|
| all commands | the error prefix is `berth:` | the tool's name; normalized by the harness |
| global | `--version`, `--home`, `--read-only`, `$BERTH_HOME`, `config.yaml` | berth-only; the state root is explicit and empty by default, and read-only mode protects the live orgs (plan, decision 6) |
| global | no `-v` | reserved for a future verbose flag |
| command hints | berth's messages name itself where ccenv's name ccenv (`run: berth login acme`, `(berth up acme)`); the harness normalizes `ccenv|berth <subcommand>` | telling someone to run ccenv from berth would be wrong. Wire names (`.ccenv-manifest.json`, `CCENV_*`) are unchanged |
| read-only commands | `ls`, `info` and the other phase 1 commands accept orgs of either `MANAGER`, where legacy `need_org` refuses `MANAGER=berth` | berth must read the live, legacy-owned orgs before cutover (`berth ls --home <legacy-checkout> --read-only`); writing commands will refuse orgs berth doesn't own (p2) |
| org names | a name that isn't `[a-z0-9][a-z0-9-]*` is reported as unknown without touching the filesystem | ccenv would build a path from it (a name with a slash or dots); the message is the same |
| flags | berth rejects unknown flags (`berth: unknown flag: --x`), and `-h`/`--help` after a command show its help | ccenv ignored or misread them as positional arguments |
| org order | berth lists orgs in byte order; ccenv's glob follows the locale's collation | the same for `[a-z0-9-]` names in the C locale; under e.g. en_US, names differing only by `-` can sort differently |
| image | berth builds and runs `berth/claude-env:<content hash>` from its embedded `image/` in `<state>/berth/`, and refuses a `<state>/berth` that isn't its own (no stamp) | plan, decisions 5 and 8: the tools never share or overwrite an image, and berth never writes into a legacy checkout's own `image/` or `compose.yml` |
| writing subcommands | `password rotate`, `env set/unset/rm` and `remote restart` need `MANAGER=berth` and are refused under `--read-only`; `show`, `ls` and `status`/`logs` of the same commands still work on any org | the command reads or writes depending on its subcommand |
| exec commands | `attach`, `shell`, `claude` and `run` count as writing: they need `MANAGER=berth` and are refused under `--read-only` | they act as the org inside its container; `--read-only` must mean berth can't change a live org in any way |
| docker reads | phase 1 reads go through the docker CLI with ccenv's argv, not the Engine API | identical calls for the harness; the Engine API client (`host.Engine`) stays available |
| unported subcommands | `berth fw <org> allow` and `berth repo add <org> …` (and the other writing `fw`/`repo` subcommands) fail with `… is not in berth yet (phase 3); use ccenv for now`, before any side effect | until phase 3; ccenv would first write the template or `repos.txt` |
| `--read-only` reads | `logs` doesn't create `home-config`/`quarantine`, `fw show`/`presets` don't write a missing template, and `repo ls`/`audit` don't write a missing `repos.txt` | read-only means no writes; nothing else changes (checked by `TestParityReadOnly`) |
| `next_port` | no stderr noise for a skipped file; leading zeros are decimal | see Internal contracts |
| repo policy | stricter canonical form (for example non-default ports and dotless hosts are rejected) | `docs/repo-policy.md`. It applies to legacy too, through `image/` |
| ssh hosts | over SSH, a missing binary is exit 127 from the remote shell, not a start error | inherent to remote exec (#6) |
