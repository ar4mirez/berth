# Test fixture for jump hosts (#99): a bastion with nothing but sshd, in front of the sshd + dind
# host on a network the test machine can't reach. Built and run by test/integration/jump_test.go.
FROM alpine:3.22

# jump: a plain user (adduser -D locks the account, which sshd refuses even for key logins: unlock
# it with no usable password instead).
RUN apk add --no-cache openssh-server openssh-client \
    && adduser -D -s /bin/sh jump && sed -i 's/^jump:!/jump:*/' /etc/shadow

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/berth-test-entrypoint
RUN chmod 755 /usr/local/bin/berth-test-entrypoint

EXPOSE 22
ENTRYPOINT ["berth-test-entrypoint"]
