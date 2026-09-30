# The firewall

Each org's egress is **default-deny**. GitHub, npm and the model provider (Anthropic, [unless you pick another](#the-model-provider)) are always allowed; anything else must be in the org's
allowlist, `config/firewall.txt`. Changes apply to the running container at once, with no restart.

```bash
berth fw acme                               # the allowlist, and the live status (on, and how many networks)
berth fw acme allow @python pypi.org        # presets, domains, IPs, CIDRs, or URLs (the host is taken)
berth fw acme allow https://sentry.example.com/api
berth fw acme deny pypi.org                 # take an entry out
berth fw acme test pypi.org example.com     # can the org reach them now?
berth fw acme presets                       # the presets and what each allows
berth fw acme off                           # full access, until: berth fw acme on
berth fw acme edit                          # edit firewall.txt in $EDITOR, then apply
berth fw acme reload                        # re-apply (a host's addresses changed)
```

## Presets

`@mise @python @node @go @rust @ruby @docker @gitlab @bitbucket @aws @gcp @azure @debian`

New orgs start with `@mise @python @go @rust @ruby`, which cover toolchain downloads and the usual package registries.

## When something is blocked

Claude is told about the firewall. When it hits a blocked host, it tells you the `berth fw <org> allow …` command to
run. If a site still fails after you allow it:

- **Dependencies:** it may depend on other hosts (a CDN, an auth domain). `berth fw acme test <host>` shows what
  answers.
- **Changed addresses:** its addresses may have changed since the last resolve. `berth fw acme reload` re-resolves now
  (it happens every 5 minutes anyway).
- **Guessing the host:** `berth fw acme off`, try it, then `berth fw acme on`. That tells you whether the firewall is
  the problem, then close it again.

## The file

One entry per line: `mode on|off`, a domain, an IP or CIDR, an `@preset`, or a `provider` line. Lines starting with
`#` are comments. `/config` is mounted read-only, so nothing inside the container can change it.

## The model provider

Anthropic's endpoints are allowed unless the file has a `provider` line. With one, that provider's endpoints replace
Anthropic's; GitHub and npm stay. `berth init --profile` writes it ([Profiles](profiles.md)); you can also add it with
`berth fw acme edit`.

| Line | Allowed |
|---|---|
| `provider anthropic` (the default) | `api.anthropic.com`, `claude.ai` and the other Claude Code endpoints |
| `provider bedrock <region>` | `bedrock-runtime.<region>.amazonaws.com`, `bedrock.<region>.amazonaws.com`, `sts.<region>.amazonaws.com`, `sts.amazonaws.com` |
| `provider vertex <region>` | `<region>-aiplatform.googleapis.com`, `aiplatform.googleapis.com`, `oauth2.googleapis.com`, `www.googleapis.com` |
| `provider openrouter` | `openrouter.ai` |

A line with an unknown provider, or without the region Bedrock and Vertex need, is reported by `berth fw acme reload`,
and Anthropic's endpoints stay.

## On a registered host

The [host guard](../hosts.md#the-host-guard) adds a host-level layer beneath the org's firewall. Org containers can't
reach the host's own services or cloud metadata (`169.254.169.254`), whatever the allowlist says.
