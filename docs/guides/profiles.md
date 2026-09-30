# Profiles

A profile sets up a new org the same way each time: its firewall, its model provider, its variables, and a script that
installs what every session needs. A team keeps one profile and runs `init` with it:

```bash
berth init acme --profile team-profile.yaml
berth env acme set AWS_BEARER_TOKEN_BEDROCK     # the secrets the profile lists; init prints these
berth up acme
```

`init` checks the whole profile before it creates anything. A profile applies once, at `init`. After that, change the
org with `berth fw`, `berth env`, or by editing `config/setup.sh`.

## The file

```yaml
profile: berth.profile/v1        # required
provider: bedrock                # anthropic (the default) | bedrock | vertex | openrouter
region: us-east-1                # bedrock and vertex only
env:                             # set as `berth env acme set` does
  CLAUDE_CODE_ENABLE_TELEMETRY: "1"
  OTEL_METRICS_EXPORTER: otlp
secrets:                         # set after init with `berth env acme set NAME`; values never go in the profile
  - AWS_BEARER_TOKEN_BEDROCK
firewall:                        # in place of the default presets (@mise @python @go @rust @ruby)
  - "@node"
  - github.com
setup: |                         # bash, run as node at every container start
  echo "installing the team's tools"
```

Unknown keys are refused, so a typo doesn't pass silently.

| Key | What init does with it |
|---|---|
| `provider`, `region` | Writes `provider <name> [region]` to `config/firewall.txt`: that provider's endpoints replace Anthropic's ([the firewall](firewall.md#the-model-provider)). Also sets the variables that point Claude Code at it: `CLAUDE_CODE_USE_BEDROCK=1` and `AWS_REGION`; `CLAUDE_CODE_USE_VERTEX=1` and `CLOUD_ML_REGION`; or `ANTHROPIC_BASE_URL=https://openrouter.ai/api` |
| `env` | Sets each variable as `berth env acme set` does, and lists it in `CCENV_ENV_KEYS`, so SSH sessions get it too. A value here wins over the provider's. Values are plain text in the profile: put secrets under `secrets` |
| `secrets` | Nothing is stored. `init` prints a `berth env acme set NAME` line for each |
| `firewall` | The entries follow the provider line in `config/firewall.txt`, in place of the default presets. Without `firewall`, the default presets stay |
| `setup` | Written to `config/setup.sh` |

## The setup script

At every start, before sessions open, the container runs `bash /config/setup.sh` as `node`, from `/home/node`, with
the org's variables. It has 5 minutes. Its output goes to `/run/berth-setup.log`. If it fails or times out, the
container logs `setup: failed (exit N)` and starts anyway.

Write it to be run again: check before installing, and update what's already there. Install into a directory that
persists and is on `PATH`, such as `/home/node/.mise/go/bin`, or with `mise use -g`. Anything the script downloads must
be allowed by the firewall, which is up before the script runs.
