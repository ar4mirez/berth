# Test fixture for the host guard on Podman (#97): a remote "host" with sshd and a rootful Podman
# (netavark, nftables). Built and run by test/integration/guard_test.go; never used outside CI.
FROM quay.io/podman/stable

# What a real berth host has (ss, flock, git, ssh-keygen), sshd, and iptables for the test to read
# the rules back. `podman compose` runs Docker's compose against Podman's socket.
RUN dnf install -y -q openssh-server iproute util-linux git iptables-nft procps-ng \
    && dnf clean all \
    && curl -fsSL -o /usr/local/bin/docker-compose \
       "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-$(uname -m)" \
    && chmod 755 /usr/local/bin/docker-compose \
    && command -v flock && command -v ss && command -v ssh-keygen && docker-compose version

# The image's root account is locked, which sshd refuses even for key logins: unlock it with no
# usable password instead.
RUN usermod -p '*' root

# The image is set up to run containers in its own namespaces (host network, UTS and so on), which
# is no host for an org: back to Podman's defaults, with cgroupfs (there is no systemd here).
RUN printf '[engine]\ncgroup_manager = "cgroupfs"\nevents_logger = "file"\n' > /etc/containers/containers.conf

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/berth-test-entrypoint
RUN chmod 755 /usr/local/bin/berth-test-entrypoint

EXPOSE 22
ENTRYPOINT ["berth-test-entrypoint"]
