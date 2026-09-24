# Test fixture for host/ssh: a remote "host" with sshd and its own Docker daemon (dind).
# Built and run by test/integration/ssh_test.go; never used outside CI.
FROM docker:dind

# ss (iproute2) and flock (util-linux) are what a real berth host is expected to have.
RUN apk add --no-cache openssh-server iproute2 util-linux \
    && command -v flock && command -v ss

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/berth-test-entrypoint
RUN chmod 755 /usr/local/bin/berth-test-entrypoint

EXPOSE 22
ENTRYPOINT ["berth-test-entrypoint"]
