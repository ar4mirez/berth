# Secrets and environment variables

## Custom variables (API keys)

```bash
berth env set acme OPENROUTER_API_KEY       # prompts for the value (hidden), or reads one line from stdin
echo "$KEY" | berth env set acme OPENROUTER_API_KEY
berth env ls acme                           # names only, never values (--output json too)
berth env unset acme OPENROUTER_API_KEY
berth env set acme OPENROUTER_API_KEY --no-restart   # apply at the next restart instead
```

**When a variable applies:** the container is recreated so a new value takes effect; `--no-restart` defers that. Once
applied, variables reach every kind of session: Remote Control, `attach`, `claude`, `run`, the browser terminal and
SSH.

**What's refused:** berth's own keys (tokens, ports, `GH_TOKEN`, …) can't be set here, and values can't contain `'`.

## Setting a secret from inside the org

When the value is on your phone or another machine, and not on the host, type it in the org's own terminal (the
browser terminal, SSH, `berth attach`):

```console
(acme) /workspace $ berth-secret-drop OPENROUTER_API_KEY
Value for OPENROUTER_API_KEY (hidden):
Dropped OPENROUTER_API_KEY. It isn't set yet: on the host, run  berth env accept acme
```

Then, on the host:

```bash
berth env accept acme --list              # the names waiting (never the values)
berth env accept acme                     # set them all, as env set would; or: accept OPENROUTER_API_KEY
berth env accept acme --no-restart        # apply at the next restart instead
```

- **The value is never typed, pasted or passed on a command line on the host**, and it doesn't go through a chat
  or a shell history. `accept` reads it from the container and stores it where `env set` does.
- **The host decides.** Until you accept, nothing in the org changes. Each name is checked as `env set` checks it,
  so berth's own keys are refused, and a value must be one line of text. If any drop is refused, none is accepted.
- **Claude can't read a pending drop**, and a drop is removed once it is accepted. In the org,
  `berth-secret-drop --list` shows what is waiting and `--cancel KEY` takes one back.
- A running org is recreated so the variable reaches its sessions, as with `env set`.

With the value in a password manager on the host, a pipe does it without the drop: `op read 'op://vault/item/field'
| berth env set acme OPENROUTER_API_KEY`.

## Where tokens and variables live

By default, the Claude token, `GH_TOKEN` and custom variables are in `org.env` (0600). They reach the container as its
environment, so anyone with Docker access on the host can read them with `docker inspect`.

`berth env migrate acme` moves them into one file each, under the org's `config/secrets/env/`. The container reads
them at start, and they're no longer in its environment. A backup is taken first, and nothing restarts: the org uses
the files from its next restart on. [Secrets as files](../secrets.md) has the details.

## The browser terminal password

32 random characters, kept in a 0600 file. It never appears in `org.env`, the container's environment, `berth info`
or the logs.

```bash
berth org password show acme           # show it
berth org password rotate acme    # a new one (restarts a running org so the terminal uses it)
```
