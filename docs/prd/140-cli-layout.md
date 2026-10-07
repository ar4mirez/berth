# PRD: an organized command line (#140)

Status: **done** · Issue: [#140](https://github.com/ar4mirez/berth/issues/140) · Restarts orgs: no

## Problem

A review of every command against its help (2026-10-07) found the command line hard to learn:

1. 45 of 87 command spellings ignored `--help`; with a default org, `--help` ran the command.
2. Verbs were invisible: `repo`, `fw`, `env`, `pkg`, `schedule`, `password`, `remote` and `backup` took them as
   plain arguments, so help couldn't list them.
3. Two argument orders: `repo add <org>` and `fw <org> allow`.
4. Duplicates in help: fourteen commands at the top level and again under `berth org`; `whoami` twice; `clone` and
   `repo add`.
5. 19 commands worked but were hidden at the top level, some under another name in their group.
6. Related commands were scattered: four top-level commands for backups.

## Decisions (2026-10-07, the maintainer's)

- **Top level: groups plus shortcuts.** Help lists the noun groups, each owning its commands once, and one section
  of everyday shortcuts: `ls`, `up`, `down`, `restart`, `shell`, `claude`, `attach`, `logs`, `info`.
- **Verb first everywhere**: `berth fw allow acme pypi.org`. The org-first order keeps working.
- **The old spellings are removed** from `berth`: one spelling per command.

## Design

**The tree.** `berth <group> <verb> [<org>] …`:

| Group | Verbs |
|---|---|
| `org` | `ls` `create` `info` `up` `down` `restart` `destroy` `attach` `shell` `claude` `run` `exec` `logs` `connect` `rehydrate` `migrate` `use` `password show\|rotate` `takeover` `handback` |
| `repo` | `add` `new` `publish` `ls` `rm` `adopt` `sync` `audit` `policy` |
| `fw` | `show` `allow` `deny` `on` `off` `edit` `reload` `presets` `test` |
| `env` | `ls` `set` `unset` `accept` `migrate` |
| `pkg` | `ls` `add` `rm` `presets` `build` |
| `account` | `signin` `token` `login` `logout` `gh` `whoami` `remote status\|logs\|restart` |
| `backup` | `create` `restore` `keygen` `schedule on\|off\|status\|run` |
| `host` | `add` `ls` `rm` `guard` `rotate-access` |
| `system` | `install` `upgrade` `pull` `build` `completion` `parity-check` |
| `mcp` | |

Every verb is a real subcommand with its own help, usage and examples.

**How it is built.** ccenv's flat commands stay in the code as the operations they are: each native command is one
of them under its new name, or calls one with its arguments put in ccenv's order. `internal/ops`'s catalog, the
`--read-only` guard and the MCP tools are keyed by those operations and don't change.

**A removed spelling answers with the new one**: `berth init` → `'init' is now 'berth org create'`, exit 1. It
runs nothing. The instructions inside images built before this change name some of them.

**What keeps working, and why** (hidden from help):

- `berth fw|env|pkg <org> <verb> …`: the org-first order, by decision.
- `berth backup <org>…|--all …`: installed timers run it every night.
- `berth restore -`: `migrate` runs it over ssh on a host whose berth may be another version.
- `berth schedule …`: `backup schedule --host` runs it on a host.
- `berth install [dir]`: `upgrade` in the previous release runs the new binary's `install`.
- `berth completion`: shell start-up files source it.
- `berth image-tag`.

**Messages.** The code's messages are written with ccenv's spellings, which the parity suite compares with ccenv's
own. `ops.Respell` turns the commands a message names into berth's (`berth init acme` → `berth org create acme`,
`berth env acme set KEY` → `berth env set acme KEY`) where berth prints: errors and their JSON form, hints, MCP tool
descriptions, and the files a new org starts with. A generated per-org Dockerfile keeps its header, so no org's
image is rebuilt because of a spelling.

**ccenv's spellings, for the `ccenv` alias.** Run as `ccenv` (`system install --alias ccenv`), or with
`BERTH_SPELLINGS=ccenv`, berth has ccenv's flat command line, exactly as before. The parity suite runs berth that
way, so the rule "behaviour matches ccenv" keeps its meaning and its tests.

## Tasks

- [x] T1. `--help` never runs the command (#139).
- [x] T2. The new tree; removed spellings; ccenv's spellings for the alias; tests; `docs/cli.md`; the reference.
- [x] T3. berth's own messages and hints use the new spellings (`ops.Respell`: errors, their JSON form, what
      berth prints, MCP tool descriptions, and the files a new org starts with).
- [x] T4. Guides, README, `install.sh` and the image's instructions use the new spellings. The image's text
      reaches an org at its next restart; until then its old instructions still work or name their replacement.

## Acceptance criteria

| Criterion | How it is checked |
|---|---|
| Every command answers `--help` and never runs for it | `TestHelpNeverRunsTheCommand` |
| Each command is listed once in help; no hidden command except the ones listed above | `TestHelpLayout`, `TestOneSpellingPerCommand` |
| Every verb has its own help with usage and an example | `TestEveryCommandHasAnExample`, the generated reference |
| Verb first works with and without a default org; org first still works for `fw`, `env`, `pkg` | `TestVerbFirst`, `TestDefaultOrg` |
| A removed spelling names the new one and runs nothing | `TestOneSpellingPerCommand` |
| Messages name commands as this command line spells them | `TestRespell`, `TestMessagesNameBerthsCommands` |
| Behaviour is unchanged: each native command runs the same operation with the same arguments | `TestVerbFirst` (the operation and arguments each spelling resolves to), `TestCatalogCoversEveryCommand`; the parity suite, on ccenv's spellings |
