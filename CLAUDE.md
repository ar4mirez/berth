# berth: working rules

berth is the Go successor to `legacy/ccenv` (Bash). The plan and all architecture decisions are in `docs/plan.md`. Read
it before starting a phase, and keep `PARITY.md` current.

## You are dogfooding
You probably run inside an org container that berth manages (the org name is in `/etc/claude-env/org`). The live orgs
were moved from ccenv to berth in phase 5 (`docs/cutover.md`); `legacy/ccenv` stays in this repo as the parity
reference.
- **No Docker here, on purpose.** Don't look for a socket, DinD, or other workarounds. Anything that needs a real daemon
  runs in GitHub Actions: `gh workflow run <wf>` and `gh run watch`.
- **Network is allowlisted.** If a host is blocked, ask the user to run `berth fw <org> allow <host>` on the host
  (`berth fw <org> reload` when a known host's addresses changed). `GOPROXY` is pinned to proxy.golang.org in
  `mise.toml`, because a `direct` fallback would be blocked by the git guard.
- **Only this repo is allowed.** Other repos need `berth repo add` on the host.

## Hard rules
- **Parity first.** Behaviour must match `legacy/ccenv` exactly, unless `PARITY.md` records the divergence and its reason.
- **Never touch live org state.** Mutating tests only run on `t-*` orgs in a temp `BERTH_HOME`. Fixtures are synthetic.
  Never copy a real `org.env`, because it contains tokens.
- **Nothing on the host restarts by surprise.** Anything that recreates a live org's container (`up`, `restart`, and the
  commands `docs/cutover.md` lists under "What restarts") stops the work running in it. Call it out in advance, per org,
  and let the user pick the moment.
- **Wire contracts keep ccenv names:** `.ccenv-manifest.json`, `.ccenv-format`, the org.env keys, and the container env vars.
  Host-side names use `berth`/`BERTH_`.
- **All org-state I/O goes through `Host.FS`/`Host.Exec`.** No direct `os.*` calls on org state outside `internal/host/local`.
- **Keep shelling out to `docker compose`.** Don't reimplement compose or the pty.
- **Scrub real names.** No real org, client, or repo names in code, docs, fixtures, or commit messages. Use `acme`,
  `globex`, `initech`, and `t-*`.
- **`image/` and `compose.yml` changes** reach a live org only when the host runs a berth build that has them (a CI
  build of `main`, `docs/host-install.md`) and that org's container is recreated. That's a restart (see above): say so in
  the PR description.

## Commands
- `go build ./... && go vet ./... && go test ./...`
- `golangci-lint run`
- `goreleaser release --snapshot --clean`
