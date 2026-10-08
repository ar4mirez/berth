#!/bin/sh
# Fixture entrypoint: authorize the test's key for root (a rootful Podman is root's) and for podman
# (a rootless one), start each one's API socket, which compose and the host guard talk to, then sshd.
set -eu
ssh-keygen -A >/dev/null
mkdir -p /run/podman /run/sshd
for home in /root /home/podman; do
  mkdir -p "$home/.ssh"
  printf '%s\n' "$AUTHORIZED_KEY" > "$home/.ssh/authorized_keys"
  chmod 700 "$home/.ssh"
  chmod 600 "$home/.ssh/authorized_keys"
done
chown -R podman:podman /home/podman/.ssh
podman system service --time=0 unix:///run/podman/podman.sock &
# No login session here, so no XDG_RUNTIME_DIR: Podman puts the user's socket under /tmp, where an
# ssh login finds it too.
su podman -c 'cd /tmp && podman system service --time=0' &
exec /usr/sbin/sshd -D -E /var/log/sshd.log
