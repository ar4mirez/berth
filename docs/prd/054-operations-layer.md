# PRD: the structured operations layer (#54)

Status: **in progress** · Issue: [#54](https://github.com/ar4mirez/berth/issues/54) · Restarts orgs: no

## Problem

berth's commands print text as they go, because phases 0 to 5 mirrored ccenv byte for byte. The TUI (#59), the MCP
server (#61), the API (#62) and scripts all need the same operations to **return data and typed errors** instead.

The first slice (#68) added the operations catalog and `--output json` for `ls`, `env ls`, `fw show` and `host ls`.
This PRD covers the rest of the issue.

## Decisions (2026-10-06)

- **All five open items are in scope**, delivered as a series of PRs, each merged when its CI is green.
- **Read operations move into `internal/ops`.** Each returns typed data and never prints; `internal/app` only renders
  it as ccenv's text. Write and lifecycle operations keep their ccenv-mirroring bodies in `internal/app`, so the
  byte-for-byte text isn't put at risk, and get typed errors and progress events through `internal/ops`.

## Objectives

1. Every read command returns data: `--output json`, with a documented, versioned schema and a golden test.
2. Errors are typed: an exit code, a message and a hint, in text and in JSON.
3. Long operations report progress as events.
4. Every operation declares whether it writes and whether it restarts a container, and a test fails for one that
   doesn't.
5. The parity suite is unchanged: the text output stays byte for byte ccenv's.

## Design

### `ops.System`

A read operation in `internal/ops` runs against a `System`: the commands and files of the host the org is on, and
the state root's orgs. `*app.App` implements it with the helpers its commands already use, so an operation makes the
same calls in the same order as before, and the parity suite's call comparison still holds.

```go
info, err := ops.GetInfo(ctx, sys, "acme")   // data, or a typed error; nothing printed
```

`internal/app`'s command calls the operation and renders the result: as ccenv's text, or as the JSON document.
A field tagged `json:"-"` carries what only the text needs (a port exactly as `org.env` has it, for example).

An operation that fails part-way returns what it gathered together with the error, because ccenv prints what it
has before it stops, and the text must match.

### Commands that return no JSON

A read command returns data, reports events (`logs`, `remote logs`: PR 4), or says why not in the catalog
(`NoJSON`). The reasons:

| Command | Why |
|---|---|
| `completion` | a shell script |
| `connect` | holds an SSH tunnel open until interrupted |
| `password show` | a secret, and secrets never appear in JSON (`docs/json.md`) |
| `parity-check` | a line diff against ccenv for people, retired with ccenv |

## Tasks

### PR 1: read operations, part 1
- [x] `ops.System`, and `*app.App` as one
- [x] `ls`, `env ls`, `fw show`, `host ls` move into `internal/ops` (their JSON is unchanged)
- [x] New JSON: `info`, `whoami`, `remote status`, `use`, `image-tag`
- [x] Golden files (`test/parity/testdata/json/`) and `docs/json.md` for each

### PR 2: read operations, part 2
- [x] New JSON: `repo ls`, `repo audit`, `repo policy`, `fw presets`, `fw test`, `schedule status`,
      `host guard status`
- [x] The catalog test: a read operation returns JSON or states why not (`TestReadsReturnDataOrSayWhyNot`)
- [x] Golden files for every document, the four from #68 included

### PR 3: rich errors
- [x] `ops.Error`: kind, exit code, message, hint; returned by every operation in `internal/ops`, and by the
      shared guards in `internal/app`
- [x] `ops.AsError` types any other failure (a silent exit, a command's own exit code, `--read-only`, usage)
- [x] With `--output json`, a failure is a `berth.error/v1` document on stderr (`TestBerthJSONErrors`)
- [x] Text output unchanged, silent exits included

### PR 4: progress events
- [x] `ops.Event`, `ops.Progress` and a sink; the catalog marks the operations that report (`Events`)
- [x] `up`, `restart`, `build`, `pull`, `backup`, `restore`, `logs` and `remote logs` emit: their steps, and every
      line they or their commands print
- [x] `--output json` on those commands prints one `berth.event/v1` per line, ending with `done` or `failed`
      (`TestBerthProgressEvents`)

### PR 5: schemas
- [ ] A JSON Schema file per document, generated from the Go types
- [ ] CI fails when a schema is stale, or a golden document doesn't validate

## Acceptance criteria

| Criterion (from the issue) | How it is checked |
|---|---|
| The parity suite is unchanged | `TestParity` and `TestParityReadOnly` pass with no scenario edited |
| Every read command has JSON output, covered by golden tests | `TestBerthJSONOutput`; the catalog test (PR 2) |
| Every operation declares its restart and write flags | `TestCatalogCoversEveryCommand` (already there) |

## Out of scope

The TUI, MCP and API themselves (#59, #61, #62), and moving write operations' bodies out of `internal/app`.
