# Getting started

## What you need

- **An OS:** Linux (amd64 or arm64), or macOS (Apple silicon or Intel).
- **Docker** with the compose plugin (`docker compose version`); on macOS, Docker Desktop or any engine that provides
  the Docker API and `docker compose`. **Or Podman**, rootful or rootless, **or Apple's `container`** on Apple
  silicon ([Container engines](engines.md)).
- **Optional: [Tailscale](https://tailscale.com).** With it, each org's SSH and browser terminal are bound to this
  machine's Tailscale address, so you can reach them from your other devices and nobody else can. Without it, they're
  bound to `127.0.0.1`. Another VPN, or only SSH, works too ([Networking](networking.md)).
- **Optional: `gh`,** signed in on this machine. `berth repo new` uses it to create repos on GitHub.

## Install berth

Every channel installs a signed release. Pick one:

=== "Install script"

    Download the script, read it, then run it. Don't pipe it into a shell.

    ```bash
    curl -fsSLO https://github.com/ar4mirez/berth/releases/latest/download/install.sh
    less install.sh
    sh install.sh                     # or: sh install.sh --version v0.9.0 --bin-dir ~/bin
    ```

    It prints each step as it goes:

    - **Signature:** it checks the signature with `cosign`, or with `gh` (the build's provenance). With neither, it
      stops, unless you set `BERTH_INSTALL_CHECKSUM_ONLY=1` to accept the checksum alone.
    - **Checksum:** it then checks the archive's checksum.
    - **Install:** it installs into `~/.local/opt/berth/<version>/`, links `~/.local/bin/berth`, and adds shell
      completion. It needs no root.

    **Upgrade:** `berth system upgrade` (same checks; `berth system upgrade --rollback` goes back).

=== "Homebrew (macOS)"

    ```bash
    brew install --cask ar4mirez/tap/berth
    ```

    **Upgrade:** `brew upgrade berth`. Includes the man pages and completion.

=== "Debian, Ubuntu"

    berth's apt repository, signed (amd64 and arm64):

    ```bash
    sudo install -d -m 755 /etc/apt/keyrings
    sudo curl -fsSL -o /etc/apt/keyrings/berth.gpg https://ar4mirez.github.io/berth/apt/berth.gpg
    echo "deb [signed-by=/etc/apt/keyrings/berth.gpg] https://ar4mirez.github.io/berth/apt stable main" \
      | sudo tee /etc/apt/sources.list.d/berth.list
    sudo apt update && sudo apt install berth
    ```

    **Upgrade:** `sudo apt update && sudo apt install berth`, or with the rest of the system.

    Without the repository, install a release's package as a file:
    `sudo apt install ./berth_<version>_<amd64|arm64>.deb`, from the
    [release page](https://github.com/ar4mirez/berth/releases/latest).

=== "Fedora, RHEL"

    berth's yum repository, signed (x86_64 and aarch64):

    ```bash
    sudo curl -fsSL -o /etc/yum.repos.d/berth.repo https://ar4mirez.github.io/berth/rpm/berth.repo
    sudo dnf install berth
    ```

    dnf asks once to accept the repository's key (fingerprint below). **Upgrade:** `sudo dnf upgrade berth`.

    Without the repository, install a release's package by URL:
    `sudo dnf install https://github.com/ar4mirez/berth/releases/download/v<version>/berth-<version>-1.<x86_64|aarch64>.rpm`.

=== "Arch Linux"

    From the AUR: `berth-bin` (the release binary) or `berth` (built from source), for example
    `yay -S berth-bin`. Or the release's package:

    ```bash
    v=0.4.0; curl -fsSLO https://github.com/ar4mirez/berth/releases/download/v$v/berth-$v-1-x86_64.pkg.tar.zst
    sudo pacman -U berth-$v-1-x86_64.pkg.tar.zst
    ```

    **Upgrade:** with your AUR helper, or `pacman -U` with the new package.

=== "mise"

    ```bash
    mise use -g github:ar4mirez/berth
    ```

    This is mise's GitHub backend: it takes the release's archive for your platform, and checks its checksum
    and its build provenance (the GitHub artifact attestation each berth release is published with) before
    installing. It needs no plugin and no registry entry.

    **Upgrade:** `mise upgrade github:ar4mirez/berth`.

    **Coming from `ubi:ar4mirez/berth`?** mise has deprecated its `ubi` backend. Switch once:
    `mise unuse -g ubi:ar4mirez/berth && mise use -g github:ar4mirez/berth`.

    **Right after a release,** mise installs the one before. It holds a new release back for a while by
    default (its `minimum_release_age` setting), and it caches each tool's list of versions for up to a day.
    To get a release now, name it, or turn both off for that command:

    ```bash
    mise use -g github:ar4mirez/berth@<version>           # as in: berth@1.2.3
    MISE_MINIMUM_RELEASE_AGE=0 MISE_USE_VERSIONS_HOST=0 mise use -g github:ar4mirez/berth
    ```

**The apt and yum repositories** hold the latest five releases and are signed with berth's packages key:

```
B377 15D1 5535 BEC5 35AC  1C8F 2DD9 5203 64B7 0A65
```

Each package in them comes from a release whose own signature was verified first ([Releases](releases.md)). The
repository's signed index lists every package's checksum, which is what apt and dnf check; the packages themselves
carry no signature of their own.

**Which upgrade path applies:** with a package manager, `berth system upgrade` says which one installed berth and
leaves the upgrade to it.

**What's in every archive and package:** the man pages (`man berth`, `man berth-org-up`, …) and bash, zsh and fish
completion.

**Checking a download by hand:** the release's `checksums.txt` is signed and covers every archive and package
([Releases](releases.md)).

**Windows:** use WSL2 and the Linux build. berth drives Linux containers with bind mounts and SSH, so there's no
native Windows build. To use orgs on another machine from Windows, the orgs run on a registered Linux host and
you reach them over SSH or the browser terminal.

## Your first org

```bash
berth org create acme --name "Ada Lovelace" --email ada@example.com
berth up acme                   # pulls berth's image (or builds it), then starts the container
berth account signin acme                 # sign in: the Claude token, then the Remote Control login
```

`berth account signin` runs inside the container and prints a sign-in link. **Open it in a private browser window**, or a
browser profile for that org, and sign in with that org's Claude account. The rest of your browser, your own `claude`
and your other orgs are never touched. `berth account whoami` shows which account each org uses.

Then give the org access to GitHub:

1. **`berth account gh acme`.** It signs in the `gh` CLI inside the container, for PRs, issues and the API. git then
   reaches the org's registered GitHub repos with that login's token, so they are writable with nothing more to set
   up. It also puts the org's git key on that GitHub account, with this machine's `gh`, if that is signed in to the
   same account.
2. **Without a gh login, add the org's key yourself,** to the GitHub **account**. `berth info acme` prints it
   (`Git public key: ssh-ed25519 …`). With `gh`:
   `gh ssh-key add ~/.local/share/berth/orgs/acme/ssh/id_ed25519.pub --title claude-acme`.
   A deploy key works for one repository only: the org could push to that repo and only read the others
   ([Repos](guides/repos.md#how-git-reaches-github)).
3. **Register the repos the org may use:** `berth repo add acme acme/widgets` registers the repo and clones it into
   `/workspace/widgets`. Nothing else can be cloned or kept there (see [Repos](guides/repos.md)).

## Connecting

`berth info acme` prints every way in, with this org's ports and addresses:

| From | How |
|---|---|
| This machine | `berth attach acme`: the org's persistent tmux session, `main` |
| Any device (your tailnet) | `ssh -t -p <SSH_PORT> node@<address> tmux new -A -s main` |
| A browser, phone or tablet | `http://<address>:<TTYD_PORT>` (password: `berth org password show acme`) |
| claude.ai/code, the Claude app | the org's Remote Control environment: pick it and start a **New session** |
| VS Code, Cursor | Remote-SSH to `claude-acme` (the `~/.ssh/config` block is in `berth info acme`) |
| Scripts, cron | `berth org run acme "your prompt"` (headless `claude -p`) |
| A quick question | `berth claude acme` (interactive `claude` in `/workspace`) |

All the terminal ways join **the same** tmux session. You can start on your laptop and pick it up on your phone.

## Next

- Open the firewall for what the org needs: `berth fw allow acme @python pypi.org` ([Firewall](guides/firewall.md)).
- Nightly encrypted backups: `berth backup keygen`, then `berth backup schedule on` ([Backups](guides/backups.md)).
- Run orgs on another machine: `berth host add box1 ops@box1.example` ([Hosts](hosts.md)).
- A default org, so you can leave it out: `berth org use acme` ([The command line](cli.md)).
- See everything at once: `berth tui` ([The dashboard](guides/tui.md)).
- Let an agent or a script manage orgs: `berth mcp`, `berth serve` ([MCP](guides/mcp.md), [The API](api.md)).
- The rest, on one page: [What berth does](features.md).
