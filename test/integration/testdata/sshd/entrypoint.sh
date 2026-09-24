#!/bin/sh
# Fixture entrypoint: authorize the test's key, start sshd, then hand over to dind.
set -eu
ssh-keygen -A >/dev/null
mkdir -p /root/.ssh
chmod 700 /root/.ssh
printf '%s\n' "$AUTHORIZED_KEY" > /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
/usr/sbin/sshd -E /var/log/sshd.log
exec dockerd-entrypoint.sh "$@"
