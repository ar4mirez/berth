# The dashboard

`berth tui` is berth in one screen: every org and host with its state, and a page per org. It does what the commands
do, through the same operations, so nothing behaves differently from the command line.

```bash
berth tui                # the dashboard
berth --read-only tui    # the same, and it can only read
```

```text
berth

  ORG      HOST   STATE  SSH   TTYD  TOKEN  REMOTE
▸ acme     local  up     2201  7701  set    on
  globex   local  down   2202  7702  -      -
  initech  box1   up     2201  7701  set    login-needed
  box2 is unreachable: ssh: connection refused

Hosts
  NAME   KIND   REACHABLE  ENGINE         ORGS  ADDRESS
  local  local  yes        docker 29.0.0  2
  box1   ssh    yes        docker 28.1.0  1     ops@box1.example:22
  box2   ssh    no                        -     ops@box2.example:22

↑↓ select · enter open · u/d/r up/down/restart · A attach · S shell · o operations · ? help · q quit
```

It needs a terminal, works over SSH, and redraws to the size it is given (40×10 at least). Colours follow the
terminal's light or dark background. Every page refreshes by itself every few seconds.

## An org's page

`enter` opens the selected org. Its tabs:

| Tab | What it shows | What you can do there |
|---|---|---|
| 1 Info | the connection sheet: state, Remote Control, address, SSH, browser terminal, git key | |
| 2 Firewall | the allowlist, and whether the firewall is live | `a` allow entries, `x` deny the selected one |
| 3 Repos | registered repos, their branch and state, and what isn't registered | `a` add, `x` remove, `y` sync |
| 4 Env | the names of the org's variables, never their values | |
| 5 Backups | this org's backups on this machine | `b` back up now |
| 6 Logs | the container's log, followed | |

```text
berth  acme

1 Info   2 Firewall   3 Repos   4 Env   5 Backups   6 Logs

  live: on 159

  ENTRY
  mode on
▸ @python
  pypi.org
  10.0.0.0/8

↑↓ select · a allow · x deny · tab switch · esc back · ? help
```

## Keys

| Key | |
|---|---|
| `↑` `↓` (or `k` `j`) | select |
| `enter` | open the selected org |
| `tab`, `1`–`6` | switch tab |
| `esc` | back to the list |
| `u` `d` `r` | start, stop, restart the org |
| `A` `S` | attach to its tmux session, or open a shell |
| `o` | every operation, by name |
| `R` | read again now |
| `?` | the keys |
| `q`, `ctrl+c` | quit |

`A` and `S` give the terminal to `berth attach` and `berth shell`: the dashboard comes back when you leave them.

## Every operation, by name

`o` lists everything berth's API does: the same list the [web dashboard](web.md) has under **All**, so the two
dashboards do the same things. Each line is an operation by the command it is, what it needs (`read`, `write`,
`restart` or `admin`), and what it does.

```text
berth

Every operation (56)

  OPERATION               NEEDS    WHAT IT DOES
▸ account logout          restart  Sign an org out of Remote Control
  account whoami          read     Which Claude account and GitHub account each org is signed in to
  backup                  write    Take an encrypted backup of orgs on this machine, into the backup
  backup schedule off     write    Stop the nightly backups on this machine
  …

↑↓ select · enter open · esc back · ? help · q quit
```

`enter` opens one as a form. Its org starts as the one the dashboard was on:

```text
berth repo add  needs: write
Register a repository for an org and clone it into /workspace. Only registered repos can be used
in an org.

  ARGUMENT  VALUE
  org       acme          required
  repo      acme/widgets  required
  dir       -
  branch    -
▸ no_clone  [x]

register only; it is cloned by the next repo sync

↑↓ select · enter set · r run · esc back · ? help
```

`enter` sets the selected argument (or turns a switch on and off), and `r` runs the operation and shows what it
answered. Several values go in one argument separated by spaces.

This is where the operations with no key of their own are: creating and destroying an org, the firewall on, off
and reloaded, packages, the repo policy, the backup schedule, Remote Control's restart and log, and the rest.

## What it asks before acting

Each action is an operation from berth's catalog, the one `--read-only` and the MCP server use. The catalog decides
what the dashboard asks, for a key and for an operation run by name alike:

- **An operation that restarts a container** (`u`, `d`, `r`) shows what it does and what stops, and runs only after
  a `y`. `enter`, and any other key, cancel.
- **One that can't be undone** (destroying an org) runs only after the org's name is typed, as `berth org destroy`
  asks on the command line.
- **One that removes something** (denying a firewall entry, removing a repo) asks the same way.
- **Every change asked for is in the audit log** (`audit.log` in the state root), as from `tui`.
- **With `--read-only`** no write runs, and `attach` and `shell` are refused too, as on the command line.

```text
berth

╭─────────────────────────────────────╮
│ berth restart acme                  │
│                                     │
│ This recreates the container.       │
│ It stops the work running in acme.  │
│                                     │
│ Run it? y = yes, anything else = no │
╰─────────────────────────────────────╯
```

A removed repo's folder goes to quarantine, as with `berth repo rm`. What an action printed, or why it failed, is
shown when it ends, with the same message and hint as the command.

## What isn't in it

Signing an org in from the dashboard, and creating
a host at a cloud provider: use the commands. Sign-in is the last part of
([#167](https://github.com/ar4mirez/berth/issues/167)). The org list doesn't show each org's image.
