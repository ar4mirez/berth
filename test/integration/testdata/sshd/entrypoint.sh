#!/bin/sh
# Fixture entrypoint: authorize the test's key (for root and ops), start sshd, then hand over to dind.
set -eu
ssh-keygen -A >/dev/null
for home in /root /home/ops; do
  mkdir -p "$home/.ssh"
  printf '%s\n' "$AUTHORIZED_KEY" > "$home/.ssh/authorized_keys"
  chmod 700 "$home/.ssh"
  chmod 600 "$home/.ssh/authorized_keys"
done
chown -R ops:ops /home/ops/.ssh
/usr/sbin/sshd -E /var/log/sshd.log
exec dockerd-entrypoint.sh "$@"
