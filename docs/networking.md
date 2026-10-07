# Networking: reaching your orgs

An org's SSH and browser terminal listen on the address its `BIND_ADDR` names (in `org.env`). The container itself
never needs inbound access for Claude to work; this is only about how *you* reach it. Remote Control
(claude.ai/code) connects outbound, so it works whatever you choose here.

| `BIND_ADDR` | Listens on | Reach it from | Good for |
|---|---|---|---|
| `tailscale` | this host's Tailscale IPv4 (resolved at `up`) | any device on your tailnet | **the default when Tailscale is installed, and the recommended setup** |
| `iface:<name>` | that interface's IPv4 (resolved at `up`), such as `wg0` or `zt0` | the devices on that VPN | WireGuard, ZeroTier, Netbird, Headscale, a corporate VPN |
| `ip:<addr>` or an address | that address | whatever can route to it | a fixed private address (a LAN, a cloud's private network) |
| `localhost` | `127.0.0.1`, through an SSH tunnel: `berth org connect` | wherever you can SSH to the host | no VPN at all |
| `127.0.0.1` | `127.0.0.1` | this host only (`berth attach`) | the default without Tailscale |
| `0.0.0.0` | every interface | anyone who can reach the host | avoid: berth warns at each `up` |

**When the org sees a change:** `org.env` changes apply at the org's next `berth restart`. `berth info <org>` always
shows the address and commands for its mode.

## Tailscale (recommended)

[Tailscale](https://tailscale.com) puts your machines on one private network, with no ports opened to the internet:

- **Nothing to configure per device:** every device on your tailnet reaches the org, and nothing else can.
- **Addresses follow the host:** `tailscale` is resolved when the org starts, so a new IP, or a restore on another
  host, just works.
- **It's the default:** `berth org create` picks it whenever the `tailscale` command is installed.

If Tailscale isn't up when an org starts, berth says so (`BIND_ADDR=tailscale but tailscale is not up on this host`).
A restore on a host without Tailscale binds the org to `127.0.0.1`, and says so.

## Another VPN: `iface:<name>`

For WireGuard, ZeroTier, Netbird, Headscale (self-hosted Tailscale) or any VPN that gives the host an interface:

```bash
$EDITOR ~/.local/share/berth/orgs/acme/org.env     # BIND_ADDR=iface:wg0
berth restart acme
berth info acme                      # shows the interface's address
```

**How it resolves:** `iface:wg0` is the interface's first IPv4 address when the org starts, like `tailscale` is.

**Things to know:**

- **The interface must be up before the org starts.** If it's missing, `up` fails with a clear message, and a restore
  binds to `127.0.0.1` instead.
- **Headscale:** with Headscale, the `tailscale` mode works as is, since it's the same client.

## A fixed address: `ip:<addr>`

`ip:10.0.4.12` (or just `10.0.4.12`, ccenv's form) binds to that address, when it's stable: a LAN address, or a
cloud VM's private network, reached through the provider's VPN or a bastion.

## No VPN: `localhost` and `berth org connect`

Bind to `localhost`, and reach the org through SSH, which you already have to the host:

- **On a registered host:** `berth org connect acme@box1` opens the tunnel. The org's SSH and browser terminal then answer
  on this machine's `127.0.0.1`, on the same ports. Leave it running, and Ctrl-C closes it. It uses berth's own key
  and pinned host key for that host.
- **On a machine you SSH into yourself:** `berth info acme` prints the `ssh -N -L …` command to run from your laptop.

```bash
berth org connect acme@box1
#   SSH                ssh -p 2201 node@127.0.0.1
#   Browser terminal   http://127.0.0.1:7701
```

**When the tunnel is closed:** SSH and the browser terminal aren't reachable, but Claude in the org keeps working,
and Remote Control too.

## `0.0.0.0`

`0.0.0.0` listens on every interface. Anyone who can reach the host can then try the org's SSH (key-only) and its
browser terminal (password-protected). berth warns at each `up` and `restart`. If you need it, put a firewall in front
of the host.

## Defaults for new orgs

`berth org create` writes `BIND_ADDR` into the new org's `org.env`. By default that's `tailscale` when Tailscale is
installed, else `127.0.0.1`, as ccenv did.

| For | Set |
|---|---|
| this machine | `bind: iface:wg0` in `~/.config/berth/config.yaml`, or `BERTH_BIND` |
| a registered host | `berth host add box1 ops@box1.example --bind localhost` (stored in `hosts.yaml`) |

Existing orgs keep what their `org.env` says.

## Reaching hosts

berth reaches a registered host over SSH, at whatever address you gave `berth host add`. That can be:

- a Tailscale name or IP;
- an address on any other VPN;
- a public address with key-only SSH.

Tailscale isn't required. berth connects directly, so an SSH jump host (bastion) isn't supported yet (#99). Until
then, reach such hosts over a VPN.
