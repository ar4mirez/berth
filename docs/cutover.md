# Cutover: from ccenv to berth, one org at a time

This moves the live orgs from ccenv to berth **without losing anything and without surprise downtime**. It follows
[`host-install.md`](host-install.md): berth is installed on the host, and stage 1's `parity-check` reported
`0 differ`.

```bash
LEGACY=~/Work/claude-envs     # the checkout that runs the live orgs (berth's state root from here on)
```

## What restarts, and when

Only one step stops anything, and it's separate, per org, and scheduled by you.

| Phase | What it does | Restarts a container? |
|---|---|---|
| A. Preflight | checks, inventory, backups | no |
| B. Port #9 and #19 to the checkout | copies berth's `image/` and `compose.yml` fixes into the checkout, and commits | no |
| C. Take over an org | `MANAGER=berth` in its `org.env` | **no**: the container keeps running as it is |
| C′. Backup schedule | adds `berth-backup` next to `ccenv-backup` | no |
| D. Image switch, per org | `berth restart <org>`: the container moves to berth's image | **yes, about 10 seconds** |
| E. Finish | removes `ccenv-backup`, writes berth's config, optional `ccenv` alias | no |

**Phase D is the only downtime.** For about 10 seconds the container is recreated, and everything *running* inside
it stops:
- tmux sessions, and the commands in them;
- Claude sessions;
- SSH and browser-terminal sessions;
- Remote Control (it reconnects after the restart).

**Files are not affected.** Workspace, `~/.claude` history, logins, mise toolchains and `~/.config` all live in the
org's folders on the host, and are mounted again as they are. The effect is the same as a `ccenv restart` today.

Phase D can wait as long as you like. Until it runs, berth manages the org while it keeps running on the old
`claude-env` image. Do it at a quiet moment, one org at a time.

Things that also recreate the container, under either tool:
- `restart` and `up`;
- `env set`/`unset` without `--no-restart`;
- `password rotate`;
- `repo policy <mode>`;
- `token`, `auth`, and `logout --all` on a running org.

**Avoid these on an org until you've planned its phase D.** Everything else (`fw`, `repo add`/`rm`/`sync`, `ls`,
`info`, `attach`, `shell`, `login`, backups) runs without a restart.

## Rules for the whole cutover

- **One org at a time, smallest first. `ar4mirez` goes last.** That's the org running this Claude session, so its
  phase D stops the session. Run it from the host.
- **Before touching an org, it has a fresh backup that was test-restored** (phase C, step 1).
- **Don't use ccenv's writing commands on an org while you're taking it over.**
- **After each step, verify. If anything looks off, stop and undo that step** (each step lists its undo).
- `berth parity-check` only compares orgs ccenv still manages. For orgs berth manages, the checks are in phase C.

## A. Preflight (no restarts)

```bash
berth --version                                    # the build you'll use; stage 1 installed it
git -C "$LEGACY" status --short                    # must be empty
grep -c berth_owned "$LEGACY/ccenv"                # must be > 0: ccenv refuses berth's orgs
berth --home "$LEGACY" parity-check                # must end "N checks, 0 differ"
df -h "$LEGACY"                                    # room for one extra copy of your largest org (phase C, step 1)
docker images berth/claude-env                     # berth's image; built in stage 2
```

Next, take an inventory of what else uses ccenv, so nothing is forgotten:

```bash
systemctl --user cat ccenv-backup.service ccenv-backup.timer 2>/dev/null   # ccenv's schedule: its time, --keep and -o
crontab -l 2>/dev/null | grep -n ccenv                                     # or a cron schedule, or other jobs
grep -rn 'ccenv' ~/.bashrc ~/.profile ~/.zshrc 2>/dev/null                 # aliases or scripts calling ccenv
```

Write down ccenv's schedule: its time, `--keep N`, `-o dir` if it has one, and whether its job sets
`CCENV_BACKUP_RECIPIENTS`. Phase C′ gives berth the same.

## B. Port #9 and #19 to the checkout (no restarts)

The checkout's `image/` and `compose.yml` get berth's two recorded changes (PARITY.md, "To port to the legacy
checkout at cutover"). Nothing running changes:
- `image/` is only used when ccenv next *builds* `claude-env`, which happens on `ccenv up`.
- `compose.yml`'s new lines default to exactly what ccenv uses today (`claude-env:latest`, `./image`).

This keeps ccenv (and a `handback`) in step with berth.

```bash
c=$(berth --version | sed -n 's/.*(commit \([0-9a-f]*\).*/\1/p')   # the commit of the berth you run
rm -rf /tmp/berth-src && git clone -q https://github.com/ar4mirez/berth /tmp/berth-src && git -C /tmp/berth-src checkout -q "$c"
/tmp/berth-src/scripts/drift.sh "$LEGACY"; echo "exit $?"          # before: shows exactly #9 and #19 (exit 1)
cp -a /tmp/berth-src/image/. "$LEGACY/image/"
cp /tmp/berth-src/compose.yml "$LEGACY/compose.yml"
/tmp/berth-src/scripts/drift.sh "$LEGACY"                           # after: "drift: none"
git -C "$LEGACY" add image compose.yml && git -C "$LEGACY" commit -m "Port berth #9 and #19 (cutover)"
berth --home "$LEGACY" parity-check                                 # still 0 differ
```

**Undo:** `git -C "$LEGACY" revert --no-edit HEAD`. That restarts nothing either.

## C. Take over one org (no restarts)

Repeat for each org, smallest first. The commands below use `ORG=<org>`.

```bash
ORG=<org>
```

**1. Back up, and test the backup** by restoring it as a stopped clone. That takes as much disk as the org, briefly.

```bash
berth --home "$LEGACY" backup "$ORG"                                # key-encrypted, into $LEGACY/backups
f=$(ls -t "$LEGACY"/backups/"$ORG"-*.tar.zst* | head -1); echo "$f"
berth --home "$LEGACY" restore "$f" --as "t-verify-$ORG" --no-start # must end "Restored to …"
du -sh "$LEGACY/orgs/$ORG" "$LEGACY/orgs/t-verify-$ORG"             # the clone is smaller by the skipped data only
docker run --rm -v "$LEGACY/orgs:/orgs" --entrypoint rm claude-env -rf "/orgs/t-verify-$ORG"
```

Keep `$f`. It's your restore point for this org.

**2. Record how things look now**, to compare after the takeover:

```bash
snap=$(mktemp -d)
for c in "info $ORG" "repo ls $ORG" "fw $ORG show" "env $ORG ls" "remote $ORG status"; do
  berth --read-only --home "$LEGACY" $c > "$snap/$(echo "$c" | tr ' ' _)" 2>&1
done
```

**3. Take it over.**

```bash
berth --home "$LEGACY" takeover "$ORG"
```

`takeover` only sets `MANAGER=berth` in the org's `org.env`, in place. It never touches the container, and it
refuses if the checkout's ccenv lacks the guard.

**4. Verify.**

```bash
for c in "info $ORG" "repo ls $ORG" "fw $ORG show" "env $ORG ls" "remote $ORG status"; do
  berth --read-only --home "$LEGACY" $c 2>&1 | diff -u "$snap/$(echo "$c" | tr ' ' _)" - && echo "same: $c"
done
"$LEGACY/ccenv" info "$ORG"        # must refuse: "managed by berth (MANAGER=berth); use: berth ... <org>"
berth --home "$LEGACY" ls          # the org is still up, on the same ports
```

Every line should say `same:`. `remote status` may differ in its Capacity count if a session
connected in between. The container, its uptime and your running work are untouched: `docker ps` shows the
same container, up for the same time.

**Undo:** `berth --home "$LEGACY" handback "$ORG"`. That sets `MANAGER=ccenv`, restarts nothing, and ccenv manages
the org again.

## C′. Backup schedule, right after the first takeover (no restarts)

ccenv's nightly `backup --all` skips berth's orgs. So from the first takeover on, berth must back up its own orgs.

Run both schedules until the last org has moved:
- `ccenv-backup` backs up ccenv's orgs;
- `berth-backup` backs up berth's orgs;
- both use `$LEGACY/backups` and the same key.

Use ccenv's `--keep` and `-o` from phase A, and a time 30 minutes after ccenv's, so the two don't run at once.

```bash
berth --home "$LEGACY" schedule --at 03:30 --keep 14        # same --keep (and -o dir, if ccenv has one) as ccenv's
berth --home "$LEGACY" schedule run                         # one run now: backs up the berth orgs
berth --home "$LEGACY" schedule status                      # the timer, and "Wrote …" lines for each berth org
```

If ccenv's job sets `CCENV_BACKUP_RECIPIENTS`, set it the same way in the shell before `berth schedule`, and
berth's job carries it too. Otherwise both jobs use the key file (`~/.config/ccenv/backup.key`). The job runs
`~/.local/bin/berth`, the link, so upgrading berth doesn't break it.

**Undo:** `berth --home "$LEGACY" schedule off`. It only removes `berth-backup`.

## D. Image switch, per org (a restart: plan it)

This is the only downtime: about 10 seconds for this org, and everything running in it stops (see the top). Pick a
quiet moment and save your work in it first.

```bash
berth --home "$LEGACY" build          # builds berth's current image beforehand (no restart), so the restart doesn't have to
berth --home "$LEGACY" restart "$ORG" # the restart: about 10 seconds
berth --home "$LEGACY" ls             # up, same ports
berth --home "$LEGACY" remote "$ORG" status
berth --home "$LEGACY" fw "$ORG" show # ends with "live: firewall: ON …"
berth --home "$LEGACY" attach "$ORG"  # the tmux session is new; your files are all there
```

The new image carries #9's stricter repo checks. Stage 1's `repo ls` and `repo audit` matching ccenv's show that the
org's registered repos are all accepted by them.

**Undo, if the org misbehaves on the new image:** a second short restart, back to `claude-env`.

```bash
berth --home "$LEGACY" handback "$ORG" && "$LEGACY/ccenv" restart "$ORG"
```

Last resort, if its files are damaged: restore it from `$f` with `berth --home "$LEGACY" restore "$f" --force`.
- That is a restart too: it stops the container, then starts and rehydrates the restored org.
- The current org folder moves to `$LEGACY/backups/.replaced/`, so it's kept.
- The restore point is from before the takeover, so anything written since then is only in that kept folder.

## E. Finish, after the last org (no restarts)

```bash
"$LEGACY/ccenv" schedule off              # removes ccenv-backup; berth-backup already covers every org
mkdir -p ~/.config/berth && printf 'home: %s\n' "$LEGACY" > ~/.config/berth/config.yaml   # plain `berth …` works now
berth ls && berth schedule status
berth install --alias ccenv               # optional: `ccenv …` runs berth (the images' CLAUDE.md still says ccenv)
```

After the alias, the old script is still there as `$LEGACY/ccenv`, for a `handback`. Keep `$LEGACY` and its
`backups/` exactly where they are: the orgs' bind mounts use those absolute paths.
