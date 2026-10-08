# PRD: #52's acceptance on Hetzner itself

Status: **in progress** · Issue: [#52](https://github.com/ar4mirez/berth/issues/52) · Restarts orgs: no

## Where #52 stands

`berth host create|destroy|reconcile` shipped in 0.9.0 (#155), tested against a stand-in for Hetzner's API. On
2026-10-08 `host create` ran against Hetzner for the first time, by hand: the server came up closed to the internet,
joined the tailnet and was registered, and an org on it started and was isolated. That run found #159 (fixed) and
two things about the tailnet:

- the host had joined **without a tag** (the key wasn't tagged), so its key expires in 180 days and the host is
  then out of reach, with nothing saying so;
- after a destroy, the machine is **still listed in the tailnet**.

`host destroy` and `host reconcile` have not run against Hetzner.

## Decisions (asked)

- **One acceptance test, run two ways**: from a maintainer's machine, and by a workflow started by hand.
- **Tailscale: warn only.** berth takes no credential for the tailnet.

## What this adds

- **`test/cloud`** (build tag `cloud`): create a host; the server presents the pinned host key; one server and one
  firewall carry the labels; an org is created and started, and its ports answer on the tailnet; no TCP port of
  the public address is open (1-1024, the org's ports, sshd's, ttyd's, Docker's); destroy is refused while the org
  is there; after the org's and the host's destroy, reconcile is clean and nothing labelled is left. Its cleanup
  deletes what the run made, however it ends.
- **`.github/workflows/cloud.yml`**, `workflow_dispatch` only: joins the runner to the tailnet, makes a single-use
  tagged auth key, runs the test, removes the test's machine from the tailnet.
- **`host create` warns when the host joined without a tag**, with the day its key expires.
- **`host destroy` names the machine left in the tailnet.**

## Tasks

- [x] T1. The warning and the note; unit tests.
- [x] T2. `test/cloud/acceptance_test.go`; linted with the `cloud` tag.
- [x] T3. The `cloud` workflow.
- [x] T4. Docs: `docs/hosts.md`, "Testing against Hetzner".
- [ ] T5. The test passes against Hetzner from the maintainer's machine.
- [ ] T6. The workflow passes (needs the three secrets, and a tailnet policy for `tag:ci` and `tag:berth`).

## Not in this

- The scan is TCP only, and from wherever the test runs.
- #52's other access modes (WireGuard, a private network with a bastion, a jump host: #99).
- The provider's SDK behind a build tag: berth calls the API directly and has no SDK to hide.
