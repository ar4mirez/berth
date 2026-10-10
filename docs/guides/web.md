# The web dashboard

berth has a dashboard for the browser, in the binary: every org and host with its state, and a page per org. It is
made for a phone first, and does what [`berth tui`](tui.md) does, through the same operations.

```bash
berth ui                               # on this machine: serves it, and opens your browser
berth serve --listen 100.64.0.7:8443   # from another device: the API and the dashboard, over TLS
```

It loads nothing from anywhere else: the page, its script and its styles are files inside berth.

## On this machine: `berth ui`

```bash
berth ui                  # prints a link, and opens it
berth --read-only ui      # the same, and it can only read
berth ui --port 7780 --no-open
```

- **It listens on `127.0.0.1` only**, on a free port (or `--port`), over plain HTTP: the traffic never leaves the
  machine.
- **Every request needs a token made for this run.** It is in the link berth prints, after the `#`, which a
  browser doesn't send to any server. berth keeps it nowhere, and it stops working when `berth ui` ends.
- **The browser is opened through a file only you can read**, which sends it on to the link: a command's
  arguments are visible to every user of a machine, and the token would be among them.
- Without a desktop (over SSH, say) nothing is opened: berth prints the link. To use it from your own machine,
  forward the port (`ssh -L 7780:127.0.0.1:7780 box1`, with `berth ui --port 7780` there) and open the link.
- It runs until interrupted.

## From another device: `berth serve`

[`berth serve`](../api.md) serves the dashboard beside the API, at `/`. Over TCP it is TLS only, and the dashboard
signs in with an [API token](../api.md#over-the-network-tls-and-tokens):

```bash
berth serve token add phone --scope restart      # prints the token once (admin: also destroy an org)
berth serve --listen 100.64.0.7:8443             # or: berth system service install --listen …
```

Open `https://100.64.0.7:8443/` and paste the token.

- **The token's scope is what the dashboard can do.** With `read` it shows everything and offers no action;
  `write` adds the firewall, repos, packages, backups and creating an org; `restart` adds start, restart and stop;
  `admin` adds destroying an org. The server decides, not
  the page: what the page hides, the API would refuse.
- **The token is kept in that browser tab only** (its session storage), and is forgotten when the tab closes or
  you sign out. There is no cookie, so no other site can act with it.
- **The page's own files are served without a token.** They hold no data: an org's name appears only in what the
  API answers, and the API answers nobody without a token.
- **The certificate.** berth's own is self-signed, so a browser warns the first time; compare what it shows with
  the fingerprint `berth serve` prints. For a certificate browsers trust, give berth one
  (`--tls-cert`, `--tls-key`): on a tailnet, `tailscale cert <this machine's name>` makes a pair.
- `berth serve --no-ui` serves the API alone.

Bind `--listen` to the address you mean, as for the API: a tailnet address for your devices, never `0.0.0.0`
unless every network the machine is on should be offered the sign-in page.

## What is in it

**Orgs**: each with its state, Remote Control and whether it has a Claude token; an org on a registered host says
which. **Hosts**: each with whether it is reachable, its engine and how many orgs it has.

An org's page:

| Tab | What it shows | What you can do there |
|---|---|---|
| Info | the connection sheet: state, the claude.ai/code link, address, SSH, the browser terminal, the git key | open the link or the terminal, copy the SSH command or the key, restart Remote Control, destroy the org |
| Firewall | the allowlist, and whether the firewall is live | turn it on or off, reload it, allow entries, deny one, test what the org can reach |
| Repos | registered repos, their branch and state, and what isn't registered | add, remove, sync |
| Env | the names of the org's variables, never their values | |
| Packages | the system packages the org's image adds, and that image | add, remove (there after the org's next restart) |
| Backups | this org's backups on this machine | back up now |
| Logs | the container's log | follow it |

Start, Restart and Stop are at the top of the page, and creating an org is under the list of orgs. Everything
refreshes by itself every few seconds while the
page is in front.

## What it asks before acting

As in the terminal dashboard, each action is an operation from berth's catalog, and the catalog decides:

- **An operation that restarts a container** (start, restart, stop) says what it does and what stops, and runs
  only after **the org's name is typed**. That is the `confirm` the API wants: the page can't send it for you.
- **Destroying an org** asks the same way, and needs a caller that may do everything: `berth ui`, or a token with
  the `admin` scope. Nobody else is shown the button.
- **One that removes something** (denying a firewall entry, removing a repo or a package), and turning the firewall
  off, ask first.
- **With `--read-only`**, or a token that may only read, no control that changes anything is shown.

While an operation runs the page shows its steps and output as they happen, and its error and hint if it fails:
the same words as the command line. Every change asked for is in the [audit log](../api.md#the-rules), as for
any caller of the API.

## What isn't in it yet

Sign-ins, setting a variable, the repo policy, the backup schedule, restores, moving an org, and
managing hosts: use the commands. Both dashboards are getting them
([#167](https://github.com/ar4mirez/berth/issues/167)).
