#!/bin/sh
# Fixture entrypoint: authorize the test's key for root (a rootful Podman is root's) and for podman
# (a rootless one), start each one's API socket, which compose and the host guard talk to, then sshd.
set -eu
ssh-keygen -A >/dev/null
# cgroup v2 delegation for the rootless Podman (#153): crun refuses an org's limits (memory, cpus,
# pids) when the user can't create cgroups. As DinD does: move every process out of this
# namespace's root cgroup into init/, enable the controllers below the root, and hand the root to
# podman, so its containers get cgroups of their own. Rootful Podman (root) is unaffected.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  mkdir -p /sys/fs/cgroup/init
  xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs 2>/dev/null || :
  sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers > /sys/fs/cgroup/cgroup.subtree_control
  chown podman:podman /sys/fs/cgroup /sys/fs/cgroup/cgroup.procs /sys/fs/cgroup/cgroup.subtree_control /sys/fs/cgroup/cgroup.threads
fi
mkdir -p /run/podman /run/sshd
for home in /root /home/podman; do
  mkdir -p "$home/.ssh"
  printf '%s\n' "$AUTHORIZED_KEY" > "$home/.ssh/authorized_keys"
  chmod 700 "$home/.ssh"
  chmod 600 "$home/.ssh/authorized_keys"
done
chown -R podman:podman /home/podman/.ssh
podman system service --time=0 unix:///run/podman/podman.sock &
# No login session (no systemd-logind) to make the user's runtime dir, so make it as logind would:
# /run/user/<uid>, 0700. The service and every podman login (sshd_config's SetEnv) use it, so they
# share one rootless runtime state (#215).
install -d -m 700 -o podman -g podman /run/user/1000
su podman -c 'cd /tmp && XDG_RUNTIME_DIR=/run/user/1000 podman system service --time=0' &
exec /usr/sbin/sshd -D -E /var/log/sshd.log
