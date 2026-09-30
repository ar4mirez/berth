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
| `berth org claude\|run\|exec <org> …` | `berth claude\|run\|exec <org> …` (`exec` is berth's own, [below](#running-a-command-in-an-org)) |
| `berth org whoami [org…]` | `berth whoami [org…]` |
| `berth org rehydrate <org>` | `berth rehydrate <org>` |
| `berth org migrate <org> <host>` | `berth migrate <org> <host>` |
| `berth org password <org> [show\|rotate]` | `berth password <org> [show\|rotate]` |
| `berth org remote <org> [status\|logs\|restart]` | `berth remote <org> [status\|logs\|restart]` |
| `berth org takeover\|handback <org>` | `berth takeover\|handback <org>` |
| `berth org destroy <org>` | `berth destroy <org>` (berth's own, [below](#removing-an-org-offboarding)) |
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

## Running a command in an org

`berth claude acme` starts Claude Code in `/workspace`, and passes every argument to it, as ccenv did. To start it
somewhere else in the workspace, or with extra variables, put berth's options first and end them with `--`:

```bash
berth claude acme --cwd app/.worktrees/feat-12 --env OTEL_RESOURCE_ATTRIBUTES=card=12 -- --agent reviewer "/review 12"
```

`berth exec` runs any command the same way, without a terminal, and ends with its exit code. Tools use it to run a step
inside the org (`crew start` runs its card preparation this way):

```bash
berth exec acme -- git -C app status
berth exec acme --cwd app --env CI=1 -- make test
```

- `--cwd DIR`: relative to `/workspace`, or absolute; it must be inside `/workspace`. The default is `/workspace`
- `--env K=V` (repeatable): set for the command after the org's own variables load, so it wins over them. The value is
  visible in the container's process list: don't pass secrets this way, use `berth env acme set`
- Both run as `node`, need the org up, and reach nothing `berth shell` doesn't

## Removing an org (offboarding)

`berth destroy acme` removes an org for good, for example at the end of an engagement:

- its container (`compose down --volumes`), which stops any session in it
- its directory: the workspace, Claude Code's config and history, the git and SSH keys, secrets, toolchains. Files the
  container made as root are removed through the engine
- its backups in the backups dir (`acme-<date>.tar.zst[.age|.gpg]`, and copies `restore --force` set aside), unless
  you pass `--keep-backups`. Backups written elsewhere with `backup -o` aren't known to berth: delete those yourself
- its lease, and the default org if it's `acme`

It lists all of that first, and removes nothing until you type the org's name. `--yes` skips the prompt; without a
terminal, `--yes` is required. It never takes the default org: name the org. It refuses under `--read-only` and for
ccenv's orgs.

berth records the org as destroyed, with the time, in `<state>/berth/destroyed`. `berth --output json ls` lists it
under `destroyed` until an org with that name exists again, so other tools can tell an offboarded org from a missing
one.

## Output

`--output json` returns structured data from the commands that have it (docs/json.md). Global flags go before the
command: `berth --read-only --output json ls`.
