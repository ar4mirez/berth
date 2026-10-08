#!/bin/sh
# Fixture entrypoint: authorize the test's key for root (a rootful Podman is root's), start Podman's
# API socket, which compose and the host guard talk to, then sshd.
set -eu
ssh-keygen -A >/dev/null
mkdir -p /root/.ssh /run/podman /run/sshd
printf '%s\n' "$AUTHORIZED_KEY" > /root/.ssh/authorized_keys
chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys
podman system service --time=0 unix:///run/podman/podman.sock &
exec /usr/sbin/sshd -D -E /var/log/sshd.log
