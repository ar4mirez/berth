# Installing berth on the host, and checking parity

This is the first part of phase 5 (cutover) in [`plan.md`](plan.md): put berth on the host next to ccenv and prove
it matches, **without changing any live org**. Stage 1 only reads. Stage 2 is optional: it exercises the writing
commands on a throwaway clone of one org. The cutover itself (setting `MANAGER=berth` on live orgs, swapping the
backup schedule) is a separate step. It is not in this guide.

Paths below assume the legacy checkout is `~/Work/claude-envs` (the same default as `scripts/drift.sh`). Set
`LEGACY` to yours if it's elsewhere. At the end there are [prompts](#prompts-for-claude-on-the-host) you can hand to
Claude Code on the host, so it can run each stage and report back.

```bash
LEGACY=~/Work/claude-envs     # the ccenv checkout that runs the live orgs
```

## Rules until cutover

- **Every berth command against the live checkout runs with `--read-only --home "$LEGACY"`.** Two exceptions are
  covered by stage 2: the `backup` command, and anything run on the `t-*` clone.
- Don't write `~/.config/berth/config.yaml`, don't run `berth install --alias ccenv`, and don't run `berth schedule`.
  All three are cutover steps.
- Don't edit a live org's `org.env`, and don't set `MANAGER` on a live org.
- berth refuses to change an org it doesn't own anyway (no `MANAGER=berth` means ccenv's), and `--read-only` refuses
  every write. These rules are the belt to those braces.

## Stage 0: what you need

- `gh`, signed in (`gh auth status`). Artifact downloads need it, even for a public repo.
- `docker` (already there for ccenv), plus `sha256sum` and `tar`.
- About 15 MB of disk for berth. Stage 2 also builds berth's image (`berth/claude-env:<hash>`), which is as big as
  `claude-env` and takes a few minutes the first time.

## Stage 1: install and check (read-only)

### 1. Download a CI build of `main`, and verify it

berth is only installed from CI builds, never from a workspace build (plan, decision 8). Every `ci` run on `main`
uploads checksummed linux/darwin archives, kept for 14 days.

```bash
dl=$(mktemp -d)
run=$(gh run list -R ar4mirez/berth --workflow ci --branch main --status success --limit 1 --json databaseId,headSha -q '.[0]')
gh run download "$(jq -r .databaseId <<<"$run")" -R ar4mirez/berth -n "berth-$(jq -r .headSha <<<"$run")" -D "$dl"
(cd "$dl" && sha256sum -c --ignore-missing checksums.txt)      # must print "OK" for the archive you use

arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
tar -xzf "$dl"/berth_*_linux_"$arch".tar.gz -C "$dl" berth
"$dl"/berth --version                                          # note the version and commit
```

### 2. Install it

The binary goes in a versioned directory, and `berth install` links it onto your PATH with bash completion. To
upgrade later, repeat steps 1 and 2 with the new version; the link moves to it.

```bash
ver=$("$dl"/berth --version | awk '{print $2}')
mkdir -p ~/.local/opt/berth/"$ver"
install -m 0755 "$dl"/berth ~/.local/opt/berth/"$ver"/berth
~/.local/opt/berth/"$ver"/berth install       # links ~/.local/bin/berth; says so if ~/.local/bin isn't on PATH
berth --version
rm -rf "$dl"
```

For convenience in this stage:

```bash
alias berthro='berth --read-only --home "$LEGACY"'
```

### 3. Smoke test

```bash
berthro ls                 # same table as `ccenv ls`
berthro info <org>         # same sheet as `ccenv info <org>`
berthro whoami
berth --read-only --home "$LEGACY" fw <org> allow example.com   # must be refused: "read-only mode …"
```

### 4. The automated parity check

`berth parity-check` runs every command that only reads through both tools, on the same checkout, and compares
stdout, stderr and the exit code. The commands are:
- `ls` and `whoami`;
- for each org, `info`, `whoami`, `repo ls`, `repo audit`, `repo policy`, `fw show`, `env ls` and `remote status`.

It normalizes only what is meant to differ: the `ccenv:`/`berth:` error prefix, and command hints such as
`run: ccenv up acme`. A difference counts only if a second run shows it too, because container state can change
between two runs. The check itself changes nothing.

```bash
berth --home "$LEGACY" parity-check                 # every ccenv org; or: parity-check <org> [<org>...]
berth --home "$LEGACY" parity-check --legacy "$LEGACY/ccenv"   # if it doesn't find ccenv by itself
```

Every line should say `ok`, and the last line should read `N checks, 0 differ` (exit 0). A `DIFF` shows a
line diff of the two runs: `-` lines are ccenv's, `+` lines are berth's. Send me the whole output; it's a bug
unless PARITY.md already records that difference.

Two things to know:
- ccenv's own `fw show` and `repo ls` create a missing `firewall.txt`/`repos.txt`, as they always do (PARITY.md,
  legacy quirk 5). berth under `--read-only` never writes.
- `whoami` and `info` print account emails, the Remote Control URL and SSH details. That's the same information
  `ccenv` prints, so don't paste the output anywhere public.

### 5. Is the legacy checkout ready for cutover? (read-only)

None of this changes anything. It lists what has to be ported to the checkout at cutover.

```bash
git -C "$LEGACY" status --short               # should be clean
grep -c berth_owned "$LEGACY/ccenv"           # >0: ccenv already refuses berth's orgs. 0: port that patch before cutover
grep -c 'CCENV_ORGS' "$LEGACY/ccenv"          # >0: CCENV_ORGS/CCENV_BACKUP_DIR supported (parity-check uses CCENV_ORGS)
git clone --depth 1 https://github.com/ar4mirez/berth /tmp/berth-src && /tmp/berth-src/scripts/drift.sh "$LEGACY"
```

`drift.sh` should show only the changes in PARITY.md's "To port to the legacy checkout at cutover" table: #9
(`image/repo-policy.sh`, `image/repo-guard.js`, the git guard) and #19 (`compose.yml`'s `image:` and
`build.context`). Anything else is drift and needs a look. If `grep -c CCENV_ORGS` says 0, parity-check still works
as long as `--home` is that same checkout, because ccenv then uses its own `orgs/`.

## Stage 2 (optional): writing commands on a clone

This exercises berth's writing commands on real data, on a clone named `t-<org>` in the same checkout. `restore`
gives the clone free ports, and makes it berth's (`MANAGER=berth`), so ccenv won't touch it.

> The clone has the same git key and gh login as the source org, so it can push to the same repos. Sign it out
> (below), and don't push from it.

```bash
ORG=<small org>; T=t-$ORG; bk=$(mktemp -d)   # a private dir (0700) for the backup

# 1. Back up the source org. With ~/.config/ccenv/backup.key present, this is key-encrypted and needs no prompt.
#    Without a key, add --no-encrypt (the file stays in $bk and is deleted in step 5).
berth --home "$LEGACY" backup "$ORG" -o "$bk"

# 2. Restore it as the clone, stopped, then sign the clone out and turn Remote Control off.
berth --home "$LEGACY" restore "$bk"/"$ORG"-*.tar.zst* --as "$T" --no-start
berth --home "$LEGACY" logout "$T" --all
sed -i 's/^REMOTE_CONTROL=.*/REMOTE_CONTROL=0/' "$LEGACY/orgs/$T/org.env"

# 3. Lifecycle. The first `up` builds berth's image.
berth --home "$LEGACY" up "$T"
berth --home "$LEGACY" ls
berth --home "$LEGACY" info "$T"
berth --home "$LEGACY" shell "$T"                           # look around, then exit
berth --home "$LEGACY" fw "$T" allow example.com && berth --home "$LEGACY" fw "$T" test example.com
berth --home "$LEGACY" fw "$T" deny example.com
berth --home "$LEGACY" repo ls "$T"
berth --home "$LEGACY" env "$T" ls
berth --home "$LEGACY" restart "$T"
berth --home "$LEGACY" down "$T"

# 4. ccenv must refuse the clone (it is berth's now), if its checkout has the guard (stage 1, step 5: berth_owned > 0).
#    If it doesn't, skip this, and never run ccenv on the clone: port the guard before cutover.
"$LEGACY/ccenv" info "$T"         # expected: "org 't-…' is managed by berth (MANAGER=berth); use: berth ... t-…"

# 5. Clean up: the clone's sshd/ is root-owned, so remove it through docker. Then remove the backup.
docker run --rm -v "$LEGACY/orgs:/orgs" --entrypoint rm claude-env -rf "/orgs/$T"
rm -rf "$bk"
```

Everything above should behave as the same `ccenv` command would; PARITY.md lists the intended differences. Note
anything that looks off, with the command and its output.

## Prompts for Claude on the host

Paste one prompt per stage. Both keep Claude to the rules above, and have it stop and report instead of improvising.

### Stage 1 (read-only)

```text
You're on the host that runs my ccenv orgs. Install berth next to ccenv and check it matches ccenv, following
https://github.com/ar4mirez/berth/blob/main/docs/host-install.md, stage 1 (steps 1–5). The legacy checkout is
~/Work/claude-envs (set LEGACY to it).

Rules, no exceptions:
- Only run berth against the checkout as `berth --read-only --home "$LEGACY" …`, plus `berth --home "$LEGACY"
  parity-check`, which only reads.
- Don't run any ccenv command that changes anything (up, down, restart, init, fw allow/deny, repo add/rm, env set,
  backup, restore, schedule, …). Reading commands are fine.
- Don't edit anything under ~/Work/claude-envs, don't create ~/.config/berth/config.yaml, and don't run
  `berth install --alias` or `berth schedule`.
- Install berth only from the CI artifact as in step 1, and check its checksum. If a download is blocked, or a
  checksum or command fails, stop and tell me; don't work around it.

Report back:
1. the berth version and commit, and where it's installed;
2. the full output of `berth --home "$LEGACY" parity-check` (the summary line, and every DIFF block verbatim);
3. step 5: git status, the two grep counts, and what drift.sh printed, with anything beyond #9 and #19 called out.
Don't paste account emails or Remote Control URLs outside this report.
```

### Stage 2 (a clone, optional)

```text
Continue with stage 2 of https://github.com/ar4mirez/berth/blob/main/docs/host-install.md on the host, using
ORG=<small org> (so the clone is t-<org>). LEGACY=~/Work/claude-envs.

Rules, no exceptions:
- The only berth commands that may write are the ones listed in stage 2, and only on the clone t-<org>. The one
  exception is `backup <org>`, which only reads the source org.
- Never run a writing command (berth or ccenv) on any other org, never edit a live org's files, and never push from
  the clone.
- If backup would prompt for a passphrase, stop and ask me.
- Stop at the first unexpected error or output, and report it; don't try to fix it.
- Always run step 5 (cleanup) at the end, even if something failed, and confirm t-<org> is gone
  (`berth --read-only --home "$LEGACY" ls`).

Report each step's command and output. Call out anything that differs from what the same ccenv command would do,
and the time the first `up` took.
```
