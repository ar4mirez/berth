# Secrets as files

An org's tokens (`CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, `GH_TOKEN`) and its custom variables (`berth env`)
can live in one of two places:

| | Where the values are | `docker inspect claude-<org>` shows them |
|---|---|---|
| **In `org.env`** (ccenv's way, and every org's until migrated) | `org.env`, passed to the container through compose's `env_file` | **yes**, to anyone with access to Docker |
| **As files** (after `berth secrets migrate`) | one file per variable in `<org>/config/secrets/env/` (0600, directory 0700), mounted read-only at `/config/secrets/env` | no |

Inside the container, nothing changes for your sessions. The entrypoint exports the files' values at start, so
tmux, the browser terminal, Remote Control and SSH sessions see them as before. `berth claude` and `berth run` load
them too.

## Migrating an org

```bash
berth secrets migrate <org>          # takes a backup first; --no-backup to skip it
```

- **It takes a backup first,** as `berth backup <org>` would. If the backup fails, nothing is moved.
- **It moves the tokens, and every variable named in `CCENV_ENV_KEYS`,** into files, and takes their lines out of
  `org.env`. The list of names stays in `org.env`.
- **Nothing restarts.** The running container keeps the environment it started with. The org's **next restart**
  (`berth restart <org>`) takes the values from the files, and from then on `docker inspect` shows none of them. That
  restart is a short one (see "What restarts" in `cutover.md`), so plan it. It fits in the bundled image update's
  restart.
- **Running it again** is safe: there's nothing left to move.

After migrating, the commands that read or write these values use the files instead of `org.env`: `token`,
`logout --all`, `env set/unset/ls`, `ls` (the TOKEN column) and `whoami`.

## Rollout on the host

1. **Upgrade berth first.** Only a berth with this feature knows about the files.
2. **Run `berth build`.** It restarts nothing.
3. **Run `berth secrets migrate <org>`** for each org. It restarts nothing.
4. **Do each org's next restart** at a moment you choose. It uses berth's new image, whose entrypoint reads the
   files.

Between steps 3 and 4 everything keeps working. The container still has its old environment, and `berth claude`/`run`
read the files directly, whichever image the container runs.

## Undoing it

Put the values back as lines in `org.env`, delete `<org>/config/secrets/env/`, and restart the org. The migration's
backup has the original `org.env`.
