# SSH logins don't inherit the container env, so the entrypoint snapshots it here.
if [ -r /etc/claude-env/env ]; then
  set -a; . /etc/claude-env/env; set +a
fi
if [ -r /etc/claude-env/org ]; then
  PS1="\[\e[35m\]($(cat /etc/claude-env/org))\[\e[0m\] \w \$ "
fi
# mise lives in the persisted ~/.mise; shims make tools work in non-interactive shells too.
export MISE_DATA_DIR=/home/node/.mise/data MISE_CONFIG_DIR=/home/node/.mise/config \
       MISE_CACHE_DIR=/home/node/.mise/cache MISE_STATE_DIR=/home/node/.mise/state \
       MISE_YES=1 MISE_TRUSTED_CONFIG_PATHS=/workspace \
       CARGO_HOME=/home/node/.mise/cargo RUSTUP_HOME=/home/node/.mise/rustup \
       GOPATH=/home/node/.mise/go XDG_CACHE_HOME=/home/node/.mise/xdg-cache
case ":$PATH:" in *":$MISE_DATA_DIR/shims:"*) ;; *)
  export PATH="$MISE_DATA_DIR/shims:$CARGO_HOME/bin:$GOPATH/bin:$HOME/.local/bin:$PATH" ;; esac
case $- in *i*) eval "$(mise activate bash)" ;; esac
alias ll='ls -la'
alias fd='fdfind'
