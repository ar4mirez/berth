#!/bin/sh
# Install berth from a signed GitHub release (#60).
#
# Download it, read it, then run it (don't pipe it into a shell):
#   curl -fsSLO https://github.com/ar4mirez/berth/releases/latest/download/install.sh
#   less install.sh
#   sh install.sh [--version vX.Y.Z] [--bin-dir DIR]
#
# What it does, printing each step:
#   1. finds the release (the latest, or --version) and this machine's archive (linux/darwin, amd64/arm64);
#   2. downloads the archive, checksums.txt and its Sigstore bundle;
#   3. verifies the signature: with cosign (checksums.txt, signed by berth's release workflow at that
#      tag), else with gh (the archive's build provenance). With neither, it stops, unless
#      BERTH_INSTALL_CHECKSUM_ONLY=1 says to accept the checksum alone;
#   4. verifies the archive's checksum, and that the binary runs as that version;
#   5. installs it in ~/.local/opt/berth/<version>/ ($BERTH_INSTALL_DIR/<version>/) and links it
#      onto your PATH with berth's own `install` (~/.local/bin, or --bin-dir), with shell completion.
# Later: berth system upgrade (same checks). Nothing is run as root, and nothing outside those paths changes.
set -eu

REPO=ar4mirez/berth
ISSUER=https://token.actions.githubusercontent.com

say() { printf '==> %s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

version="" bindir=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || die "--version needs a value (vX.Y.Z)"; version=$2; shift 2 ;;
    --bin-dir) [ $# -ge 2 ] || die "--bin-dir needs a value"; bindir=$2; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) die "unknown argument: $1 (see: sh install.sh --help)" ;;
  esac
done

for t in curl tar uname mktemp; do have "$t" || die "$t is needed"; done
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "berth runs on Linux and macOS (on Windows: inside WSL2)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "no berth build for $(uname -m)" ;;
esac

if [ -z "$version" ]; then
  say "Finding the latest release"
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || die "can't reach github.com"
  version=${url##*/}
fi
v=${version#v}
tag=v$v
case "$v" in *[!0-9A-Za-z.+-]*|"") die "not a version: $version" ;; esac
archive="berth_${v}_${os}_${arch}.tar.gz"
# BERTH_INSTALL_BASE: a mirror of the release files (<base>/<tag>/…). The signature still has to be
# the release workflow's, at that tag.
base="${BERTH_INSTALL_BASE:-https://github.com/$REPO/releases/download}/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
say "Downloading berth $tag for $os/$arch"
for f in "$archive" checksums.txt checksums.txt.sigstore.json; do
  curl -fsSL -o "$tmp/$f" "$base/$f" || die "can't download $base/$f (is $tag a release?)"
done

if have cosign; then
  say "Verifying the signature on checksums.txt (cosign)"
  cosign verify-blob --new-bundle-format --bundle "$tmp/checksums.txt.sigstore.json" \
    --certificate-identity "https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$tag" \
    --certificate-oidc-issuer "$ISSUER" "$tmp/checksums.txt" >/dev/null 2>&1 \
    || die "the signature doesn't verify: don't install this. (Releases before v0.3.0 use an older format: install those by hand, docs/host-install.md.)"
elif have gh; then
  say "Verifying the archive's build provenance (gh attestation)"
  gh attestation verify "$tmp/$archive" --repo "$REPO" \
    --signer-workflow "$REPO/.github/workflows/release.yml" >/dev/null 2>&1 \
    || die "the archive's provenance doesn't verify: don't install this"
elif [ "${BERTH_INSTALL_CHECKSUM_ONLY:-}" = 1 ]; then
  printf 'install.sh: WARNING: no cosign or gh here, so the signature is NOT checked (BERTH_INSTALL_CHECKSUM_ONLY=1)\n' >&2
else
  die "to check the signature, install cosign (https://docs.sigstore.dev/cosign/system_config/installation/) or gh (https://cli.github.com), then run this again. To accept the checksum alone: BERTH_INSTALL_CHECKSUM_ONLY=1 sh install.sh"
fi

say "Verifying the checksum"
line=""
while IFS= read -r l; do
  case "$l" in *"  $archive") line=$l ;; esac
done < "$tmp/checksums.txt"
[ -n "$line" ] || die "checksums.txt doesn't list $archive"
if have sha256sum; then
  (cd "$tmp" && printf '%s\n' "$line" | sha256sum -c - >/dev/null) || die "$archive doesn't match its checksum: don't install this"
elif have shasum; then
  (cd "$tmp" && printf '%s\n' "$line" | shasum -a 256 -c - >/dev/null) || die "$archive doesn't match its checksum: don't install this"
else
  die "sha256sum or shasum is needed"
fi

tar -xzf "$tmp/$archive" -C "$tmp" berth
case "$("$tmp/berth" --version 2>/dev/null)" in
  *" $v "*) ;;
  *) die "the binary doesn't run as $v" ;;
esac

dir="${BERTH_INSTALL_DIR:-$HOME/.local/opt/berth}/$v"
say "Installing into $dir"
mkdir -p "$dir"
cp "$tmp/berth" "$dir/berth.new"
chmod 0755 "$dir/berth.new"
mv "$dir/berth.new" "$dir/berth"
if [ -n "$bindir" ]; then "$dir/berth" install "$bindir"; else "$dir/berth" install; fi
say "Done: $("$dir/berth" --version). Next: berth org create <org> (https://ar4mirez.github.io/berth/getting-started/)"
