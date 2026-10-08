# PRD: cloud hosts, and a host's own `berth serve` (#51, #52, #148)

Status: **done, with #52 untested against the real provider** · Restarts orgs: no

## Decisions (2026-10-08, the maintainer's)

- **#51: Hetzner Cloud** is the first provider (`docs/decisions/051-first-cloud-provider.md`).
- **#52 is tested against a stand-in for the API**, not a real VM. The maintainer runs the sandbox test.
- **A new host is reached over Tailscale**, as the issue says; the provider's firewall lets nothing in.
- **#148: the host's socket over ssh**: berth forwards a registered host's API socket through the connection it
  has, and sends whole operations.

## #148: a registered host's own `berth serve`

- `host.Host` gets `Unix`, a dialer for a Unix socket on the host (over ssh: a direct-streamlocal channel, as for
  the engine's socket).
- `internal/cli/hostapi.go`: when a command names `org@host`, its operation is one of a short list, and the host
  has a socket that answers, the command goes to `POST /v1/cli` there. Otherwise ssh, as before.
- **Only operations that are the same wherever they run**: `fw` (show, presets, test, allow, deny),
  `repo` (ls, add, rm), `env ls`, `pkg ls`, `remote status`. A test keeps the list inside what the API serves, and
  out of what depends on the operator's machine (the lifecycle and its lease, `info`, `ls`, backups).
- This is narrower than "a `Host` implementation", which the issue names: that would be remote file and exec
  endpoints. The maintainer chose operations.

## #52: `berth host create|destroy|reconcile`

- **`internal/cloud`**: Hetzner's HTTP API called directly (six calls). The issue asks for the SDK behind a build
  tag "so the default binary stays small": no SDK at all keeps it smaller, and leaves no released binary without
  the feature.
- **Labels** `berth.managed=true`, `berth.host=<name>` on the server and its firewall. `reconcile` lists by them
  and `--prune` deletes what belongs to no registered host; a failed `create` removes what it made.
- **cloud-init**: the pre-generated ssh host key, an `ops` user with berth's first-login key, password logins and
  root login off, Docker 28.x from Docker's repository, Tailscale joined with the single-use key.
- **Access**: a firewall with no rules. berth pins the host key it made, waits for `berth-<name>` on the tailnet,
  then registers it through `host add`, which installs the host guard (the "DOCKER-USER drops").
- **Metadata**: Hetzner has no instance roles and no IMDSv2. The equivalent lock-down is that org containers can't
  reach 169.254.0.0/16 (the guard, and each org's firewall).
- Secrets come from the environment (`HCLOUD_TOKEN`, `BERTH_TAILSCALE_AUTHKEY`), and are never printed.

## Tasks

- [x] T1. #148: the socket dialer, the delegation, `TestHostAPIOpsAreServed`, `TestHostAPI` (integration).
- [x] T2. #51: the decision record.
- [x] T3. #52: `internal/cloud`, `host create|destroy|reconcile`, tests against a stand-in API.
- [x] T4. Docs: `docs/hosts.md`, `PARITY.md`, the reference.
- [x] T5. #52's acceptance test in a sandbox project: `test/cloud`, run by the `cloud` workflow
      (`docs/prd/052-acceptance.md`).

## Acceptance criteria

| Criterion | How it is checked |
|---|---|
| #148: the remote tests pass through the new path | `TestHostAPI`: a read and a write through the host's berth, the same output as over ssh, the host's audit log, errors and exit codes |
| #148: a host without `berth serve` keeps working over ssh | `TestHostAPI`, before the server starts and after it stops; every other integration test |
| #52: create, up an org, destroy in a sandbox; no tagged resources remain | **not run.** Against the stand-in: `TestHostCreate`, `TestHostCreateLeavesNothingBehind`, `TestHostReconcileAndDestroy` |
| #52: the public IP has no open ports | **not run.** The firewall is created with no rules and the server behind it (`TestHostCreate`) |
| #52: the pinned host key matches | `TestHostCreate`: the fingerprint berth registers is the key in the server's user data. Not checked against a real sshd |

## Not done

- #52 against Hetzner itself, and so: the cloud-init document on a real VM, the wait on the tailnet, an org on
  the new host.
- The other access modes of #52's update (WireGuard, a private network with a bastion, a jump host).
- A CI job with a sandbox project.
