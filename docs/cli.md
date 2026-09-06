# Omihomo CLI

`bin/omihomo` is the disk and systemd boundary for the plugin. The panel invokes this file by
absolute path from the plugin directory. The CLI does not wrap mihomo API operations for group or
config selection, latency, connections, or parameter lookup.

## Commands

```text
omihomo core install|repair|start|stop|restart|version
omihomo core uninstall [--keep-data]
omihomo core autostart on|off

omihomo sub add <url>
omihomo sub list
omihomo sub remove <name>
omihomo sub update <name>
omihomo sub activate <name>

omihomo rule add <type> <value> <target>
omihomo rule list
omihomo rule remove <index>
omihomo rule raw [prepend|append|filter] <rule>

omihomo set mode <rule|global|direct>
omihomo set tun <on|off>
omihomo set tun-redirect <on|off>
omihomo set tailscale <on|off>
omihomo set group <name>

omihomo status
omihomo api-info
```

The form-facing rule types are `DOMAIN-SUFFIX`, `DOMAIN-KEYWORD`, `IP-CIDR`, and `PROCESS-NAME`.
`rule raw` is the CLI-only escape hatch for all other mihomo rule syntax. Raw rules go to the
prepend list by default; `append` and `filter` are available only through this command.

## Output

Read verbs emit one JSON object or array on stdout. Objects and array members are flat so the
panel can parse them without knowing the CLI implementation. Add `--pretty` anywhere in a command
to render the successful JSON result for a human. Write verbs emit no stdout on success.

Errors always go to stderr. When stderr is a pipe, which is how the panel reads them, they are a
JSON object:

```json
{"error":"mihomo is not installed","code":10}
```

When stderr is a terminal, or `--pretty` was passed, the same error is one plain line instead:

```text
omihomo: mihomo is not installed (exit 10)
```

`--pretty` also buffers stdout for reformatting, so interactive verbs such as `core install` should
be run without it; without `--pretty` the command keeps the terminal and its build output and
prompts stay visible.

Exit codes are:

| Code | Meaning |
| ---: | --- |
| 0 | Success |
| 1 | Generic failure or invalid arguments |
| 10 | Core is not installed |
| 11 | Operation conflicts with the current core state |
| 12 | Unit is active but the mihomo API is unreachable |
| 13 | No active subscription |
| 14 | The core is missing the root permissions TUN needs |
| 15 | tailscaled is not installed |
| 20 | Subscription content was rejected by Mihomo |
| 21 | Subscription fetch failed |

`status` is the exception: it always exits `0` and returns one stable object. Its `state` is
`not-installed`, `stopped`, `starting`, `degraded`, or `on`; `detail` and unknown fields are null
when they cannot be observed. The stable object includes `state`, `status`, `detail`, `ip`, `latency`,
`download`, `upload`, `config`, `uptime`, `active_subscription`, `primary_group`,
`tun_enabled`, `tun_redirect`, `autostart_enabled`, `permissions_ok`, `tailscale_enabled`, and
`tailscale_present`. The IP, latency, throughput, and config
fields are nullable because those live API values remain panel-owned per ADR-0001.

## Files

The state directory is `$XDG_DATA_HOME/omihomo`, falling back to `~/.local/share/omihomo`:

```text
subscriptions.json       array of {name, url, updated_at, userinfo}
cache/<name>.yaml        validated config or generated raw-subscription wrapper
override.yaml            global user-owned settings and rule lists
runtime.yaml             merged YAML loaded by mihomo
active                   active subscription name, one line
.lock                    shared write lock
```

`sub add` takes only the URL. The subscription names itself: the name is the `profile-title`
response header, base64-decoded when it carries the `base64:` prefix, falling back to the
`content-disposition` filename and then to the URL's host. Because that name is a server's to
write and Omihomo uses it as both a cache filename and a CLI argument, it is stripped of path
separators and control characters, trimmed, and capped at 64 characters; a name already taken
gets a numeric suffix. Adding a URL that is already on the list is an error. A subscription keeps
the name it was added under: `sub update` refreshes its YAML and quota, not its name.

Fetches identify as `clash.meta`. `OMIHOMO_USER_AGENT` overrides the string for a server that wants
a different one. Omihomo accepts full Mihomo YAML and raw subscription formats supported by the
installed Mihomo version. It does not promise support for arbitrary converter formats.

A mapping with a structural Mihomo key such as `proxies`, `proxy-providers`, `proxy-groups`,
`rules`, `rule-providers`, `dns`, or `tun` is treated as a full config and cached unchanged after
`mihomo -t` validation. Other content is preflighted through a file-backed provider in an isolated
temporary core. This forces Mihomo to parse raw base64 and share-link lists without fetching the
URL twice or touching the running service. Rejected content exits with code `20` and leaves no
subscription record or cache.

For accepted raw content, the cached YAML is a generated config with one HTTP provider named
`subscription`, one `Proxy` selector, and a final `MATCH,Proxy` rule. The provider cache path is
`providers/<sha256-of-url>.yaml`, so subscriptions with the same display name remain independent.
Mihomo owns provider refreshes after activation.

`sub add` activates the subscription it just added when nothing is active yet, because `core start`
refuses to run without one. A later `sub add` never takes the slot from the active subscription;
`sub activate` is the way to switch.

The merge sits on a floor of `mixed-port: 7890` and a DNS block enabling `1.1.1.1` and `8.8.8.8`,
applied under the subscription rather than over it. A subscription that names its own inbound
ports or resolvers keeps them, and `override.yaml` still wins over both. Neither default is
cosmetic: TUN answers the machine's DNS through `dns-hijack`, so a runtime with no `nameserver`
cannot resolve even its own proxy servers, and a subscription carrying no inbound is unreachable
with TUN off. Raw subscriptions carry neither, so they get both.

Subscription updates prepare the candidate cache, metadata, and active runtime in temporary files.
An active core receives `PUT /configs?force=true` before Omihomo replaces durable state. A failed
conversion, merge, or reload keeps the previous cache, metadata, and runtime. The systemd unit is
not restarted. Every mutation of the state files holds the one shared `flock`.

A reload of a TUN-enabled runtime is two loads, not one. Mihomo answers a reload that rebuilds a
live TUN adapter with `200` and then logs `configure tun interface: device or resource busy`,
leaving the machine with no tunnel until TUN is switched off and on. So Omihomo loads the same
config once with `tun.enable: false`, waits for the kernel to release the adapter, and then loads
it for real.

`set tun on` is unprivileged. It updates the runtime config and hot-reloads a running core. If the
core is stopped, the same command starts its user unit after preparing the TUN-enabled runtime.
An active subscription and the root permissions installed with the core are required.

The TUN adapter is named `omihomo` through `config.tun.device`, not the `Meta` mihomo defaults to.
Every mihomo-based client uses that default, so on a machine that runs another one the status
probe would read its `Meta` as Omihomo's own working tunnel. An override that predates the name
gains it on the next command that writes state.

When TUN is on and its device is absent, `status` reports `degraded` and reads the reason out of
the unit's journal for the current invocation, so `detail` carries what mihomo actually said.

`set tun-redirect on|off` moves `config.tun.auto-redirect` and `config.tun.stack` together, and
`status` reports the flag as `tun_redirect`. On is `auto-redirect: true` with the `mixed` stack:
sing-tun creates an nftables table and hands TCP to the kernel through it, which is the faster
path but needs that table to itself. Off is `auto-redirect: false` with the `gvisor` stack, which
tunnels entirely in userspace and installs no firewall rules, so it works anywhere.

The two are one setting because the pairing `auto-redirect: false` with `mixed` is the one
combination that comes up looking healthy and carries no TCP at all — `mixed` uses the system TCP
stack, which only ever sees TCP because the redirect puts it there.

A machine where something else already holds an nftables table sing-tun wants reports
`TUN acceleration cannot start on this machine`, and `set tun-redirect off` is the repair. What
holds the table cannot be read without root and is not Omihomo's to take away, so the detail names
the switch rather than guessing at the culprit.

The default override excludes loopback, the private and CGNAT ranges, link-local, the
documentation and multicast blocks, and their IPv6 equivalents from TUN's `auto-route`, through
`config.tun.route-exclude-address`. Without them the LAN goes into the tunnel and the router's web
UI, printers, and local DNS stop answering while TUN is on. The list is the same set Koala Clash
ships, and it replaces any `route-exclude-address` the subscription carries. Edit it in
`override.yaml`; an override that predates the list gains it on the next command that writes state,
which rebuilds `runtime.yaml` too. An emptied list is left as the user left it.

Two more defaults let TUN and Tailscale coexist regardless of the integration below:
`config.tun.exclude-interface` holds `tailscale0`, and `config.dns.nameserver-policy` sends
`+.ts.net` to `100.100.100.100`, without which `dns-hijack: any:53` swallows the MagicDNS lookups
only Tailscale's resolver can answer. They are backfilled and left alone on the same terms.

`set tailscale on` points the machine's tailscaled at a proxy Omihomo opens, so that Tailscale
still reaches its coordination server and DERP relays on a network that blocks them. It needs
tailscaled installed and an active subscription. The runtime gains one `mixed` listener on
`127.0.0.1:7899`, named `omihomo-tailscale`, and four rules: the tailnet's own IPv4 and IPv6
ranges go `DIRECT`, and `tailscale.com` and `tailscale.io` go to `GLOBAL`, which Omihomo already
points at the primary group. They sit behind the user's own prepended rules, so an explicit rule
about Tailscale still wins. Both the listener and the rules are generated during the merge from
the one flag in `override.yaml`, so `set tailscale off` removes them.

The environment tailscaled reads is root-owned, so the toggle also writes
`/etc/systemd/system/tailscaled.service.d/omihomo.conf` through the privileged helper and
`try-restart`s the unit; `off` removes that file. A tailscaled the user has stopped stays stopped.
`core uninstall` removes the drop-in too.

Only TCP goes through it: tailscaled honours `HTTP_PROXY` and `HTTPS_PROXY` for its control plane,
its DERP relays, and its logs, while peer-to-peer WireGuard is UDP and ignores them. On a network
that needs this toggle, peers therefore connect relayed rather than directly. Nothing follows the
core's lifecycle either: with the integration on and the core stopped, Tailscale cannot reach its
control plane. A system unit cannot depend on a user unit, so that is accepted rather than worked
around.

The generated runtime owns Mihomo's `GLOBAL` system group and points it at the resolved primary
subscription group. This makes global mode follow the same selected config as rule mode without
showing `GLOBAL`, `DIRECT`, or `REJECT` as manual config choices. `set mode global` fails when the
active subscription has no groups, and `set group GLOBAL` is rejected.

`core autostart on|off` is `systemctl --user enable|disable` on the unit, and is what the panel's
manage view toggles. Turning it on writes the unit first when it is missing, so autostart works on
a core that has never been started.

`core install` installs the AUR package and the required `go-yq`, `jq`, `curl`, and `nftables`
tools, writes the user unit and default override, and prepares the permissions hook in one
privileged call. `yay --sudoloop` keeps that authorization alive while it installs the AUR
package, and the pacman hook makes the binary setuid root inside that package transaction (see
ADR-0006). Installation is terminal-only because pacman's output and the one setup password
prompt need somewhere to go. `core repair` is the explicit reapplication path, and it also clears
the file capabilities left behind by installs that predate ADR-0006.

The YAML tool has to be mikefarah's yq v4, packaged on Arch as `go-yq`. Arch's `yq` package is
kislyuk's jq wrapper, which owns the same `/usr/bin/yq` and speaks a different language; `core
install` replaces it, and every other command fails with an explicit message when the wrong one is
on `PATH`.

`core uninstall` reverses all of it: it stops and disables the unit, restores the binary's plain
`755` mode and removes the pacman hook and the tailscaled drop-in, drops the
`~/.local/bin/omihomo` symlink and the unit file, removes the `mihomo-bin` package, and deletes
the state directory. `--keep-data` keeps subscriptions, override,
and cache. Removing the widget itself is `omarchy plugin remove omihomo`.
