#!/bin/sh
# Fixture entrypoint: authorize the test's key for jump, then run sshd in the foreground.
set -eu
ssh-keygen -A >/dev/null
mkdir -p /home/jump/.ssh
printf '%s\n' "$AUTHORIZED_KEY" > /home/jump/.ssh/authorized_keys
chmod 700 /home/jump/.ssh
chmod 600 /home/jump/.ssh/authorized_keys
chown -R jump:jump /home/jump/.ssh
exec /usr/sbin/sshd -D -e
