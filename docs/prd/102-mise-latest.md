# PRD: mise installs the latest release (#102)

Status: **done** · Issue: [#102](https://github.com/ar4mirez/berth/issues/102) · Restarts orgs: no

## Problem

Right after v0.4.1, the packages workflow's `mise use -g ubi:ar4mirez/berth` installed 0.3.0, while the install
script and Homebrew installed 0.4.1. The job only printed the version, so it passed.

## Findings

- **It was a cache, and it has cleared.** On 2026-10-06 `mise ls-remote ubi:ar4mirez/berth` lists 0.4.1 as the
  latest, with and without mise's versions host. mise keeps each tool's list of versions on that host, and it can
  lag a new release by up to a day.
- **It wasn't ubi's asset selection.** With the versions host off, mise installed 0.4.1 from the release's assets
  (the source archive, `.deb`, `.rpm` and `.pkg.tar.zst` are there too), and the binary runs.

## Objectives

1. The workflow fails when mise can't install the latest release, and doesn't fail for a cache berth doesn't own.
2. A user who hits the lag knows what it is and how to get the release now.

## Tasks

- [x] T1. Check what mise resolves today, with and without the versions host.
- [x] T2. `packages.yml`: `MISE_USE_VERSIONS_HOST=0`, and assert the installed version is the latest release's.
- [x] T3. `packages.yml`: what the versions host lists by default, as a warning when it lags.
- [x] T4. `docs/getting-started.md`: the lag, and the two ways around it.

## Acceptance criteria

| Criterion (the issue's to-do list) | How it is checked |
|---|---|
| Re-run a day later; if still 0.3.0, look at ubi's asset selection | T1: 0.4.1 now, and installed correctly with the versions host off |
| CI lists releases from GitHub directly and asserts the version is the latest | The `mise` job's first step; the workflow runs on this PR |
| The docs say mise may lag, and how to pin | `docs/getting-started.md`, the mise tab |
