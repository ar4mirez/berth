# One image for every org. Org-specific state lives in bind mounts, never in the image.
# Every version below is pinned, and every download is checked against its sha256 before use (#39).
# A bump changes the version and its checksums together (Renovate opens the PR; the checksums come
# from the project's own published checksum file). Debian's packages come from the digest-pinned
# base's signed apt repos.
FROM node:22-bookworm-slim@sha256:43ac6c60b8f89723f746e8a92ce91abd5017e627ce1ddfe4238355d3a30b772c

# renovate: datasource=npm depName=@anthropic-ai/claude-code
ARG CLAUDE_CODE_PINNED=2.1.281
ARG CLAUDE_CODE_INTEGRITY=sha512-sldhFsGqOnGA8pfOMLjyc+Sc331VkZ1TsP1sdpvMeVFYBikC5x53miTCsnTiE6Bio74IYPn7BIzTNbHcRp5fXQ==
# renovate: datasource=github-releases depName=tsl0922/ttyd
ARG TTYD_VERSION=1.7.7
ARG TTYD_SHA256_AMD64=8a217c968aba172e0dbf3f34447218dc015bc4d5e59bf51db2f2cd12b7be4f55
ARG TTYD_SHA256_ARM64=b38acadd89d1d396a0f5649aa52c539edbad07f4bc7348b27b4f4b7219dd4165
# renovate: datasource=github-releases depName=jdx/mise
ARG MISE_VERSION=v2026.9.14
ARG MISE_SHA256_AMD64=2b289d1b3074e0b1d3f95bad0bd78bbc517c1a5bcb020cbc1643d260f5d1a351
ARG MISE_SHA256_ARM64=b405a2ea062c4d9560eee0a3c45b79e53c8834cf38e956bb47ca70e0d2e7479a
# renovate: datasource=github-releases depName=cli/cli
ARG GH_VERSION=v2.101.0
ARG GH_SHA256_AMD64=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8
ARG GH_SHA256_ARM64=b57e8063f18862647c9d22727c32e9da1b963f8bf9db648fe123a6975695640f
# CLAUDE_CODE_VERSION (from org.env, through compose) overrides the pin; empty means the pin.
ARG CLAUDE_CODE_VERSION=
ARG USER_UID=1000
ARG USER_GID=1000
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
      git openssh-client openssh-server tmux tini sudo less vim-tiny \
      iptables ipset iproute2 dnsutils curl ca-certificates jq ripgrep fd-find \
      procps locales unzip xz-utils bzip2 zstd file age gnupg \
      build-essential pkg-config autoconf bison libssl-dev zlib1g-dev libyaml-dev libffi-dev \
      libreadline-dev libgdbm-dev libncurses-dev libsqlite3-dev libbz2-dev liblzma-dev \
    && sed -i 's/# en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen && locale-gen \
    && rm -rf /var/lib/apt/lists/*

# gh: the upstream release (Debian's is years behind and lacks current auth flags).
RUN case "${TARGETARCH:-amd64}" in amd64) sum=$GH_SHA256_AMD64;; arm64) sum=$GH_SHA256_ARM64;; *) echo "unsupported arch" >&2; exit 1;; esac \
    && v="${GH_VERSION#v}" && t="gh_${v}_linux_${TARGETARCH:-amd64}" \
    && curl -fsSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/${GH_VERSION}/$t.tar.gz" \
    && echo "$sum  /tmp/gh.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/gh.tar.gz -C /tmp && install -m 755 "/tmp/$t/bin/gh" /usr/local/bin/gh \
    && rm -rf /tmp/gh.tar.gz "/tmp/$t" && gh --version | head -1

# ttyd isn't packaged for bookworm; use the upstream static build.
RUN case "${TARGETARCH:-amd64}" in amd64) a=x86_64 sum=$TTYD_SHA256_AMD64;; arm64) a=aarch64 sum=$TTYD_SHA256_ARM64;; *) echo "unsupported arch" >&2; exit 1;; esac \
    && curl -fsSL -o /usr/local/bin/ttyd "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.$a" \
    && echo "$sum  /usr/local/bin/ttyd" | sha256sum -c - \
    && chmod +x /usr/local/bin/ttyd && ttyd --version

# mise: lets Claude install toolchains on demand (go, python/uv, ruby, rust, java...).
# Build deps above cover tools mise has to compile (e.g. ruby).
RUN case "${TARGETARCH:-amd64}" in amd64) a=x64 sum=$MISE_SHA256_AMD64;; arm64) a=arm64 sum=$MISE_SHA256_ARM64;; *) echo "unsupported arch" >&2; exit 1;; esac \
    && curl -fsSL -o /usr/local/bin/mise "https://github.com/jdx/mise/releases/download/${MISE_VERSION}/mise-${MISE_VERSION}-linux-$a" \
    && echo "$sum  /usr/local/bin/mise" | sha256sum -c - \
    && chmod +x /usr/local/bin/mise && mise --version

# Match the host user's UID/GID so bind-mounted files stay owned by you.
RUN groupmod -o -g ${USER_GID} node && usermod -o -u ${USER_UID} -g ${USER_GID} node \
    && usermod -p '*' node \
    && mkdir -p /workspace /home/node/.claude /home/node/.mise /run/sshd /etc/ssh/authorized_keys \
    && chown -R node:node /workspace /home/node

# The pinned version: the registry's integrity for it must be the pinned one, and npm then checks
# the tarball against it. A version set in org.env is installed as asked (npm still verifies it).
RUN v="${CLAUDE_CODE_VERSION:-$CLAUDE_CODE_PINNED}" \
    && if [ "$v" = "$CLAUDE_CODE_PINNED" ]; then \
         test "$(npm view "@anthropic-ai/claude-code@$v" dist.integrity)" = "${CLAUDE_CODE_INTEGRITY}" \
           || { echo "claude-code $v: registry integrity differs from the pin" >&2; exit 1; }; \
       fi \
    && npm install -g "@anthropic-ai/claude-code@$v" && npm cache clean --force

COPY sshd_config /etc/ssh/sshd_config.d/claude-env.conf
COPY tmux.conf /etc/tmux.conf
COPY init-firewall.sh entrypoint.sh /usr/local/bin/
COPY bashrc.sh /etc/profile.d/claude-env.sh
# Enforced for every session (incl. ones spawned from claude.ai): latest Opus, no permission prompts.
COPY managed-settings.json /etc/claude-code/managed-settings.json
# Org-independent instructions every session loads (how to install tools, firewall, layout).
COPY CLAUDE.md /etc/claude-code/CLAUDE.md
RUN chmod +x /usr/local/bin/*.sh \
    && sed -i '1i . /etc/profile.d/claude-env.sh  # before the non-interactive early return' /home/node/.bashrc

# Repo allowlist. The org's git key is mounted under a root-only dir; `node` reaches GitHub only through
# git-ssh-guard (via a single sudo rule), which checks every fetch/push against /config/repos.txt.
# repo-guard.js is the Claude-side PreToolUse hook (wired in managed-settings.json).
COPY repo-policy.sh repo-guard.js /usr/local/lib/claude-env/
COPY git-ssh-guard /usr/local/bin/git-ssh-guard
RUN chmod 755 /usr/local/bin/git-ssh-guard && chmod 644 /usr/local/lib/claude-env/* \
    && install -d -m 700 -o root -g root /opt/claude-secrets \
    && printf '%s\n' 'Defaults!/usr/local/bin/git-ssh-guard env_keep += "GIT_PROTOCOL"' \
         'node ALL=(root) NOPASSWD: /usr/local/bin/git-ssh-guard' > /etc/sudoers.d/git-ssh-guard \
    && chmod 440 /etc/sudoers.d/git-ssh-guard && visudo -cf /etc/sudoers.d/git-ssh-guard \
    && git config --system core.sshCommand "sudo -n /usr/local/bin/git-ssh-guard" \
    && git config --system ssh.variant ssh \
    && git config --system url."git@github.com:".insteadOf "https://github.com/" \
    && git config --system --add url."git@github.com:".insteadOf "http://github.com/" \
    && git config --system url."git@gitlab.com:".insteadOf "https://gitlab.com/" \
    && git config --system url."git@bitbucket.org:".insteadOf "https://bitbucket.org/"

ENV LANG=en_US.UTF-8 \
    SHELL=/bin/bash \
    CLAUDE_CONFIG_DIR=/home/node/.claude \
    DISABLE_AUTOUPDATER=1 \
    ANTHROPIC_MODEL=opus \
    EDITOR=vi \
    MISE_DATA_DIR=/home/node/.mise/data \
    MISE_CONFIG_DIR=/home/node/.mise/config \
    MISE_CACHE_DIR=/home/node/.mise/cache \
    MISE_STATE_DIR=/home/node/.mise/state \
    MISE_YES=1 \
    MISE_TRUSTED_CONFIG_PATHS=/workspace \
    CARGO_HOME=/home/node/.mise/cargo \
    RUSTUP_HOME=/home/node/.mise/rustup \
    GOPATH=/home/node/.mise/go \
    XDG_CACHE_HOME=/home/node/.mise/xdg-cache \
    PATH=/home/node/.mise/data/shims:/home/node/.mise/cargo/bin:/home/node/.mise/go/bin:/home/node/.local/bin:$PATH

WORKDIR /workspace
EXPOSE 2222 7681
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
