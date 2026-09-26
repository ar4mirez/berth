# Rolling out an image update

berth builds each org's container from its own embedded image. A new berth version whose `image/` changed reaches an
org only when that org's container is **recreated**. Until then, the org keeps running on the image it started with.
This guide rolls such an update out without surprises: **one org at a time, each restart approved by you, each one
verified, each one undoable.**

It's written for **v0.2.0**, the first image update after the cutover. That update contains:

| Issue | What changes in the container |
|---|---|
| #39 | every tool pinned by version and checksum (Claude Code stays at the version the orgs run) |
| #38 | the container user is set to your host user at start, not baked into the image. It's the same UID on this host, so file ownership doesn't change |
| #1 | DNS goes through a local dnsmasq that adds allowlisted answers to the firewall, so rotating hosts (`sum.golang.org`, CDNs) stop being blocked, and DNS to outside resolvers is blocked. An allowlist entry now also covers its subdomains |
| #37 | the image can read secrets from files. Nothing uses that until you run `berth secrets migrate` (phase 2, optional) |

The steps are the same for later image updates. Only the table changes.

```bash
LEGACY=~/Work/claude-envs          # berth's state root (config.yaml's home:)
NEW=v0.2.0                         # the release to install
```

## What restarts

| Phase | Restarts? |
|---|---|
| A. Preflight | no |
| B. Install the new berth; `berth build` | no. Every org keeps running as it is |
| C. Per org: the image switch (`berth restart <org>`) | **yes: about 10–20 seconds per org, when you say so** |
| D. Optional, later: secrets as files (`berth secrets migrate`, then a restart) | **yes: one more restart per org, when you say so** |

During a restart, everything *running* in that org's container stops: commands, tmux and Claude sessions, SSH, and
the browser terminal. Remote Control reconnects afterwards. **Files are not affected:** workspace, history, logins
and toolchains live on the host.

## A. Preflight (no restarts)

```bash
berth --version                                # the version running now: note it for rollback
ls ~/.local/opt/berth/                         # installed versions; keep the current one
berth ls                                       # every org up, on its usual ports
berth schedule status                          # the nightly backup ran: "Wrote …" for each org, no "failed"
systemctl --user cat berth-backup.service | grep ExecStart   # should run ~/.local/bin/berth (the link)
df -h ~/.local/share/docker /var/lib/docker 2>/dev/null; docker system df   # room for one more image (~2 GB)
```

If `ExecStart` names a versioned path (`~/.local/opt/berth/<version>/berth`), note its `--keep` and `-o`. After step
B, run `berth schedule` again with the same values, so the job runs the link from then on. That restarts nothing.

## B. Install the new berth and build its image (no restarts)

```bash
dl=$(mktemp -d) && gh release download "$NEW" -R ar4mirez/berth -D "$dl"
(cd "$dl" && sha256sum -c --ignore-missing checksums.txt)          # "OK" for the archive you use
gh attestation verify "$dl"/berth_*_linux_amd64.tar.gz -R ar4mirez/berth   # built by berth's release workflow
# If cosign is installed, the signature too (docs/releases.md):
# cosign verify-blob --bundle "$dl/checksums.txt.sigstore.json" --certificate-identity \
#   "https://github.com/ar4mirez/berth/.github/workflows/release.yml@refs/tags/$NEW" \
#   --certificate-oidc-issuer https://token.actions.githubusercontent.com "$dl/checksums.txt"
arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
ver=${NEW#v}; mkdir -p ~/.local/opt/berth/"$ver"
tar -xzf "$dl"/berth_"${ver}"_linux_"$arch".tar.gz -C "$dl" berth && install -m 0755 "$dl"/berth ~/.local/opt/berth/"$ver"/berth
~/.local/opt/berth/"$ver"/berth install && berth --version && rm -rf "$dl"

berth build                     # the new image, ahead of the restarts (a few minutes: every pinned tool downloads)
docker images berth/claude-env  # the new tag, next to the one the orgs run now
berth ls                        # nothing restarted: every org is up, as before
```

**Undo:** `~/.local/opt/berth/<previous>/berth install` points `berth` back at the previous version. Nothing restarts.

## C. The image switch, one org at a time (a restart each)

Smallest org first. `ar4mirez` goes last, because its restart stops the Claude session that runs in it, so do that
one from the host. For each org:

```bash
ORG=<org>; snap=$(mktemp -d)
for c in "info $ORG" "fw $ORG show" "env $ORG ls" "remote $ORG status" "whoami $ORG"; do
  berth $c > "$snap/$(echo "$c" | tr ' ' _)" 2>&1 || true
done
docker inspect -f '{{.Config.Image}}' claude-"$ORG"         # the image it runs now

berth restart "$ORG"                                         # THE RESTART (about 10–20 seconds)

docker inspect -f '{{.Config.Image}}' claude-"$ORG"         # the new tag from step B
berth ls                                                     # up, same ports
berth fw "$ORG" show | tail -1                               # "live: on <N>" (N can differ a little: DNS)
docker exec claude-"$ORG" sh -c 'grep -q "^nameserver 127.0.0.1" /etc/resolv.conf && pgrep -x dnsmasq >/dev/null && echo dns-ok'
docker exec claude-"$ORG" sh -c '[ "$(id -u node)" = "$HOST_UID" ] && echo uid-ok'
docker exec -u node claude-"$ORG" touch /home/node/.config/.berth-uid-check
stat -c '%U' "$LEGACY/orgs/$ORG/home-config/.berth-uid-check" && rm -f "$LEGACY/orgs/$ORG/home-config/.berth-uid-check"   # your user
docker exec -u node claude-"$ORG" bash -lc 'test -n "${CLAUDE_CODE_OAUTH_TOKEN:-}${ANTHROPIC_API_KEY:-}" && echo token-ok'
docker exec -u node claude-"$ORG" claude --version           # the pinned Claude Code
for c in "env $ORG ls" "remote $ORG status" "whoami $ORG"; do
  berth $c 2>&1 | diff -u "$snap/$(echo "$c" | tr ' ' _)" - >/dev/null && echo "same: $c" || echo "CHANGED: $c"
done
```

What to expect:
- `dns-ok`, `uid-ok` and `token-ok` all print.
- The `stat` names your user.
- `same:` for `env ls` and `whoami`. For `remote status`, `restarting` for a few seconds, then `running`, is normal
  (Remote Control reconnects).

Then attach and check that your repos, tmux and tools are where you left them: `berth attach "$ORG"`.

**Undo for that org** (another short restart): point `berth` back at the previous version and restart the org, and
it runs its previous image again, which is still on the host:
```bash
~/.local/opt/berth/<previous>/berth install && berth restart "$ORG"
```
Then reinstall the new version (`~/.local/opt/berth/<new>/berth install`) for the other orgs, or stop there and
report.

## D. Optional, later: secrets as files (a restart each)

Only after every org runs the new image and has been fine for a while. `docs/secrets.md` has the details. Per org:

```bash
berth secrets migrate "$ORG"      # backup first; moves the tokens and custom variables into files; no restart
berth restart "$ORG"              # THE RESTART: from now on docker inspect shows no secret
docker inspect claude-"$ORG" | grep -c 'CLAUDE_CODE_OAUTH_TOKEN\|GH_TOKEN\|ANTHROPIC_API_KEY'   # 0
docker exec -u node claude-"$ORG" bash -lc 'test -n "${CLAUDE_CODE_OAUTH_TOKEN:-}${ANTHROPIC_API_KEY:-}" && echo token-ok'
```

## Prompt for Claude on the host

Run it in Claude Code **on the host**, not inside an org's container. It covers phases A–C. Phase D is a separate
decision.

```text
Roll out berth's new image to my orgs on this host, following
https://github.com/ar4mirez/berth/blob/main/docs/image-update.md, phases A to C, with NEW=v0.2.0 and
LEGACY=~/Work/claude-envs.

Rules, no exceptions:
- Only phase C restarts containers, and only one org at a time: smallest org first (du -s "$LEGACY"/orgs/*),
  ar4mirez always last. Before each org's restart, ask me. Name the org, and say that for about 10–20 seconds
  everything running in it stops (commands, tmux and Claude sessions, SSH), and that Remote Control reconnects
  afterwards. Wait for my explicit "yes" for that org. If I say no or later, or if you can't ask me (a
  non-interactive run), skip that org and list it as pending.
- Never run anything else that restarts or stops a container (up, restart, down, env set without --no-restart,
  password rotate, repo policy <mode>, token/auth/logout --all on a running org). Don't run phase D.
- Install berth only from the release as in phase B, and check the checksums and the attestation. If a download or
  a check fails, stop and tell me; don't work around it.
- Never delete or move anything under ~/Work/claude-envs/orgs or ~/Work/claude-envs/backups, and keep the previous
  berth version installed.
- After each org's restart, run all of phase C's checks. At the first unexpected result, run that org's undo from
  phase C, stop, and report; don't continue with other orgs.

Report:
- phase A's output (versions, backups, the schedule's ExecStart, free space);
- phase B: the verified checksum and attestation, the new version, the build time, the new image tag;
- for each org: its size, the restart time, every check's output, and whether it's done or pending.
```
