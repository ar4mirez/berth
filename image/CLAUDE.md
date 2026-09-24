# Container environment

You are running inside an isolated per-organization container. Repos live in `/workspace`.

## Allowed repos
Only the repos registered for this org may be used. They are the folders listed in `/config/repos.txt`, set up by the
user with `ccenv repo add`. This is enforced: cloning, adding remotes, and reading or editing other folders under
`/workspace` are blocked, and git can only fetch from or push to registered repos. Anything else placed in `/workspace`
is moved out automatically. If a task needs another repo, stop and ask the user to run
`ccenv repo add <org> <owner/repo>` on the host (the org name is in `/etc/claude-env/org`). Don't look for workarounds.

## Installing software
Use **mise** for language toolchains and CLIs. Don't use apt (you have no root) or curl-pipe installers.
- Global (all repos in this org): `mise use -g go@latest`, `mise use -g python@3.13 uv@latest`, `mise use -g rust@stable`, `mise use -g ruby@3`, `mise use -g node@lts`
- Per repo: `mise use go@1.23` writes a `mise.toml` in the current repo, and an existing `mise.toml`/`.tool-versions` is honored (`mise install`)
- Other CLIs: `mise use -g aqua:cli/cli`, `mise use -g ubi:owner/repo`, `mise use -g npm:pkg`, `mise use -g pipx:pkg`
- Python: prefer `uv` (`uv venv`, `uv pip install`, `uv run`) over bare pip
- Tools are on PATH right away through mise shims, and they persist across container restarts.

## Network
Outbound traffic goes through an allowlist firewall. If a download or API call fails with a connection error or
"Connection refused"/"administratively prohibited", it is probably blocked. Don't try to work around it. Instead, tell the user
the exact host(s) and ask them to run on the host: `ccenv fw <org> allow <host>` (the org name is in `/etc/claude-env/org`).
Presets exist for common ecosystems (`@python @go @rust @ruby @node @docker ...`).
