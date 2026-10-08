# Decision: the first cloud provider is Hetzner Cloud (#51)

Decided 2026-10-08 by the maintainer. `berth host create` (#52) is built for it.

## What berth needs, on each

The figures are from the providers' documentation as I know it, not measured here. Check prices and limits on
their own pages before relying on them.

| | Hetzner Cloud | DigitalOcean |
|---|---|---|
| Go SDK | `hcloud-go`, official | `godo`, official |
| API tokens | per project, read or read-and-write. A project for berth alone is the scope | per team, with custom scopes per resource type; not limited to tagged resources |
| Finding berth's own resources | labels (key and value) on servers, firewalls, networks, keys; selectors in every list call | tags on droplets and some other resources; firewalls can be applied by tag |
| cloud-init user data | up to 32 KiB | up to 64 KiB |
| Firewall, private network | Cloud Firewalls, stateful, applied at creation; a firewall with no rules lets nothing in. Private Networks | Cloud Firewalls; VPC |
| Metadata service | 169.254.169.254; holds the user data, no credentials (there are no instance roles) | 169.254.169.254; holds the user data, no credentials |
| arm64 and amd64 | both: shared arm64 (CAX) and x86 (CX, CPX) | amd64 only |
| A host for 1–5 orgs (4 vCPU, 8 GB and up) | a few euros a month on arm64 | several times that |
| Regions | Germany, Finland, US east and west, Singapore | more: Americas, Europe, Asia, Australia |
| Snapshots | yes, and scheduled backups | yes, and scheduled backups |

**Tailscale-only access** works the same on both: cloud-init installs Tailscale and joins the tailnet with an auth
key, and a provider firewall with no inbound rule keeps the public address closed. Neither needs an open port for
Tailscale.

## Why Hetzner

- **Cost and arm64.** An org wants 4 vCPU and 8 GB by default; arm64 at Hetzner is the cheapest way to give it
  that, and berth's image is built for arm64 already.
- **Labels with selectors** are what "reconcile by tag, no orphans" needs, on every resource berth creates.
- **A project is the token's scope**, which is coarse but simple: a project for berth's hosts, and the token can
  touch nothing else.

What it costs: fewer regions, and none in South America, Africa or Australia.

## Constraints for #52

- **The token can do anything in its project.** The docs tell the operator to give berth a project of its own.
- **User data can be read back** by anything on the VM that reaches 169.254.169.254, and it holds the ssh host key
  berth pre-generated and the Tailscale auth key. So: the auth key must be single-use, and org containers must not
  reach the metadata service. The host guard already drops 169.254.0.0/16 from org networks, and each org's own
  firewall doesn't allow it either.
- **There is no instance role to turn off** and no IMDSv2: the lock-down the issue asks for is the guard's drop.
- **32 KiB of user data** is enough for what berth sends (about 2 KiB), and the create refuses more.
