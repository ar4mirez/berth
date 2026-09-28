# The command line

`berth --help` lists the commands in sections. Every command has examples in its own `--help`.

## Two spellings, the same commands

berth started as a rewrite of ccenv, whose commands were one flat list: `berth init`, `berth token`,
`berth gh-login`, and so on. They all still work, unchanged. The parity suite runs them against ccenv, and the
instructions inside the container image (`berth fw <org> allow`, `berth repo add`) use them.

Related commands are also gathered under a noun (#55): `berth org …`, `berth account …` and `berth system …`. Each verb
there *is* the flat command under another name: the same arguments, output, access (`--read-only`) and restart
behaviour. Help shows the grouped form and hides the flat commands the groups cover. Use whichever you like.

| Grouped | Flat (ccenv's) |
|---|---|
| `berth org ls` | `berth ls` |
| `berth org create <org>` | `berth init <org>` |
| `berth org info <org>` | `berth info <org>` |
| `berth org up\|down\|restart <org>` | `berth up\|down\|restart <org>` |
| `berth org attach\|shell\|logs <org>` | `berth attach\|shell\|logs <org>` |
| `berth org claude\|run <org> …` | `berth claude\|run <org> …` |
| `berth org whoami [org…]` | `berth whoami [org…]` |
| `berth org rehydrate <org>` | `berth rehydrate <org>` |
| `berth org migrate <org> <host>` | `berth migrate <org> <host>` |
| `berth org password <org> [show\|rotate]` | `berth password <org> [show\|rotate]` |
| `berth org remote <org> [status\|logs\|restart]` | `berth remote <org> [status\|logs\|restart]` |
| `berth org takeover\|handback <org>` | `berth takeover\|handback <org>` |
| `berth account signin <org>` | `berth auth <org>` |
| `berth account token <org>` | `berth token <org>` |
| `berth account login\|logout <org>` | `berth login\|logout <org>` |
| `berth account gh <org>` | `berth gh-login <org>` |
| `berth account whoami [org…]` | `berth whoami [org…]` |
| `berth system install\|upgrade\|pull\|build` | `berth install\|upgrade\|pull\|build` |
| `berth system completion [shell]` | `berth completion [shell]` |
| `berth system parity-check` | `berth parity-check` |

These commands were already a noun followed by an org or a verb, so they keep their shape: `repo`, `clone`, `fw`,
`env`, `secrets`, `backup`, `restore`, `schedule`, `keygen` and `host`. The names `auth` and `backup` stay the flat
commands they were (`berth auth acme`, `berth backup acme`). That's why the groups are called `account` and not `auth`.

## A default org

`berth use acme` (or `berth use acme@box1`, for an org on a registered host) saves a default org on this machine.
Org commands take it when you leave the org out:

```bash
berth use acme@box1
berth up                        # = berth up acme@box1
berth fw show                   # = berth fw acme@box1 show
berth repo add acme/widgets     # = berth repo add acme@box1 acme/widgets
berth use                       # shows it
berth use --clear               # removes it
```

**When berth uses it.** The org is "left out" when, where the command expects the org, there is:

- nothing;
- a flag;
- one of that command's own verbs (`show`, `allow`, …);
- something that can't be an org name (`owner/repo`, or a sentence for `run`).

**When it doesn't.** A mistyped org (`berth up acmee`) is an error, and the default never replaces it. Commands that
name several orgs (`whoami`, `backup`) don't use it, and neither does `init`, which names a new org.

**How you can tell.** When the default is used, berth says so on stderr: `(acme@box1, the default org: berth use)`.

It lives in `~/.config/berth/context`. Nothing reads it unless you set it, so scripts and the parity suite behave as
before.

## Output

`--output json` returns structured data from the commands that have it (docs/json.md). Global flags go before the
command: `berth --read-only --output json ls`.
