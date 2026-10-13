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
  `admin` adds destroying an org, and what takes a secret (a variable's value, a Claude token, API tokens). The
  server decides, not
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
| Info | the connection sheet: state, the claude.ai/code link, address, SSH, the browser terminal, the git key | open the link or the terminal (its user is shown, and its password when you ask), copy the SSH command or the key, sign the org in to Claude or GitHub, restart Remote Control, destroy the org |
| Firewall | the allowlist, and whether the firewall is live | turn it on or off, reload it, allow entries, deny one, test what the org can reach |
| Repos | registered repos, their branch and state, and what isn't registered | add, remove, sync |
| Env | the names of the org's variables, never their values | set one (its value is typed unseen, and never shown again), remove one |
| Packages | the system packages the org's image adds, and that image | add, remove (there after the org's next restart) |
| Backups | this org's backups on this machine | back up now |
| Logs | the container's log | follow it |

The top of an org's page has what fits its state: **Start** for one that is down, **Restart** and **Stop** for
one that is up. A page for an org that is down says so, and leaves out what needs it running (signing it in,
Remote Control's restart) until it is started.

Creating an org is under the list of orgs. It writes the org's files and keys and doesn't start it: its page
opens next, to start it from. Everything refreshes by itself every few seconds while the page is in front.

## What it asks before acting

As in the terminal dashboard, each action is an operation from berth's catalog, and the catalog decides:

- **An operation that stops the work running in an org** (restart, stop) says what it does and what stops, and
  runs only after **the org's name is typed**. That is the `confirm` the API wants.
- **Starting an org that is down** stops nothing, so it asks without the name typed, and the page sends the
  name for it. The first start gets the image, which can take a few minutes: its progress shows as it goes.
- **Destroying an org** asks the same way, and needs a caller that may do everything: `berth ui`, or a token with
  the `admin` scope. Nobody else is shown the button.
- **One that removes something** (denying a firewall entry, removing a repo or a package), and turning the firewall
  off, ask first.
- **With `--read-only`**, or a token that may only read, no control that changes anything is shown.

While an operation runs the page shows its steps and output as they happen, and its error and hint if it fails:
the same words as the command line. Every change asked for is in the [audit log](../api.md#the-rules), as for
any caller of the API.

## Every operation, by name

**All**, at the top, lists everything the API does: each operation by the command it is, what it needs
(`read`, `write`, `restart` or `admin`), and whether this caller may run it. Opening one gives a form with its
arguments and what each is for, and shows what it answered. The list is made from the API's own description of
itself, so an operation is there the day it has an endpoint, before it has a screen of its own.

Today that is where these are: the repo policy and audit, adopting and creating repos, removing a variable, the
backup schedule, Remote Control's log, signing an org out, building an org's image, pulling and building berth's,
upgrading berth, hosts (adding, removing, guarding, rotating berth's key), rehydrating an org, publishing a local
repo, moving secrets into files, asking Claude something in an org, restoring a backup, and moving an org to
another host.

An operation that restarts or removes an org asks for its name there too.

## The browser terminal's password

An org's Info tab links to its browser terminal, which asks for a user and a password. The user is shown beside
the link. **Show the password** asks berth for the password and shows it, with a button to copy it:

- only for a caller that may do everything (`berth ui`, or a token with the `admin` scope), and not on a
  read-only server;
- only when you ask: it isn't fetched with the page;
- for a minute, or until you press **Hide** or the page moves on;
- with a line in berth's audit log each time, saying it was shown and for which org.

## Signing an org in

**Sign in to Claude** and **Sign in to GitHub**, on an org's Info tab, run `berth account signin` and
`berth account gh` for it, on a terminal of berth's own where the org is. The page shows what the command
prints, as text, offers the link it shows to open, and has a place to type the code you get there. They need a
caller that may do everything: `berth ui`, or a token with the `admin` scope.

- Open the link in a browser signed in to **that org's** account: a private window keeps yours out of it.
- The page reads what a terminal would draw; it isn't one. If a step shows a choice, the ↑, ↓, Enter and Esc
  buttons send those keys.
- A sign-in can show the org's new token as it is made, as the command line does.
- **Stop**, or closing the page, ends the sign-in. It is offered once the org is running: one that is down is
  started first. A sign-in that ends badly says so, with what the command printed.

## What stays on the command line

- **A session in an org** (`attach`, `shell`, `claude`): the org has its own ways in, and its Info tab links to
  them: the browser terminal, SSH, and claude.ai/code.
- **An org's browser-terminal password, for a caller without the `admin` scope**: `berth org password show`
  prints it, on the machine the org is on.
- **Hosts at a cloud provider** (`host create`, `destroy`, `reconcile`): they need the provider's key, which
  stays out of a server.
- berth's own setup: `install`, the service, shell completion.
