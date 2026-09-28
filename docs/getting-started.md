# Getting started

## What you need

- **An OS:** Linux (amd64 or arm64), or macOS (Apple silicon or Intel).
- **Docker** with the compose plugin (`docker compose version`). On macOS, Docker Desktop or any engine that provides
  the Docker API and `docker compose`.
- **Optional: [Tailscale](https://tailscale.com).** With it, each org's SSH and browser terminal are bound to this
  machine's Tailscale address, so you can reach them from your other devices and nobody else can. Without it, they're
  bound to `127.0.0.1`.
- **Optional: `gh`,** signed in on this machine. `berth repo new` uses it to create repos on GitHub.

## Install berth

berth ships as signed release archives on GitHub. Package managers (Homebrew, apt/rpm, AUR, mise) are planned (#60).

```bash
v=$(gh release view -R ar4mirez/berth --json tagName -q .tagName)    # the latest release, e.g. v0.4.0
dl=$(mktemp -d); gh release download "$v" -R ar4mirez/berth -D "$dl"

# The signature over checksums.txt, then the archive's checksum (cosign: https://docs.sigstore.dev)
cosign verify-blob --new-bundle-format --bundle "$dl/checksums.txt.sigstore.json" \
  --certificate-identity "https://github.com/ar4mirez/berth/.github/workflows/release.yml@refs/tags/$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com "$dl/checksums.txt"
(cd "$dl" && sha256sum -c --ignore-missing checksums.txt)          # macOS: shasum -a 256 -c --ignore-missing

os=$(uname -s | tr A-Z a-z); arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
tar -xzf "$dl"/berth_*_"${os}_${arch}".tar.gz -C "$dl" berth
mkdir -p ~/.local/opt/berth/"${v#v}" && install -m 0755 "$dl/berth" ~/.local/opt/berth/"${v#v}"/berth
~/.local/opt/berth/"${v#v}"/berth install     # links ~/.local/bin/berth, with shell completion
berth --version
```

**Later upgrades:** `berth upgrade` fetches the next release, checks the same signature and checksum, and installs it
next to the current one. It restarts nothing, and `berth upgrade --rollback` goes back.
[Releases](releases.md) has the details.

**Man pages:** every release archive includes them (`man/berth.1` and one page per command).

## Your first org

```bash
berth init acme --name "Ada Lovelace" --email ada@example.com
berth up acme                   # pulls berth's image (or builds it), then starts the container
berth auth acme                 # sign in: the Claude token, then the Remote Control login
```

`berth auth` runs inside the container and prints a sign-in link. **Open it in a private browser window**, or a
browser profile for that org, and sign in with that org's Claude account. The rest of your browser, your own `claude`
and your other orgs are never touched. `berth whoami` shows which account each org uses.

Then give the org access to GitHub:

1. **Add the org's key.** Put the org's public key on GitHub, as an account key or a deploy key. `berth info acme`
   prints it (`Git public key: ssh-ed25519 …`). With `gh`:
   `gh ssh-key add ~/.local/share/berth/orgs/acme/ssh/id_ed25519.pub --title claude-acme`.
2. **Optional: `berth gh-login acme`.** It signs in the `gh` CLI inside the container, for PRs, issues and the API.
3. **Register the repos the org may use:** `berth repo add acme acme/widgets` registers the repo and clones it into
   `/workspace/widgets`. Nothing else can be cloned or kept there (see [Repos](guides/repos.md)).

## Connecting

`berth info acme` prints every way in, with this org's ports and addresses:

| From | How |
|---|---|
| This machine | `berth attach acme`: the org's persistent tmux session, `main` |
| Any device (your tailnet) | `ssh -t -p <SSH_PORT> node@<address> tmux new -A -s main` |
| A browser, phone or tablet | `http://<address>:<TTYD_PORT>` (password: `berth password acme`) |
| claude.ai/code, the Claude app | the org's Remote Control environment: pick it and start a **New session** |
| VS Code, Cursor | Remote-SSH to `claude-acme` (the `~/.ssh/config` block is in `berth info acme`) |
| Scripts, cron | `berth run acme "your prompt"` (headless `claude -p`) |
| A quick question | `berth claude acme` (interactive `claude` in `/workspace`) |

All the terminal ways join **the same** tmux session. You can start on your laptop and pick it up on your phone.

## Next

- Open the firewall for what the org needs: `berth fw acme allow @python pypi.org` ([Firewall](guides/firewall.md)).
- Nightly encrypted backups: `berth keygen`, then `berth schedule` ([Backups](guides/backups.md)).
- Run orgs on another machine: `berth host add box1 ops@box1.example` ([Hosts](hosts.md)).
- A default org, so you can leave it out: `berth use acme` ([The command line](cli.md)).
