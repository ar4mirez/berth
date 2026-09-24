# One image for every org. Org-specific state lives in bind mounts, never in the image.
FROM node:22-bookworm-slim

ARG CLAUDE_CODE_VERSION=latest
ARG USER_UID=1000
ARG USER_GID=1000
ARG TTYD_VERSION=1.7.7
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
      git openssh-client openssh-server tmux tini sudo less vim-tiny \
      iptables ipset iproute2 dnsutils curl ca-certificates jq ripgrep fd-find \
      procps locales unzip xz-utils bzip2 zstd file age gnupg \
      build-essential pkg-config autoconf bison libssl-dev zlib1g-dev libyaml-dev libffi-dev \
      libreadline-dev libgdbm-dev libncurses-dev libsqlite3-dev libbz2-dev liblzma-dev \
    && sed -i 's/# en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen && locale-gen \
    && rm -rf /var/lib/apt/lists/*

# gh from GitHub's own apt repo (Debian's is years behind and lacks current auth flags).
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli.gpg] https://cli.github.com/packages stable main" \
       > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh && rm -rf /var/lib/apt/lists/* && gh --version | head -1

# ttyd isn't packaged for bookworm; use the upstream static build.
RUN case "${TARGETARCH:-amd64}" in amd64) a=x86_64;; arm64) a=aarch64;; *) echo "unsupported arch" >&2; exit 1;; esac \
    && curl -fsSL -o /usr/local/bin/ttyd "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.$a" \
    && chmod +x /usr/local/bin/ttyd && ttyd --version

# mise: lets Claude install toolchains on demand (go, python/uv, ruby, rust, java...).
# Build deps above cover tools mise has to compile (e.g. ruby).
RUN curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh && mise --version

# Match the host user's UID/GID so bind-mounted files stay owned by you.
RUN groupmod -o -g ${USER_GID} node && usermod -o -u ${USER_UID} -g ${USER_GID} node \
    && usermod -p '*' node \
    && mkdir -p /workspace /home/node/.claude /home/node/.mise /run/sshd /etc/ssh/authorized_keys \
    && chown -R node:node /workspace /home/node

RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} && npm cache clean --force

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
