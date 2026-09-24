# berth: Go successor to ccenv, built by dogfooding (revised after adversarial review)

## Context
`ccenv` (`<legacy-checkout>/ccenv`) is a 1,064-line Bash script. It orchestrates one hardened Claude Code container per org and currently manages three live orgs: the three live orgs. The next features are remote environments on on-prem and cloud VMs, which Bash can't carry. We will rewrite the host CLI in **Go** as **`berth`**, in a **new public repo, `ar4mirez/berth`**. It must reach 100% parity before any cutover, and the work happens *from inside a ccenv container*, so the project is dogfooded from day 0.

**Naming conventions.** The binary is `berth`. Host-side names use the `BERTH_` prefix: `BERTH_HOME`, `~/.config/berth/`, the `berth-backup` unit, the `berth.manager` label and the `berth/claude-env:<hash>` image. `CCENV_*` backup env vars are still accepted as aliases.

**Wire contracts keep the ccenv names**, because old backups and existing containers depend on them: `.ccenv-manifest.json`, `.ccenv-format`, the org.env keys, and the container env vars.

**Coexistence.** The two binaries have different names, so `ccenv` (legacy) and `berth` run side by side with no rename. At the end, `berth install --alias ccenv` is optional.

Three adversarial reviews (parity, security/remote, dogfooding) found 7+ blockers in v1 of this plan. Everything below includes their fixes.

## Core decisions (changed from v1)
1. **Go.** Keep this unchanged. The reasons still hold: native Docker, SSH and SFTP libraries, cloud SDKs, a single static binary, and cross-compilation.
2. **Host = FS + Exec + Docker + Facts, not "a Docker endpoint".** Every command writes host files (`org.env`, `firewall.txt`, `repos.txt`, secrets). Bind mounts resolve on the host that runs the container, so a remote host needs remote file access.
   - Interface: `Host{FS(Read/Write/Chmod/Lock, atomic), Exec(cmd, tty), Docker(), Facts(uid, arch, tailscale IP, used ports)}`.
   - Implement `local` **and** `ssh` (`x/crypto/ssh` + `pkg/sftp`, with berth's own known_hosts) **in phase 0**.
   - A linter rule forbids direct `os.*` calls on org state outside `host/local`.
   - The interface works at the level of operations, so a future `berthd` agent can implement it without a rewrite.
3. **Keep shelling out to `docker compose` (through `Host.Exec`) until after parity.** The live containers carry compose project, network and labels, and the legacy `down`/`up` depend on them.
   - Go runs `docker compose -f <state>/compose.yml --env-file org.env` with exactly the same environment the Bash script sets (`ccenv:35-37`).
   - Use the Engine API only for reads: ps, inspect, exec status.
   - Interactive commands (`attach`, `shell`, `claude`, `login`, `gh-login`, `token`) exec `docker exec -it` / `ssh -t` with inherited stdio and the exit code propagated. No pty reimplementation.
4. **The backup engine stays in the root container (`image/archive.sh`).** `sshd/*_key` files are owned by root with mode 0600, so the user process can't read them.
   - Go only orchestrates and streams the backup.
   - Passphrase backups stay gpg (AES256, S2K), for compatibility.
   - Remote hosts only ever receive age *recipients*. `backup.key` never leaves the operator's machine.
5. **berth owns `image/` and `compose.yml` from day 0.** It is a fresh repo with no history imported. It was seeded by *copying* `image/`, `compose.yml` and the legacy script (`legacy/ccenv`, with `legacy/image` and `legacy/compose.yml` symlinked so it runs unmodified) from claude-envs `legacy-v1`, with real org names scrubbed.
   - The private claude-envs repo is frozen at tag `legacy-v1` as the running copy on the host. Hot-fixes are ported by hand in both directions, and `scripts/drift.sh` (run on the host) flags divergence.
   - `go:embed` is a fallback only. The binary writes `image/` to `<state>/image/`, stamped with a content hash; `BERTH_IMAGE_DIR` overrides it.
6. **State root is explicit.** Resolution order: `--home` > `$BERTH_HOME` > `~/.config/berth/config.yaml` > `~/.local/share/berth`.
   - The default location is **empty on purpose**, so berth can't touch the live orgs by accident.
   - At cutover, set `home: <legacy-checkout>`. Never move `orgs/`: bind paths are absolute.
7. **Tenancy boundary = a host (VM), or a Linux user with rootless dockerd.** Never a shared rootful daemon. Anyone in the docker group can `docker inspect` tokens. Record this rule in the docs now.
8. **Rules for coexisting with legacy.**
   - `MANAGER=ccenv|berth` in `org.env`, with a missing value meaning `ccenv`. Each tool refuses orgs it doesn't own. berth sets the `com.docker.compose.project` labels so legacy `ccenv down` can still recover an org.
   - The image is tagged `berth/claude-env:<hash>`. berth never touches `claude-env:latest`.
   - The berth schedule unit is `berth-backup`. At cutover, disable legacy `ccenv-backup` in the same step, never both at once.
   - `berth` is installed on the host from CI release artifacts only, never from a workspace build.

## Deferred to post-parity "v2 hardening" (listed so we don't lose them)
- **Tokens as files, not env vars.** They are currently visible in `docker inspect`. This requires changing `entrypoint.sh`.
- **Runtime UID remap in the entrypoint.** Today the UID is a build arg, which means one image per user. Also: pin all versions and checksums in the Dockerfile.
- **Multi-arch GHCR image**, signed with cosign and pinned by digest per release. An on-host build stays as the offline fallback.
- **Replace compose with the Engine API**, as its own phase with a label-compatibility test. Keep the per-org network. Add a `DOCKER-USER` drop for the bridge gateway and 169.254.169.254 on remote hosts.
- **Remote host commands:** `host add|ls|rm|rotate-access`, `org@host` addressing, a host-side lock around read-modify-write and port allocation, an active-host lease (to prevent two Remote Control instances), and schedules that run on the host (a signed berth binary pushed there with recipients only).
- **Cloud providers**, one first (Hetzner or DO):
  - use the provider SDK behind a build tag;
  - tag every resource and reconcile by tag;
  - cloud-init installs a pinned Docker and joins Tailscale with a single-use tagged auth key;
  - public ingress is closed;
  - the host key is pre-generated and pinned;
  - IMDSv2 with hop limit 1, and no instance role.
- **Supply chain:** goreleaser + cosign + SLSA, govulncheck, Renovate, and the `moby/moby/client` split module. Self-update only once releases are signed.

## Parity work items (from the parity review; each gets a row in `PARITY.md`)
- **`org.env` handling:**
  - a raw reader that mirrors `envval` (`grep|tail -1|cut`, no unquoting);
  - a writer that mirrors `setval` (replaces every match or appends, rewrites in place, keeps inode and mode 0600);
  - `next_port` = max+1 (restore zeroes itself first);
  - `resolve_bind` tailscale behaviour, with the fix: don't resolve for `down`/`logs`.
- **repo-policy:** three implementations already disagree (shell `repo_canon`, JS `canon`, future Go) on slashes, port stripping and Unicode lowercasing. **First** write a canonical spec, then fix the `.sh` and `.js` versions. One `testdata/canon.tsv` runs through bash, node and Go.
- **In-container contracts** in `internal/contract`:
  - `/config` file formats;
  - the env vars the entrypoint reads;
  - `/run/firewall.status`;
  - the log strings the host greps for (`blocked by organization policy`, `Capacity`);
  - the tmux session `main`.
- **CLI grammar:**
  - org-first groups (`fw|remote|password <org> <sub>`) next to sub-first `repo <sub> <org>`;
  - aliases;
  - `schedule` verbs;
  - passthrough with `DisableFlagParsing` for `claude`, `run` and `build`;
  - `-` as stdin for `restore`;
  - `berth: msg` on stderr, exit 1. The prefix is the only intended difference; the harness normalizes it;
  - no arguments prints help and exits 0, an unknown command exits 1.
- **Backup:** the key file has the age-keygen 3-line format (grepped for `age1…`); passphrases are read from `/dev/tty`; `backup -o -` keeps stdout clean; the prune regex; the `.restore-XXXX` staging; `--force` moves the old org to `.replaced/`; restore runs `up`, sleeps 5, then rehydrates.
- **`migrate`:** `restore - [--as]` reading a plain zstd tar from stdin is a frozen contract. Test all four Go/Bash pairings.
- **Install and completion:** the completion must not rely on `readlink $(command -v ccenv)/../orgs`. Use cobra `ValidArgsFunction` over the state root, and include the preset list.
- **Smaller items** (listed in PARITY.md): init scaffold and modes, fw allow/deny asymmetry, repo adopt/audit glob behaviour, whoami/ls account columns, token `sk-ant-*` via `/dev/shm`, `ensure_image` checks, `human()` without numfmt.

## Dogfooding setup
- **Docker access for the dev container: none.** Mounting `docker.sock` or privileged DinD would give the dev container host root, including access to the live orgs' tokens. Sysbox would also bypass the OUTPUT-only firewall. Testing is split into three tiers:
  1. **In the container:** `go build/vet/test`, golangci-lint, `goreleaser --snapshot`, and the shell/node/Go repo-policy agreement test.
  2. **GitHub Actions** for everything that needs real Docker. Claude runs `gh workflow run` + `gh run watch`.
  3. *(Deferred)* A disposable tailnet VM as an SSH host, for faster feedback.
- **GitHub access:**
  - `ccenv gh-login` (full account token, like the other orgs). **Accepted tradeoff:** the token reaches every repo the account can, and it can approve releases, so Claude may cut and approve releases. Revisit (with a fine-grained PAT for this repo only, and a `release` environment gated on the owner) before berth is used by anyone else;
  - a write-enabled **deploy key** for git;
  - `main` ruleset: no deletion, no force-push, changes via PR (the admin can bypass). Not a release gate while gh-login is in use.
- **Firewall additions:** `vuln.go.dev`, `pkg.go.dev`, and the Actions log hosts: `results-receiver`, `pipelines.actions.githubusercontent.com`, and `productionresultssa0..19.blob.core.windows.net`.
- **`mise.toml`:** `GOPROXY=https://proxy.golang.org`, `GOTOOLCHAIN=local`, `GOFLAGS=-mod=readonly`. The `direct` fallback would hit `git-ssh-guard`.
- **Safety:**
  - live orgs are read-only for berth, enforced by a `--read-only` flag;
  - mutating tests only run on `t-*` orgs in `~/.local/share/berth-test`, using ports 2290–99 / 7790–99 on 127.0.0.1;
  - the port allocator is host-wide (`docker ps` + all homes);
  - fixtures are synthetic only, never real `org.env` files (they contain tokens);
  - lifecycle tests on real data use restored clones (`restore --as t-x --no-start`, token cleared, `REMOTE_CONTROL=0`).
- **The dev org stays on ccenv and is cut over last.** Recovery: `docker rm -f claude-ar4mirez && ccenv up ar4mirez`. Claude on the host is the break-glass.

## Day 0 bootstrap (host, run with the legacy `ccenv`)
```bash
cd <legacy-checkout> && git status --short && ccenv backup --all
# small legacy patch: ORGS="${CCENV_ORGS:-$ROOT/orgs}", BACKUPS likewise; refuse orgs with MANAGER=berth
git commit -am "legacy: overridable state root, manager guard" && git tag legacy-v1 && git push --follow-tags
# (done) berth created fresh on the host, not from claude-envs history; files copied and scrubbed
gh repo create ar4mirez/berth --public --source . --push -d "Isolated Claude Code environments per org — local, on-prem, cloud"
#   ccenv's legacy-v1 also fixed a backup bug for orgs without a mise config (archive.sh, jq + pipefail)
ccenv init ar4mirez --email <email>             # edit org.env: SHARED_ACCOUNT=1 (set BEFORE auth); then ccenv gh-login ar4mirez after up,
                                                         #   keep REMOTE_CONTROL=1 (default), REMOTE_CAPACITY=8
gh repo deploy-key add orgs/ar4mirez/ssh/id_ed25519.pub -R ar4mirez/berth --allow-write --title claude-ar4mirez
ccenv fw ar4mirez allow vuln.go.dev pkg.go.dev results-receiver.actions.githubusercontent.com pipelines.actions.githubusercontent.com productionresultssa{0..19}.blob.core.windows.net
ccenv up ar4mirez
ccenv auth ar4mirez      # token + Remote Control login using the shared Claude account credentials:
                         #   use one private window for both steps, and pick the shared organization on the org screen
ccenv whoami ar4mirez <shared-org>        # both rows show the same Claude organization (expected; SHARED_ACCOUNT=1 silences the warning)
ccenv remote ar4mirez status        # service running, environment URL printed, not "blocked by organization policy"
ccenv repo add ar4mirez ar4mirez/berth
ccenv fw ar4mirez test proxy.golang.org sum.golang.org vuln.go.dev
# ruleset on main + v* tags: require PR, CI green, owner review
```
**Remote Control for the dev org.** The `ar4mirez` environment appears in claude.ai/code next to the other org's environment, because it uses the same account. The `--name ar4mirez` prefix keeps them apart, and each container has its own login in `orgs/<org>/claude/`. The main dogfooding loop is: open the ar4mirez environment in claude.ai/code or the app, start a **New session** (it runs in `/workspace`), and work on `/workspace/berth`. `ccenv attach ar4mirez` is still available.

**Parity requirement.** When berth takes over `ar4mirez` (last in the cutover order), `berth remote ar4mirez status` must show the same environment URL, and the saved login must survive the recreate.

## Phases (each ends with its PARITY.md rows green in CI)
0. **Foundations** (in the container): Go module, cobra skeleton, the `Host` interface with `local` + `ssh`, state-root resolution, CI (unit + integration on real Docker), and goreleaser snapshots. Also the canonical repo-policy spec with the `.sh`/`.js` fixes and the shared `canon.tsv`, and the parity harness: fake `docker`/`tailscale`/`gh`/`ssh`/`systemctl`/`crontab` on PATH that log argv+env, used to diff legacy vs Go transcripts, stdout/stderr, exit codes, and the resulting file tree and modes.
1. **Read-only commands:** `ls`, `info`, `whoami`, `logs`, `repo ls`, `fw show/presets`, completion. Once these pass, the first real use is `berth ls --home <legacy-checkout> --read-only`.
2. **Lifecycle:** init, up, down, restart, build, attach, shell, claude, run, auth, token, login, logout, gh-login, password, remote.
3. **Repos and firewall:** all `repo *` and `fw *` subcommands.
4. **Backup, restore, migrate:** backup, keygen, restore, rehydrate, schedule, migrate. Run the full backup matrix: {legacy, go} creator × {legacy, go} restorer × {none, gpg, age, age-ssh} × {file, stdin}. Extracted trees must be equal, including `sshd/` owned by 0:0 with mode 600.
5. **Cutover, one org at a time:** back up → `MANAGER=berth` → verify. Order: `t-*` clones, then the live orgs (smallest first), and `ar4mirez` last. Then swap `ccenv-backup` for `berth-backup`, optionally run `berth install --alias ccenv`, and archive claude-envs.
6. **v2 hardening, then remote hosts, then the first cloud provider** (see the deferred list).

## Verification
- **CI:** unit tests, golden tests (the org.env/firewall/repos editors, the init scaffold, schedule unit and cron text, completion), the three-language canon test, transcript-parity tests against the legacy script, and integration tests on a real daemon (legacy `up` → Go `down`/`restart`/`logs` and the reverse; no orphan containers or networks). The `docker inspect` comparison is limited to Binds, PortBindings, Memory, NanoCpus, CapAdd, RestartPolicy, Env, Hostname, Image and the compose project label.
- **Also in CI:** the backup matrix. **Image drift:** until cutover the live orgs are built from claude-envs' `image/`, not berth's. Any berth change to `image/` is also applied to claude-envs by hand, on the host, and `scripts/drift.sh` diffs the two checkouts.
- **Before cutover on the host:** `ls`, `info`, `whoami` and `repo ls` produce identical output under both binaries for every live org. The systemd unit has been rewritten, and a scheduled backup has run successfully.

## Decisions made
- **Name:** `berth`. **Repo:** `ar4mirez/berth`, **public**. Rulesets are free on public repos.
- **Seeding:** fresh repo; `image/`, `compose.yml` and `legacy/ccenv` were copied from claude-envs `legacy-v1`. Real org and client names were replaced with acme/globex/initech. gitleaks found nothing in claude-envs history, but none of that history is imported anyway.
- **Dev org:** `ar4mirez`, sharing an existing org's Claude account (`SHARED_ACCOUNT=1`), git email `<email>`.
- **Test loop:** CI only for now, no tier-3 VM. Because of that, `host/ssh` is tested in CI against an `sshd` + `dind` service container.
