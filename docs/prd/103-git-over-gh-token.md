# PRD: git uses the gh-login token, or the org's SSH key (#103)

Status: **done** · Issue: [#103](https://github.com/ar4mirez/berth/issues/103) · Restarts orgs: yes (image)

## Problem

An org has one SSH key, and every git operation in its container uses it. When that key was added to GitHub as a
deploy key, it can write to exactly one repository. Every repo registered later clones (a public repo needs no
write access) and then fails at the first `git push`, with `Permission to … denied to deploy key`. Nothing in berth
says so before that point, and nothing inside the container can fix it.

The org usually already holds a credential that would work: `berth gh-login` signs `gh` in with a token that has the
`repo` scope. Git never uses it.

## Decision

The issue left one decision open: may git use the gh-login token? **Decided 2026-10-06: support both transports.**

- An org whose `gh` is signed in reaches github.com over **HTTPS with that token**.
- An org whose `gh` is not signed in keeps **SSH with the org's key**, exactly as before.
- `GIT_TRANSPORT=ssh` in `org.env` keeps SSH even with a gh login, for an org that wants the token for PRs and
  issues only.

The token already reaches every repo its account can (`docs/plan.md`, "Dogfooding setup"), so git using it does not
widen what the org holds. Both transports only connect for repos in `config/repos.txt`.

## Objectives

1. A registered GitHub repo is writable with no per-repo key setup, when the org has a gh login.
2. An unregistered repo stays unreachable, over SSH and over HTTPS.
3. `berth repo add` says so when the org can't push to the repo it just registered.
4. The docs stop presenting a deploy key as an equal option.

## Design

### In the image

| Piece | What it does |
|---|---|
| `git-transport` (root) | Chooses `https` or `ssh` for github.com and writes git's system `url.*.insteadOf` rules. Runs at container start, every 30 seconds, and from `berth gh-login`. The choice is in `/run/git-transport`. |
| `git-remote-berth` (node) | git's remote helper for `berth::https://github.com/<path>`. Checks the repo against `repos.txt` with `repo_canon`, as `git-ssh-guard` does, then runs git's own `remote-https` with gh's token as the only credential. |
| `git-ssh-guard` | Unchanged. |
| `repo-guard.js` | Also refuses commands that name the new transport pieces (`credential.helper`, `remote-https`, `git-remote-berth`, `git-transport`, `GIT_TRANSPORT`). |

In `https` mode, every github.com URL form (`https://`, `http://`, `git@github.com:`, `ssh://git@github.com/`) is
rewritten to `berth::https://github.com/`, so a repo cloned over SSH before keeps working without touching its
`origin`. In `ssh` mode the rules are the image's original ones. gitlab.com and bitbucket.org always use SSH.

### In the CLI

- `berth gh-login` runs `git-transport apply` in the container once signed in, so git switches at once.
- `berth repo add`, after a clone (or when the repo is already there), runs a dry-run push in the container. If it
  is refused, it prints a warning with the cause and the fix. The add still succeeds: read-only use is legitimate.

## Tasks

- [x] T1. Prototype the HTTPS transport in a container: clone, fetch, dry-run push; unregistered repos refused.
- [x] T2. `image/git-remote-berth`, `image/git-transport`; Dockerfile and entrypoint wiring.
- [x] T3. `repo-guard.js`: refuse tampering with the new pieces.
- [x] T4. `berth gh-login` applies the transport.
- [x] T5. `berth repo add` checks write access and warns.
- [x] T6. Tests: the HTTPS guard agrees with `canon.tsv` (the repo-policy agreement test), contract checks, parity
      suite (berth-only tests for the new calls), an image CI step.
- [x] T7. Docs: getting-started, `guides/repos.md`, `repo-policy.md`, `PARITY.md`.

## Acceptance criteria

| Criterion (from the issue) | How it is checked |
|---|---|
| With `gh-login` and a deploy-key org key, `git push` to a second registered repo succeeds | Container test: in `https` mode a registered repo clones, fetches and passes a dry-run push using only the token (no SSH key mounted at all). CI repeats the guard's side of it with a fake token. |
| An unregistered repo stays unreachable over SSH and HTTPS | `TestHTTPSGuardAgreement` runs every `canon.tsv` row through `git-remote-berth`; the image CI step tries each URL form against an unregistered repo. |
| `repo add` on a repo the org can't push to warns with the cause and the fix; the add succeeds | `TestBerthRepoAddWriteCheck`. |
| Docs updated; the repo-policy agreement test covers the HTTPS path | The docs above; `TestHTTPSGuardAgreement`. |

## Out of scope

- gitlab.com and bitbucket.org over HTTPS: `gh` holds no token for them.
- Making the allowlist a hard barrier for a process that deliberately bypasses git's configuration. That is the
  existing model for the token (`docs/guides/repos.md`), and unchanged.

## Live-org impact

The transport lives in the image. It reaches an org after a host upgrade to a berth with this change and that org's
next restart, which stops the work running in it. Until then the org keeps SSH only. The `repo add` warning and the
docs are host-side and need no restart.
