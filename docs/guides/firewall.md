# The firewall

Each org's egress is **default-deny**. GitHub, npm and the model provider (Anthropic, [unless you pick another](#the-model-provider)) are always allowed; anything else must be in the org's
allowlist, `config/firewall.txt`. Changes apply to the running container at once, with no restart.

```bash
berth fw show acme                          # the allowlist, and the live status (on, and how many networks)
berth fw allow acme @python pypi.org        # presets, domains, IPs, CIDRs, or URLs (the host is taken)
berth fw allow acme https://sentry.example.com/api
berth fw deny acme pypi.org                 # take an entry out
berth fw test acme pypi.org example.com     # can the org reach them now?
berth fw presets acme                       # the presets and what each allows
berth fw off acme                           # full access, until: berth fw on acme
berth fw edit acme                          # edit firewall.txt in $EDITOR, then apply
berth fw reload acme                        # re-apply (a host's addresses changed)
```

## Presets

`@mise @python @node @go @rust @ruby @docker @gitlab @bitbucket @aws @gcp @azure @debian`

New orgs start with `@mise @python @go @rust @ruby`, which cover toolchain downloads and the usual package registries.

## When something is blocked

Claude is told about the firewall. When it hits a blocked host, it tells you the `berth fw allow <org> …` command to
run. If a site still fails after you allow it:

- **Dependencies:** it may depend on other hosts (a CDN, an auth domain). `berth fw test acme <host>` shows what
  answers.
- **Changed addresses:** its addresses may have changed since the last resolve. `berth fw reload acme` re-resolves now
  (it happens every 5 minutes anyway).
- **Guessing the host:** `berth fw off acme`, try it, then `berth fw on acme`. That tells you whether the firewall is
  the problem, then close it again.

## Domains and wildcards

A domain allows that name **and every name under it**: `example.com` also allows `api.example.com`. `*.example.com`
means the same thing. The firewall matches whole labels only, so a `*` anywhere else is refused:

```console
$ berth fw allow acme 'web-git-*.example.app'
berth: 'web-git-*.example.app': a wildcard only works as a leading '*.' (the firewall can't match part of a label). List each host, or allow the whole domain, like '*.example.com'
```

For hosts like preview deployments, either list each one, or allow the domain above them, knowing that it allows
every name under it (`*.vercel.app` is every Vercel app).

`berth fw allow` also refuses anything else the firewall can't use: an IPv6 address, a host with a port, a name with
other characters. Nothing is written when an entry is refused.

## The file

One entry per line: `mode on|off`, a domain, an IPv4 address or range, an `@preset`, or a `provider` line. Lines
starting with `#` are comments. `/config` is mounted read-only, so nothing inside the container can change it.

An entry the firewall can't use (written by `berth fw edit acme`, or by an older berth) is **skipped**, and the
rest of the list applies. berth says so each time the list is applied, and ends with exit code 3:

```console
$ berth fw reload acme
firewall: skipping 'web-git-*.example.app': a wildcard only works as a leading '*.' (list each host, or allow the whole domain, like '*.example.com')
firewall: ON (187 allowlisted networks)
firewall: WARNING skipped (see above): web-git-*.example.app. Remove each with: berth fw deny acme '<entry>'
```

`berth fw show acme` then shows `live: on 187 (1 skipped)`. At a container start, the same lines are in `berth logs acme`.

## DNS

Names resolve through a resolver inside the container, which asks the one Docker gave the container, and adds each
answer for an allowlisted name to the allowlist as it goes. DNS to any other resolver is refused, so a lookup can't
carry data out.

Each time the list is applied, berth checks that a lookup still gets an answer. If none does, it says so
(`firewall: WARNING DNS isn't answering …`), the status reads `on 187 (DNS not answering)`, and the command ends with
exit code 3. While DNS is down, the addresses already allowed stay allowed
([Troubleshooting](../troubleshooting.md#nothing-resolves)).

## The model provider

Anthropic's endpoints are allowed unless the file has a `provider` line. With one, that provider's endpoints replace
Anthropic's; GitHub and npm stay. `berth org create --profile` writes it ([Profiles](profiles.md)); you can also add it with
`berth fw edit acme`.

| Line | Allowed |
|---|---|
| `provider anthropic` (the default) | `api.anthropic.com`, `claude.ai` and the other Claude Code endpoints |
| `provider bedrock <region>` | `bedrock-runtime.<region>.amazonaws.com`, `bedrock.<region>.amazonaws.com`, `sts.<region>.amazonaws.com`, `sts.amazonaws.com` |
| `provider vertex <region>` | `<region>-aiplatform.googleapis.com`, `aiplatform.googleapis.com`, `oauth2.googleapis.com`, `www.googleapis.com` |
| `provider openrouter` | `openrouter.ai` |

A line with an unknown provider, or without the region Bedrock and Vertex need, is reported by `berth fw reload acme`,
and Anthropic's endpoints stay.

## On a registered host

The [host guard](../hosts.md#the-host-guard) adds a host-level layer beneath the org's firewall. Org containers can't
reach the host's own services or cloud metadata (`169.254.169.254`), whatever the allowlist says.
