# PRD: distribution, the remaining channels (#60)

Status: **done**, with one step left to the maintainer (the aqua pull request) · Issue: [#60](https://github.com/ar4mirez/berth/issues/60) · Restarts orgs: no

## Where it stood

#93 shipped most of the issue: the Homebrew cask, the `.deb`, `.rpm` and Arch packages, the AUR packages, mise
(`ubi:`), the verified install script, and a workflow that installs each channel on a clean machine. Three things
were left.

| Left | Now |
|---|---|
| Signed apt and yum repositories | Built and signed by the docs deploy; at `https://ar4mirez.github.io/berth/apt` and `/rpm` |
| An aqua registry entry | Written and tested; submitting it is the maintainer's (below) |
| Windows | Decided: WSL2 with the Linux build; no native Windows build |

## Decisions (2026-10-07)

- **Hosting: the docs site.** No new repository, account or access token; only a signing key.
- **The signing key** was generated for this purpose (RSA 4096, no expiry, no passphrase) and is the
  `PACKAGES_GPG_KEY` Actions secret. Its public half is `tools/pkgrepo/berth-packages.asc`. Fingerprint:
  `B377 15D1 5535 BEC5 35AC  1C8F 2DD9 5203 64B7 0A65`.
- **aqua: prepared, not submitted.** The entry goes to someone else's repository, from the maintainer's account.
- **Windows: WSL2 only.** berth drives Linux containers with bind mounts and SSH; `docs/getting-started.md` says so.

## Design

### The repositories

- **Stateless.** The docs workflow, on `main`, downloads the `.deb` and `.rpm` packages of the latest five releases
  (`tools/pkgrepo/fetch.sh`), builds both repositories (`tools/pkgrepo/build.sh`) into the site, and signs them. No
  branch or bucket holds state: the releases are the source.
- **Verified first.** Each release's `checksums.txt` is checked with berth's own release verifier (its Sigstore
  signature, by the release workflow at that tag), and each package against it, before it can enter a repository.
- **What is signed** is each repository's index: apt's `Release` (`InRelease`, `Release.gpg`) and yum's
  `repomd.xml`. The index lists every package's checksum, so apt and dnf (`repo_gpgcheck=1`) refuse a package that
  isn't the released one. The packages carry no signature of their own.
- **A release publishes them:** the release workflow ends by running the docs workflow.
- **The key that signs must be the committed one:** `build.sh` refuses a keyring whose secret key doesn't match
  `berth-packages.asc`.

### Checked in CI

- `packages.yml`, job `repository`: `tools/pkgrepo/test.sh` builds both repositories from the snapshot's packages
  with a throwaway key, installs berth from them on Debian, Ubuntu and Fedora, and checks that a repository changed
  after it was signed is refused by apt and by dnf.
- `packages.yml`, job `repository-published` (when the `PKG_REPO` repository variable is `true`): adds the published
  repositories exactly as the docs say, and checks that the latest release is what installs.

## Tasks

- [x] T1. `tools/pkgrepo/build.sh`, `fetch.sh`, `test.sh`.
- [x] T2. The signing key: generated, stored as a secret, the public half committed.
- [x] T3. `docs.yml` publishes the repositories; `release.yml` runs it after a release.
- [x] T4. `packages.yml`: the `repository` and `repository-published` jobs.
- [x] T5. Docs: the Debian and Fedora tabs; `berth upgrade`'s hint for a system package.
- [x] T6. The aqua registry entry, tested with aqua.
- [x] T7. Windows recorded as decided.
- [ ] T8. After the first deploy: check the published repositories, then set `PKG_REPO=true`.
- [ ] T9. Maintainer: submit the aqua entry.

## Acceptance criteria

| Criterion (from the issue) | How it is checked |
|---|---|
| Each channel installs the current release on a clean machine in CI | `packages.yml`: native packages, the install script, mise, Homebrew, the AUR, and now the apt and yum repositories |
| Package metadata points at the docs and the license | `.goreleaser.yaml` (`homepage`, `license`), unchanged; the `native` job checks the license file is installed |

## The aqua registry entry

For `pkgs/ar4mirez/berth/registry.yaml` in [aquaproj/aqua-registry](https://github.com/aquaproj/aqua-registry),
with a `pkg.yaml` beside it (`packages: [{name: ar4mirez/berth@v<latest>}]`), as that repository's contributing
guide asks. Tested with aqua 2.63.0 as a local registry: it installs v0.4.1 and the binary runs. Releases before
v0.4.0 use another archive layout, so the entry starts there.

```yaml
packages:
  - type: github_release
    repo_owner: ar4mirez
    repo_name: berth
    description: Isolated Claude Code environments, one per organization
    version_constraint: "false"
    version_overrides:
      - version_constraint: semver(">= 0.4.0")
        asset: berth_{{trimV .Version}}_{{.OS}}_{{.Arch}}.{{.Format}}
        format: tar.gz
        checksum:
          type: github_release
          asset: checksums.txt
          algorithm: sha256
        supported_envs:
          - darwin
          - linux
```

With it merged there, `mise use -g aqua:ar4mirez/berth` works and verifies the release's checksum.

## Not done

- **Packages signed one by one** (`rpm --addsign`, `dpkg-sig`). The signed index covers them; signing each would
  need the key in the release workflow too.
- **A Homebrew core formula, an official Arch repository.** The issue marks both as later.
