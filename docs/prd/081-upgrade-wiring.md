# PRD: `berth upgrade` works in a released binary (#81)

Status: **done** · Issue: [#81](https://github.com/ar4mirez/berth/issues/81) · Restarts orgs: no

## Problem

`berth upgrade` installs a verified release next to the current one and moves the link (#42). In every released
binary it ended at once:

```console
$ berth upgrade --version v0.4.1
berth: upgrade isn't available in this build
```

`App.Upgrade` needs an `Upgrader`: the client for berth's releases and the Sigstore verifier. Only the tests ever
set one. `appFor`, which builds the App for every command, didn't. `--rollback` worked, because it returns before
the check, and a Homebrew or mise install never reached it, because it is sent to its package manager first.

The issue's second part, `berth host` missing from v0.3.0, was a feature that missed that release: it is in v0.4.0.

## Objectives

1. `berth upgrade [--version vX.Y.Z]` works from an install-script install.
2. A test runs the command-line path, so this can't come back.

## Design

- `internal/cli` has `newUpgrader`: `upgrade.NewClient()` and `upgrade.Sigstore{}`. `appFor` gives it to every App.
  It is a variable so a test can serve a fake release.
- `schedule --host` reads the same field to install this berth's release on a host. With a release build it now
  fetches the signed release for the host's architecture, as #49 designed; before, it fell back to refusing or to
  pushing itself.

## Tasks

- [x] T1. Reproduce with the released v0.4.1 binary.
- [x] T2. Wire the upgrader in `appFor`.
- [x] T3. `TestUpgradeThroughTheCLI`: the CLI path against a fake release (upgrade, `system upgrade --version`, a
      bad signature, `--read-only`). It fails without T2.
- [x] T4. A real upgrade: a build of this branch, the v0.4.1 release from GitHub, Sigstore's public trust root.
- [x] T5. Docs: troubleshooting.

## Acceptance criteria

| Criterion | How it is checked |
|---|---|
| `berth upgrade --version <current>` says `berth is already at <current>.` | `TestUpgradeThroughTheCLI` |
| `berth upgrade` installs a newer verified release and links it | `TestUpgradeThroughTheCLI`; by hand against v0.4.1 |
| A test runs the CLI path, not only `App.Upgrade` with an injected `Upgrader` | `TestUpgradeThroughTheCLI`, `TestUpgraderIsWired` |

## Reaching existing installs

The fix is in the binary, and the binaries already installed can't upgrade themselves. Their one-time path is the
install script, which verifies the same signature:

```bash
curl -fsSLO https://github.com/ar4mirez/berth/releases/latest/download/install.sh && less install.sh && sh install.sh
```

From the release with this fix on, `berth upgrade` works.
