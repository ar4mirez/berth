# Releases

berth is released from tags. Each release is signed and has build provenance.

## Versions

berth uses semantic versioning: `vMAJOR.MINOR.PATCH`.
- **While berth is below 1.0, a MINOR bump may change behaviour.** PARITY.md records every intended difference from
  ccenv, and the release notes call out anything that touches the image, since that needs a restart of each org to
  take effect.
- **A tag with a suffix is marked as a pre-release** (`v0.2.0-rc.1`, for example).
- **Snapshots** (CI builds of `main`) are `<next>-snapshot+<commit>`.

## What a release contains

- `berth_<version>_<os>_<arch>.tar.gz` for linux and darwin, amd64 and arm64.
- `checksums.txt`: the sha256 of every archive.
- `checksums.txt.sigstore.json`: a keyless cosign signature over `checksums.txt`. Its certificate names
  `.github/workflows/release.yml` at that release's tag, in `ar4mirez/berth`.
- A build-provenance attestation (SLSA) for each archive and for `checksums.txt`, stored with GitHub.
- Release notes generated from the merged PRs.

## Verifying a release

```bash
v=v0.1.0                                   # the release you're installing
gh release download "$v" -R ar4mirez/berth -D berth-"$v" && cd berth-"$v"

# 1. The checksums were signed by berth's release workflow, at that tag.
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/ar4mirez/berth/.github/workflows/release.yml@refs/tags/$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

# 2. The archive matches its checksum.
sha256sum -c --ignore-missing checksums.txt        # macOS: shasum -a 256 -c --ignore-missing checksums.txt

# 3. Optional: its provenance (which workflow and commit built it).
gh attestation verify berth_*_linux_amd64.tar.gz -R ar4mirez/berth
```

Steps 1 and 2 together cover every archive. `docs/host-install.md` puts them in the install steps.

## Cutting a release (maintainers)

1. `main` is green in `ci` and `integration`.
2. Tag and push:
   ```bash
   git tag -a v0.1.0 -m v0.1.0 && git push origin v0.1.0
   ```
3. The `release` workflow does the rest, in order:
   - builds the archives;
   - signs `checksums.txt`;
   - publishes the release;
   - verifies the signature and the checksums, as a user would;
   - attests provenance.

Pull requests that change `.goreleaser.yaml` or the release workflow run the same pipeline as a dry run. It builds,
signs and verifies, and publishes nothing. `gh workflow run release` runs the dry run by hand.

## Releases that change the image

A release whose image changed reaches each org only at that org's next restart. `docs/image-update.md` rolls one out:
one org at a time, each restart approved, verified and undoable. Its release notes list the image changes.
