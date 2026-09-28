# Toolchains and Claude Code

## Installing software in an org (mise)

Every container ships with [mise](https://mise.jdx.dev), plus the build dependencies needed to compile tools such as
Ruby. Claude is told to use it, so it can install what a task needs:

```bash
mise use -g go@latest python@3.13 uv@latest rust@stable ruby@3   # org-wide
mise use node@lts                                                  # per repo (writes mise.toml)
mise use -g aqua:mikefarah/yq                                      # any CLI from aqua, ubi, npm or pipx
```

Tools live in the org's `mise/` folder, so they survive restarts and rebuilds. Backups skip them, and
`berth rehydrate` (which `restore` runs) reinstalls them. Each org has its own set.

## The model

Every session is pinned to `opus`, which resolves to the latest Opus, through managed settings. That includes the
sessions started from claude.ai.

## Updating Claude Code

The image pins a Claude Code version, and the auto-updater is off inside containers, so every org runs the version
its image has.

| To | Do |
|---|---|
| **Get a newer pinned version** | Upgrade berth (`berth upgrade`), then restart each org when it suits you (`berth restart acme`). A new image reaches an org only at its restart. |
| **Try another version locally** | `CLAUDE_CODE_VERSION=<version> berth build`, then restart the org. |

[Image rollout](../image-update.md) walks through rolling a new image out across orgs, one at a time.
