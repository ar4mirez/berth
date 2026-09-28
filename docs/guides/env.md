# Secrets and environment variables

## Custom variables (API keys)

```bash
berth env acme set OPENROUTER_API_KEY       # prompts for the value (hidden), or reads one line from stdin
echo "$KEY" | berth env acme set OPENROUTER_API_KEY
berth env acme                              # names only, never values (--output json too)
berth env acme unset OPENROUTER_API_KEY
berth env acme set OPENROUTER_API_KEY --no-restart   # apply at the next restart instead
```

**When a variable applies:** the container is recreated so a new value takes effect; `--no-restart` defers that. Once
applied, variables reach every kind of session: Remote Control, `attach`, `claude`, `run`, the browser terminal and
SSH.

**What's refused:** berth's own keys (tokens, ports, `GH_TOKEN`, …) can't be set here, and values can't contain `'`.

## Where tokens and variables live

By default, the Claude token, `GH_TOKEN` and custom variables are in `org.env` (0600). They reach the container as its
environment, so anyone with Docker access on the host can read them with `docker inspect`.

`berth secrets migrate acme` moves them into one file each, under the org's `config/secrets/env/`. The container reads
them at start, and they're no longer in its environment. A backup is taken first, and nothing restarts: the org uses
the files from its next restart on. [Secrets as files](../secrets.md) has the details.

## The browser terminal password

32 random characters, kept in a 0600 file. It never appears in `org.env`, the container's environment, `berth info`
or the logs.

```bash
berth password acme           # show it
berth password acme rotate    # a new one (restarts a running org so the terminal uses it)
```
