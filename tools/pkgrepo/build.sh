#!/usr/bin/env bash
# build.sh <packages-dir> <out-dir> [base-url]: build berth's signed apt and yum repositories
# (#60) from the .deb and .rpm files in <packages-dir>, into <out-dir>/apt and <out-dir>/rpm.
#
#   <out>/apt/dists/stable/…        Release, InRelease and Release.gpg, signed; main, amd64 and arm64
#   <out>/apt/pool/main/b/berth/…   the .deb files
#   <out>/apt/berth.gpg, berth.asc  the public key, for /etc/apt/keyrings
#   <out>/rpm/<arch>/…              the .rpm files and repodata; repomd.xml.asc is its signature
#   <out>/rpm/berth.repo            for /etc/yum.repos.d; RPM-GPG-KEY-berth is the public key
#
# The repositories' metadata is signed, and lists each package's checksum, so apt and dnf refuse a
# package that isn't the one the release published. The secret key must be in gpg's keyring
# (GNUPGHOME) and be the one whose public half is tools/pkgrepo/berth-packages.asc: PKGREPO_PUBKEY
# names another file (tests sign with a throwaway key).
# Needs gpg, apt-ftparchive (apt-utils) and createrepo_c.
set -euo pipefail

pkgs="${1:?usage: build.sh <packages-dir> <out-dir> [base-url]}"
out="${2:?usage: build.sh <packages-dir> <out-dir> [base-url]}"
base="${3:-https://ar4mirez.github.io/berth}"
pub="${PKGREPO_PUBKEY:-$(dirname "$0")/berth-packages.asc}"

# The key users are told to trust must be the one that signs.
fpr=$(gpg --show-keys --with-colons "$pub" | awk -F: '$1=="fpr"{print $10; exit}')
[ -n "$fpr" ] || { echo "$pub isn't a public key" >&2; exit 1; }
gpg --list-secret-keys "$fpr" >/dev/null 2>&1 || { echo "the secret key for $fpr ($pub) isn't in gpg's keyring" >&2; exit 1; }
sign() { gpg --batch --yes --local-user "$fpr" "$@"; }

rm -rf "$out/apt" "$out/rpm"

# --- apt ---------------------------------------------------------------------------------------
apt="$out/apt"; pool="$apt/pool/main/b/berth"
mkdir -p "$pool"
cp "$pkgs"/*.deb "$pool/"
for arch in amd64 arm64; do
  d="$apt/dists/stable/main/binary-$arch"; mkdir -p "$d"
  (cd "$apt" && apt-ftparchive --arch "$arch" packages pool) > "$d/Packages"
  [ -s "$d/Packages" ] || { echo "no .deb for $arch" >&2; exit 1; }
  gzip -9 -k -n "$d/Packages"
done
(cd "$apt/dists/stable" && apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=berth -o APT::FTPArchive::Release::Label=berth \
  -o APT::FTPArchive::Release::Suite=stable -o APT::FTPArchive::Release::Codename=stable \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64" -o APT::FTPArchive::Release::Components=main \
  -o APT::FTPArchive::Release::Description="berth: isolated Claude Code environments, one per organization" \
  release . > Release)
sign --clearsign -o "$apt/dists/stable/InRelease" "$apt/dists/stable/Release"
sign --armor --detach-sign -o "$apt/dists/stable/Release.gpg" "$apt/dists/stable/Release"
cp "$pub" "$apt/berth.asc"
gpg --dearmor < "$pub" > "$apt/berth.gpg"

# --- yum / dnf ---------------------------------------------------------------------------------
rpm="$out/rpm"
for arch in x86_64 aarch64; do
  mkdir -p "$rpm/$arch"
  cp "$pkgs"/*."$arch".rpm "$rpm/$arch/"
  createrepo_c --quiet "$rpm/$arch"
  sign --armor --detach-sign -o "$rpm/$arch/repodata/repomd.xml.asc" "$rpm/$arch/repodata/repomd.xml"
done
cp "$pub" "$rpm/RPM-GPG-KEY-berth"
# The packages themselves carry no signature: the signed metadata lists their checksums, so
# repo_gpgcheck is what protects them.
cat > "$rpm/berth.repo" <<REPO
[berth]
name=berth
baseurl=$base/rpm/\$basearch
enabled=1
repo_gpgcheck=1
gpgcheck=0
gpgkey=$base/rpm/RPM-GPG-KEY-berth
REPO

echo "apt: $(ls "$pool" | wc -l | tr -d ' ') packages; rpm: $(ls "$rpm"/*/*.rpm | wc -l | tr -d ' ') packages; signed by $fpr"
