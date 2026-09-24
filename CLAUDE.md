# berth: working rules

berth is the Go successor to `legacy/ccenv` (Bash). The plan and all architecture decisions are in `docs/plan.md`. Read
it before starting a phase, and keep `PARITY.md` current once it exists.

## You are dogfooding
You probably run inside a ccenv container (the org name is in `/etc/claude-env/org`) that the legacy tool manages.
- **No Docker here, on purpose.** Don't look for a socket, DinD, or other workarounds. Anything that needs a real daemon
  runs in GitHub Actions: `gh workflow run <wf>` and `gh run watch`.
- **Network is allowlisted.** If a host is blocked, ask the user to run `ccenv fw <org> allow <host>`. `GOPROXY` is pinned
  to proxy.golang.org in `mise.toml`, because a `direct` fallback would be blocked by the git guard.
- **Only this repo is allowed.** Other repos need `ccenv repo add` on the host.

## Hard rules
- **Parity first.** Behaviour must match `legacy/ccenv` exactly, unless `PARITY.md` records the divergence and its reason.
- **Never touch live org state.** Mutating tests only run on `t-*` orgs in a temp `BERTH_HOME`. Fixtures are synthetic.
  Never copy a real `org.env`, because it contains tokens.
- **Wire contracts keep ccenv names:** `.ccenv-manifest.json`, `.ccenv-format`, the org.env keys, and the container env vars.
  Host-side names use `berth`/`BERTH_`.
- **All org-state I/O goes through `Host.FS`/`Host.Exec`.** No direct `os.*` calls on org state outside `internal/host/local`.
- **Keep shelling out to `docker compose`** until parity is done. Don't reimplement compose or the pty.
- **Scrub real names.** No real org, client, or repo names in code, docs, fixtures, or commit messages. Use `acme`,
  `globex`, `initech`, and `t-*`.
- **`image/` changes** reach the live orgs only after they are ported to the legacy checkout on the host. Call this out
  in the PR description.

## Commands
- `go build ./... && go vet ./... && go test ./...`
- `golangci-lint run`
- `goreleaser release --snapshot --clean`
