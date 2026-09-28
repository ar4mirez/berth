# Repos

An org can only use the repos registered for it. You register them from the host, and Claude can't add any itself.
[Concepts](../concepts.md#the-repo-allowlist) explains the three layers that enforce this.

```bash
berth repo add acme acme/webapp                        # register and clone (owner/repo, an ssh or https URL)
berth repo add acme acme/api --branch develop --dir api-dev
berth repo add acme acme/webapp --no-clone             # register only; cloned by the next repo sync
berth clone acme acme/webapp                           # the same as repo add
berth repo ls acme                                     # registered repos, branch, uncommitted changes, strays
berth repo audit acme                                  # check the registry against what's in /workspace
berth repo rm acme api-dev [--delete]                  # unregister (the folder is quarantined, or deleted)
berth repo adopt acme <dir>... | --all                 # register folders already there, or bring one back from quarantine
berth repo sync acme                                   # clone what's registered but missing (rehydrate does it too)
berth repo policy acme [enforce|warn|off]              # show or set the policy (setting it restarts a running org)
```

## New repos

```bash
berth repo new acme acme/new-svc --private -d "New service" --gitignore Node   # create on GitHub, register, clone
berth repo new acme acme/new-app --template acme/app-template                  # from a template repo
berth repo new acme prototype --local                 # no GitHub repo yet: git init; Claude can commit locally
berth repo publish acme prototype acme/prototype --private   # later: create it on GitHub, push, switch the remote
```

Repos are created on GitHub from the **host**, with your own `gh` login. When the host has no `gh`, berth uses the
container's (`berth gh-login acme`).

## The policy

`REPO_POLICY` in `org.env`:

| Value | Effect |
|---|---|
| `enforce` (default) | Claude and git are blocked for unregistered repos, and anything else in `/workspace` is quarantined |
| `warn` | Nothing is blocked; unregistered items are logged in `/run/repo-policy.log` |
| `off` | No checks |

## Things to know

- **Registered form:** a registered repo's URL is compared in a canonical form, `host/path`, lowercased, with no
  `.git` and no default port. [Repo policy](../repo-policy.md) has the exact rules.
- **The org's key:** the org's SSH key needs access to the repo on GitHub. `berth info acme` prints the public key.
- **A known limit:** someone typing commands in the container (not Claude) could fetch an unregistered repo's code
  with a signed-in `gh` or a public tarball URL. The sweep quarantines whatever lands in `/workspace`, but files kept
  elsewhere, such as `/tmp`, stay. For the strictest setup, don't run `gh-login` in that org.
