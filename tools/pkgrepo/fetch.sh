#!/usr/bin/env bash
# fetch.sh <dir> [releases]: download the .deb and .rpm packages of berth's latest releases (5 by
# default) into <dir>, for the apt and yum repositories (#60). Each release's checksums.txt is
# verified with berth's own release verifier (its Sigstore signature, by the release workflow at
# that tag), and each package against it: nothing unverified reaches the repositories.
# Needs gh (GH_TOKEN) and go; run from the repo's root.
set -euo pipefail

dir="${1:?usage: fetch.sh <dir> [releases]}"
keep="${2:-5}"
repo=ar4mirez/berth
mkdir -p "$dir"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT

n=0
for tag in $(gh release list -R "$repo" --exclude-drafts --exclude-pre-releases --limit 30 --json tagName -q '.[].tagName'); do
  [ "$n" -lt "$keep" ] || break
  # Releases before the packages existed (v0.4.0) have none: they don't count.
  assets=$(gh release view "$tag" -R "$repo" --json assets -q '.assets[].name')
  grep -qE '\.(deb|rpm)$' <<<"$assets" || continue
  d="$tmp/$tag"; mkdir -p "$d"
  gh release download "$tag" -R "$repo" -D "$d" -p '*.deb' -p '*.rpm' -p checksums.txt -p checksums.txt.sigstore.json
  go run ./tools/verifyrelease "$d/checksums.txt" "$d/checksums.txt.sigstore.json" \
    "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$tag" >/dev/null
  count=0
  for f in "$d"/*.deb "$d"/*.rpm; do
    sum=$(sha256sum "$f" | cut -d' ' -f1)
    grep -qx "$sum  $(basename "$f")" "$d/checksums.txt" || { echo "$tag: $(basename "$f") doesn't match the signed checksums.txt" >&2; exit 1; }
    cp "$f" "$dir/"
    count=$((count + 1))
  done
  echo "$tag: $count packages, verified"
  n=$((n + 1))
done
[ "$n" -gt 0 ] || { echo "no release has packages" >&2; exit 1; }
