#!/usr/bin/env bash
# test.sh <packages-dir>: build the apt and yum repositories from the packages in <packages-dir>
# with a throwaway signing key, then install berth from them on clean Debian, Ubuntu and Fedora
# containers, as docs/getting-started.md tells users to, and check that a repository changed after
# it was signed is refused (#60). Needs docker. The packages must be for this machine's
# architecture too.
set -euo pipefail

pkgs=$(cd "${1:?usage: test.sh <packages-dir>}" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d "${TMPDIR_PKGREPO:-${TMPDIR:-/tmp}}/pkgrepo.XXXXXX"); trap 'rm -rf "$work"' EXIT
chmod 755 "$work"

echo "== build (ubuntu: apt-utils, createrepo-c, gnupg), signed with a throwaway key"
docker run --rm -v "$pkgs:/pkgs:ro" -v "$here:/tool:ro" -v "$work:/work" ubuntu:24.04 bash -euc '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq && apt-get install -y -qq apt-utils createrepo-c gnupg >/dev/null
  export GNUPGHOME=/tmp/gnupg; mkdir -m 700 $GNUPGHOME
  gpg --batch --quiet --passphrase "" --quick-generate-key "berth packages test <test@example.com>" rsa3072 sign never
  gpg --armor --export > /work/test.asc
  PKGREPO_PUBKEY=/work/test.asc /tool/build.sh /pkgs /work/repo file:///repo
  # The committed key must be refused when another one signs.
  if [ -f /tool/berth-packages.asc ] && /tool/build.sh /pkgs /work/other file:///repo 2>/dev/null; then
    echo "built with a key that is not the committed one"; exit 1
  fi
  # Built as root in here: hand it back to whoever owns the directory, so they can remove it.
  chown -R "$(stat -c %u:%g /work)" /work
  chmod -R a+rX /work/repo'

apt_install='
  set -eu
  export DEBIAN_FRONTEND=noninteractive
  install -d -m 755 /etc/apt/keyrings && cp /repo/apt/berth.gpg /etc/apt/keyrings/berth.gpg
  echo "deb [signed-by=/etc/apt/keyrings/berth.gpg] file:///repo/apt stable main" > /etc/apt/sources.list.d/berth.list
  apt-get update -qq -o Dir::Etc::sourcelist=/etc/apt/sources.list.d/berth.list -o Dir::Etc::sourceparts=- 
  apt-get install -y -qq berth >/dev/null
  berth --version'
for image in debian:stable-slim ubuntu:24.04; do
  echo "== apt install berth ($image)"
  docker run --rm -v "$work/repo:/repo:ro" "$image" sh -c "$apt_install"
done

echo "== dnf install berth (fedora)"
docker run --rm -v "$work/repo:/repo:ro" fedora:latest sh -euc '
  cp /repo/rpm/berth.repo /etc/yum.repos.d/berth.repo
  grep -q "^repo_gpgcheck=1$" /etc/yum.repos.d/berth.repo
  dnf install -y -q --repo berth berth >/dev/null
  berth --version'

echo "== a repository changed after it was signed is refused"
cp -R "$work/repo" "$work/bad"
# Both forms of the index, as someone serving another package would have to change them: neither
# matches the checksums in the signed Release any more.
for a in amd64 arm64; do
  f="$work/bad/apt/dists/stable/main/binary-$a/Packages"
  sed -i.orig 's/^Version: .*/Version: 99.0.0/' "$f" && rm -f "$f.orig"
  gzip -9 -n -c "$f" > "$f.gz"
done
if docker run --rm -v "$work/bad:/repo:ro" debian:stable-slim sh -c "$apt_install" >/dev/null 2>&1; then
  echo "apt installed from a tampered repository"; exit 1
fi
for a in x86_64 aarch64; do echo "<!-- tampered -->" >> "$work/bad/rpm/$a/repodata/repomd.xml"; done
if docker run --rm -v "$work/bad:/repo:ro" fedora:latest sh -euc 'cp /repo/rpm/berth.repo /etc/yum.repos.d/; dnf install -y -q --repo berth berth' >/dev/null 2>&1; then
  echo "dnf installed from a tampered repository"; exit 1
fi
echo "ok: berth installs from the signed apt and yum repositories; tampered ones are refused"
