# Phase 0 kickoff prompt

Paste this into a new Claude Code session in the dev org's environment (`/workspace/berth`).

```
We're starting phase 0 of berth, the Go successor to legacy/ccenv. Before writing any code, read CLAUDE.md and all of docs/plan.md. Also read legacy/ccenv, image/repo-policy.sh, image/repo-guard.js and image/archive.sh closely enough to understand what behavior has to stay the same.

Deliver phase 0 as a series of small PRs against main. Keep each one reviewable and green in CI before starting the next. Use `gh pr create`, then `gh run watch` to see CI results; you have no Docker here, so anything that needs a daemon runs in Actions.

1. Toolchain and skeleton
   - `mise install`, then `go mod init github.com/ar4mirez/berth`.
   - A cobra root command `berth` with version info.
   - Errors print as `berth: <msg>` on stderr and exit 1. With no arguments, print help and exit 0. An unknown command exits 1.
   - Add golangci-lint config, a Makefile or justfile, and .goreleaser.yaml (linux/darwin, amd64/arm64). `goreleaser release --snapshot --clean` must work locally.

2. CI (.github/workflows)
   - ci.yml: build, vet, test, golangci-lint, govulncheck and a goreleaser snapshot.
   - integration.yml: a job with a real Docker daemon, even if it only runs a smoke test for now. Allow triggering it with workflow_dispatch.
   - Don't add a release workflow yet.

3. State root (internal/config)
   - Resolve the root in this order: --home > $BERTH_HOME > ~/.config/berth/config.yaml (`home:`) > ~/.local/share/berth.
   - Add a global --read-only flag. Any mutating operation must refuse to run while it is set.
   - Unit-test the precedence and the read-only guard.

4. Host abstraction (internal/host)
   - Implement `Host{FS, Exec, Docker, Facts}` as described in the plan. FS covers Read, Write, Chmod, Lock and atomic writes. Exec needs a TTY option and must propagate exit codes. Facts covers uid/gid, arch, Tailscale IP and ports in use.
   - `local` implementation.
   - `ssh` implementation using x/crypto/ssh + pkg/sftp, with berth's own known_hosts file (no trust-on-first-use without an explicit flag). Docker access goes through a streamlocal dial to /var/run/docker.sock.
   - Test `ssh` in integration.yml against an sshd + dind service container.
   - Add a lint rule or test that fails on direct os.* file I/O on org state outside internal/host/local.

5. org.env parity (internal/org)
   - A raw reader that mirrors `envval`: last duplicate wins, no unquoting.
   - A writer that mirrors `setval`: replace every match or append, rewrite in place, keep the inode and mode 0600.
   - `next_port` (max+1).
   - Golden tests with synthetic fixtures only: duplicate keys, a missing key, and values containing =, $, spaces and quotes.

6. repo-policy canonical spec
   - Write docs/repo-policy.md defining canonical repo URL form: slashes, ports, scheme, user@, and ASCII-only lowercasing.
   - Add testdata/canon.tsv with input and expected columns.
   - Fix image/repo-policy.sh and image/repo-guard.js so both match the spec, and add the Go implementation.
   - A test runs the table through bash, node and Go and fails if any of them disagrees.
   - The PR description must say that the image/ changes also have to be ported to the legacy checkout on the host before live orgs pick them up.

7. Parity harness (test/parity)
   - Fake docker, tailscale, gh, ssh, systemctl and crontab binaries on PATH. Each logs its argv and the relevant env (ORG, ORG_DIR, BIND_ADDR, HOST_UID) and returns scripted output.
   - A runner executes legacy/ccenv with CCENV_ORGS pointing at a temp fixture home and berth with --home pointing at the same fixtures. It diffs stdout, stderr (normalizing the ccenv:/berth: prefix), exit codes, the fake-binary transcripts, and the resulting file tree including file modes.
   - Create PARITY.md with one row per command, subcommand, flag and alias from legacy/ccenv. Columns: stdout/stderr/exit, files touched with modes, docker calls, side effects, status. Everything starts as "not started" except what this phase covers.

Rules
- Parity comes first. Any intentional divergence must be recorded in PARITY.md with the reason.
- No real org, client or repo names anywhere. Use acme, globex, initech and t-*.
- Fixtures are synthetic only.
- Keep shelling out to `docker compose`; no Engine API writes, no pty reimplementation.
- If the firewall blocks a host, stop and tell me the exact hostname so I can run `ccenv fw ar4mirez allow <host>` (or `reload` if it is a Google host; see issue #1).
- Once each PR is green, tell me and wait for my go-ahead before starting the next.
```
