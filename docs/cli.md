# Omihomo CLI

`bin/omihomo` is the disk and systemd boundary for the plugin. The panel invokes this file by
absolute path from the plugin directory. The CLI does not wrap mihomo API operations for group or
config selection, latency, connections, or parameter lookup.

## Commands

```text
omihomo core install|repair|start|stop|restart|version
omihomo core uninstall [--keep-data]
omihomo core autostart on|off

omihomo sub add <name> <url>
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
| 11 | User unit is not active |
| 12 | Unit is active but the mihomo API is unreachable |
| 13 | No active subscription |
| 20 | YAML failed `mihomo -t` validation |
| 21 | Subscription fetch failed |

`status` is the exception: it always exits `0` and returns one stable object. Its `state` is
`not-installed`, `stopped`, `starting`, `degraded`, or `on`; `detail` and unknown fields are null
when they cannot be observed. The stable object includes `state`, `status`, `detail`, `ip`, `latency`,
`download`, `upload`, `config`, `uptime`, `active_subscription`, `primary_group`,
`tun_enabled`, and `autostart_enabled`. The IP, latency, throughput, and config fields are nullable because those live API
values remain panel-owned per ADR-0001.

## Files

The state directory is `$XDG_DATA_HOME/omihomo`, falling back to `~/.local/share/omihomo`:

```text
subscriptions.json       array of {name, url, updated_at, userinfo}
cache/<name>.yaml        last validated subscription fetch
override.yaml            global user-owned settings and rule lists
runtime.yaml             merged YAML loaded by mihomo
active                   active subscription name, one line
.lock                    shared write lock
```

Subscription updates fetch to a temporary file, validate with `mihomo -t`, and replace the cache
only after validation succeeds. Updating the active subscription merges `runtime.yaml` and sends
`PUT /configs?force=true`; the systemd unit is not restarted. Every mutation of the state files
holds the one shared `flock`.

`core autostart on|off` is `systemctl --user enable|disable` on the unit, and is what the panel's
manage view toggles. Turning it on writes the unit first when it is missing, so autostart works on
a core that has never been started.

`core install` installs the AUR package and the required `go-yq`, `jq`, `libcap`, `curl`, and
`nftables` tools, writes the user unit and default override, and performs the single privileged
capability and pacman-hook step. It runs unattended: pacman and yay get `--noconfirm`, and one
`sudo -v` up front covers every privileged step, so nothing pops a second prompt. It is
terminal-only, because pacman's output and that one password prompt need somewhere to go. `core
repair` is the non-build capability reapplication path.

The YAML tool has to be mikefarah's yq v4, packaged on Arch as `go-yq`. Arch's `yq` package is
kislyuk's jq wrapper, which owns the same `/usr/bin/yq` and speaks a different language; `core
install` replaces it, and every other command fails with an explicit message when the wrong one is
on `PATH`.

`core uninstall` reverses all of it: it stops and disables the unit, removes the capabilities and
their pacman hook, drops the `~/.local/bin/omihomo` symlink and the unit file, removes the
`mihomo-bin` package, and deletes the state directory. `--keep-data` keeps subscriptions, override,
and cache. Removing the widget itself is `omarchy plugin remove omihomo`.
