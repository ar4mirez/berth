# The command line

`berth --help` lists the everyday commands and the groups. Every command has usage and examples in its own `--help`,
and a page in the [command reference](reference/commands/berth.md).

## The layout

```
berth <group> <verb> [<org>] …
```

Every command lives in one group, and each group's `--help` lists its verbs. The verb comes first, then the org.

| Group | Its verbs |
|---|---|
| `berth org` | `ls` `create` `info` `up` `down` `restart` `destroy` `attach` `shell` `claude` `run` `exec` `logs` `connect` `rehydrate` `migrate` `use` `password show\|rotate` `takeover` `handback` |
| `berth repo` | `add` `new` `publish` `ls` `rm` `adopt` `sync` `audit` `policy` |
| `berth fw` | `show` `allow` `deny` `on` `off` `edit` `reload` `presets` `test` |
| `berth env` | `ls` `set` `unset` `accept` `migrate` |
| `berth pkg` | `ls` `add` `rm` `presets` `build` |
| `berth account` | `signin` `token` `login` `logout` `gh` `whoami` `remote status\|logs\|restart` |
| `berth backup` | `create` `restore` `keygen` `schedule on\|off\|status\|run` |
| `berth host` | `add` `ls` `rm` `guard` `rotate-access` |
| `berth system` | `install` `upgrade` `pull` `build` `completion` `parity-check` |
| `berth mcp` | (the MCP server: [guide](guides/mcp.md)) |

**Everyday shortcuts.** Nine `berth org` commands also work on their own, since you type them all day:
`berth ls`, `up`, `down`, `restart`, `shell`, `claude`, `attach`, `logs` and `info`. `berth up acme` is
`berth org up acme`.

**`--help` is always safe.** Every command and verb answers `--help` (and `-h`) with its usage and examples, and
runs nothing.

## Coming from an earlier berth, or from ccenv

Until 0.5.0 berth kept ccenv's flat commands (`berth init`, `berth token`, `berth gh-login`, …) beside the groups.
There is one spelling now. A removed one tells you its replacement and runs nothing:

```console
$ berth init acme
berth: 'berth init' is now 'berth org create' (berth org create --help)
```

| Before | Now |
|---|---|
| `berth init <org>` | `berth org create <org>` |
| `berth destroy`, `run`, `exec`, `connect`, `rehydrate`, `migrate`, `takeover`, `handback` | `berth org <the same verb>` |
| `berth use [<org>]` | `berth org use [<org>]` |
| `berth password <org> [show\|rotate]` | `berth org password show\|rotate <org>` |
| `berth auth <org>` | `berth account signin <org>` |
| `berth token`, `login`, `logout <org>` | `berth account token\|login\|logout <org>` |
| `berth gh-login <org>` | `berth account gh <org>` |
| `berth whoami [org…]`, `berth org whoami` | `berth account whoami [org…]` |
| `berth remote <org> [status\|logs\|restart]`, `berth org remote` | `berth account remote status\|logs\|restart <org>` |
| `berth clone <org> <repo>` | `berth repo add <org> <repo>` |
| `berth fw <org> allow …` | `berth fw allow <org> …` |
| `berth env <org> set KEY` | `berth env set <org> KEY` |
| `berth pkg <org> add …` | `berth pkg add <org> …` |
| `berth secrets migrate <org>` | `berth env migrate <org>` |
| `berth backup <org>…` | `berth backup create <org>…` |
| `berth restore <file>` | `berth backup restore <file>` |
| `berth keygen` | `berth backup keygen` |
| `berth schedule [--at …]`, `schedule status\|run\|off` | `berth backup schedule on [--at …]`, `backup schedule status\|run\|off` |
| `berth upgrade`, `pull`, `build`, `parity-check` | `berth system <the same verb>` |

**What still works as it was**, because something other than a person runs it:

- `berth fw|env|pkg <org> <verb> …`: the org before the verb. The instructions inside org images built before
  this change use it.
- `berth backup <org>…|--all …`: the nightly timer `backup schedule` installed runs it.
- `berth restore -` and `berth schedule …`: `org migrate` and `backup schedule on --host` run them on a host over
  ssh, where berth may be another version.
- `berth install [dir]`: the previous release's `upgrade` runs the new binary's `install`.
- `berth completion [shell]`: shell start-up files source it.

**The `ccenv` alias.** `berth system install --alias ccenv` links berth as `ccenv`. Run under that name (or with
`BERTH_SPELLINGS=ccenv`), berth has ccenv's whole flat command line, exactly as before: scripts written for ccenv
keep working, and the parity suite compares berth with ccenv that way.

## A default org

`berth org use acme` (or `berth org use acme@box1`, for an org on a registered host) saves a default org on this machine.
Org commands take it when you leave the org out:

```bash
berth org use acme@box1
berth up                        # = berth up acme@box1
berth fw show                   # = berth fw show acme@box1
berth fw allow pypi.org         # = berth fw allow acme@box1 pypi.org
berth repo add acme/widgets     # = berth repo add acme@box1 acme/widgets
berth org use                   # shows it
berth org use --clear           # removes it
```

**When berth uses it.** The org is "left out" when, where the command expects the org, there is:

- nothing, or a flag;
- something that can't be an org name: `owner/repo`, `pypi.org`, `@python`, `API_KEY`, a sentence for `run`;
- for a verb that takes a fixed number of arguments (`env set KEY`, `fw show`), exactly those and no more.

Without a default org, a command whose org is left out says so: `fw show: which org? (berth fw show <org>; …)`.

**When it doesn't.** A mistyped org (`berth up acmee`) is an error, and the default never replaces it. Commands that
name several orgs (`account whoami`, `backup create`) don't use it, and neither do `org create`, which names a new
org, and `org destroy`. A word that could be an org name is taken for one: `berth fw allow localhost` means the org
`localhost`, so name the org when an entry looks like one (`berth fw allow acme localhost`).

**How you can tell.** When the default is used, berth says so on stderr: `(acme@box1, the default org: berth org use)`.

It lives in `~/.config/berth/context`. Nothing reads it unless you set it, so scripts and the parity suite behave as
before.

## Running a command in an org

`berth claude acme` starts Claude Code in `/workspace`, and passes every argument to it, as ccenv did. To start it
somewhere else in the workspace, or with extra variables, put berth's options first and end them with `--`:

```bash
berth claude acme --cwd app/.worktrees/feat-12 --env OTEL_RESOURCE_ATTRIBUTES=card=12 -- --agent reviewer "/review 12"
```

`berth org exec` runs any command the same way, without a terminal, and ends with its exit code. Tools use it to run a step
inside the org (`crew start` runs its card preparation this way):

```bash
berth org exec acme -- git -C app status
berth org exec acme --cwd app --env CI=1 -- make test
```

- `--cwd DIR`: relative to `/workspace`, or absolute; it must be inside `/workspace`. The default is `/workspace`
- `--env K=V` (repeatable): set for the command after the org's own variables load, so it wins over them. The value is
  visible in the container's process list: don't pass secrets this way, use `berth env set acme KEY`
- Both run as `node`, need the org up, and reach nothing `berth shell` doesn't

## Removing an org (offboarding)

`berth org destroy acme` removes an org for good, for example at the end of an engagement:

- its container (`compose down --volumes`), which stops any session in it
- its directory: the workspace, Claude Code's config and history, the git and SSH keys, secrets, toolchains. Files the
  container made as root are removed through the engine
- its backups in the backups dir (`acme-<date>.tar.zst[.age|.gpg]`, and copies `backup restore --force` set aside), unless
  you pass `--keep-backups`. Backups written elsewhere with `backup create -o` aren't known to berth: delete those yourself
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
