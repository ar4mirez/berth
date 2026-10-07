# PRD: system packages per org (#106)

Status: **done** · Issue: [#106](https://github.com/ar4mirez/berth/issues/106) · Restarts orgs: yes (image: the in-container instructions)

## Problem

Some projects need system packages berth's image doesn't have. The case that hit this: Playwright's Chromium
downloads inside an org and can't start, for 23 missing libraries. Nothing in the container can fix it: apt needs
root, and mise doesn't provide system libraries.

The ways around it were all bad: installing as root into the running container (lost on every recreate, and it
needs a Debian mirror in the org's firewall), a forked `image/` for every org through `BERTH_IMAGE_DIR` (every
image grows, off the released image, re-merged at each upgrade), or another base image (no firewall, no git guard).

## Decisions (2026-10-07)

- **Proposal A only:** per-org packages. A global overlay (proposal B) can be its own issue if it is ever wanted.
- **The command is `berth pkg <org> …`**, shaped like `berth fw` and `berth env`.
- **A change builds now and restarts nothing.** `add` and `rm` build the org's image at once, so a bad name fails
  there and the list is put back; the image applies at the org's next restart. `--no-build` only saves.

## Objectives

1. An org can list Debian packages, and they are in its container after its next restart, and after every one
   since.
2. The released image, the firewall, the git guards and the entrypoint stay berth's. Other orgs are unaffected.
3. The org's firewall needs no Debian mirror.
4. An org with no packages behaves exactly as before, call for call.

## Design

- **The list** is `orgs/<org>/config/packages.txt`: one Debian package name or `@preset` per line. It is org
  config, so backups carry it, and the container can read it at `/config/packages.txt`.
- **The image** is `berth/claude-env-<org>:<base>-<list>`: berth's image hash, and a hash of the expanded, sorted
  package list. A change to either is a new image, so `berth upgrade` or an edit rebuilds it lazily.
- **The build** is a generated Dockerfile in `<state>/berth-orgs/<org>/`: `FROM` berth's image, one
  `apt-get install --no-install-recommends`, the apt lists removed. berth's image is pulled or built first, as
  usual, if it isn't there.
- **compose** gets that image and that directory as the org's `CLAUDE_ENV_IMAGE`, `IMAGE_TAG` and
  `CLAUDE_ENV_IMAGE_DIR`, for every compose command. `up` (so `restart`, `restore`, `env set`…) builds the image
  first when it isn't there.
- **Names are checked** against Debian's own rule before they are written or reach a command line.
- **`@playwright-chromium`** is the issue's verified list of 23 libraries and 7 font packages.
- **The agent is told:** `image/CLAUDE.md` says to ask for `berth pkg <org> add …` when a system library is missing.

## Tasks

- [x] T1. `internal/ops/packages.go`: the list, the check, presets, the image name, the Dockerfile.
- [x] T2. `berth pkg <org> [ls|add|rm|presets|build]`, with completion, help and the generated reference.
- [x] T3. compose runs an org with packages on its image, and builds it at `up` when missing.
- [x] T4. `--output json`: `berth.packages/v1`, `berth.package-presets/v1`, with schemas and golden files.
- [x] T5. Tests: `TestPackages`, `TestBerthPkg`.
- [x] T6. A real run: a `t-*` org in a temporary state root gets a package, restarts, and has it.
- [x] T7. Docs: `guides/packages.md`, `json.md`, `PARITY.md`, the in-container instructions.

## Acceptance criteria

| Criterion (from proposal A) | How it is checked |
|---|---|
| `add`, `ls`, `rm` manage a per-org list stored with the org | `TestBerthPkg` |
| berth builds a thin per-org image on the released one, and compose runs it for that org | `TestBerthPkg` (the build, then compose's image and context); the real run |
| Rebuilt when the base image or the list changes, lazily at the next `up`/`restart`; applying is a restart, and berth says so | `TestPackages` (the image name), `TestBerthPkg` (`restart` builds when missing; the message) |
| No Debian mirror in the org's firewall | The real run: the packages install at build time, with the org's firewall on |
| A `@playwright-chromium` preset | `TestPackages`; `pkg <org> presets` |
| Backups carry the list | It is in `config/`, which backups already include |

## Not done

- **`--from-playwright`** (deriving the list from a project's Playwright version), the issue's other nice-to-have.
- **Removing an org's older images.** They are left for `docker image prune`.
- **Proposal B**, by decision.
