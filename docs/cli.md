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
| 20 | Subscription content was rejected by Mihomo |
| 21 | Subscription fetch failed |

`status` is the exception: it always exits `0` and returns one stable object. Its `state` is
`not-installed`, `stopped`, `starting`, `degraded`, or `on`; `detail` and unknown fields are null
when they cannot be observed. The stable object includes `state`, `status`, `detail`, `ip`, `latency`,
`download`, `upload`, `config`, `uptime`, `active_subscription`, `primary_group`,
`tun_enabled`, `autostart_enabled`, and `permissions_ok`. The IP, latency, throughput, and config
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

Subscription updates prepare the candidate cache, metadata, and active runtime in temporary files.
An active core receives `PUT /configs?force=true` before Omihomo replaces durable state. A failed
conversion, merge, or reload keeps the previous cache, metadata, and runtime. The systemd unit is
not restarted. Every mutation of the state files holds the one shared `flock`.

`set tun on` is unprivileged. It updates the runtime config and hot-reloads a running core. If the
core is stopped, the same command starts its user unit after preparing the TUN-enabled runtime.
An active subscription and the root permissions installed with the core are required.

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
`755` mode and removes the pacman hook, drops the `~/.local/bin/omihomo` symlink and the unit file, removes the
`mihomo-bin` package, and deletes the state directory. `--keep-data` keeps subscriptions, override,
and cache. Removing the widget itself is `omarchy plugin remove omihomo`.
