# Test fixture for host/ssh: a remote "host" with sshd and its own Docker daemon (dind).
# Built and run by test/integration/ssh_test.go; never used outside CI.
FROM docker:dind

# ss (iproute2) and flock (util-linux) are what a real berth host is expected to have; git and
# ssh-keygen are what init runs there.
RUN apk add --no-cache openssh-server iproute2 util-linux git \
    && command -v flock && command -v ss && command -v ssh-keygen

# ops: a plain user in the docker group, as on a real host (the root login stays for the host/ssh
# tests). adduser -D locks the account, which sshd refuses even for key logins: unlock it with no
# usable password instead.
RUN addgroup -S docker 2>/dev/null; adduser -D -u 1000 -s /bin/sh ops && addgroup ops docker \
    && sed -i 's/^ops:!/ops:*/' /etc/shadow

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/berth-test-entrypoint
RUN chmod 755 /usr/local/bin/berth-test-entrypoint

EXPOSE 22
ENTRYPOINT ["berth-test-entrypoint"]
