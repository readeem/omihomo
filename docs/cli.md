# Command line

`/usr/bin/omihomo` is the packaged Go binary. Client commands use the daemon's owner-only socket;
`daemon` runs the service and `claim` performs privileged installation setup.

```text
omihomo status
omihomo start | stop
omihomo set mode <rule|global|direct>
omihomo set tun|tun-redirect|tailscale|tailnet-proxy <on|off>
omihomo set group <name>
omihomo sub list
omihomo sub add                         # HTTPS URL on stdin
omihomo sub update|activate|remove <name>
omihomo rule list
omihomo rule add <type> <value> <target>
omihomo rule remove <index>
omihomo group list
omihomo group select <group> <proxy>
omihomo group test <group> [proxy...]
omihomo conn list
omihomo conn close <id>... | close --all
omihomo egress
omihomo watch [stream...]
omihomo version
sudo /usr/bin/omihomo claim [--legacy-data DIR] [--legacy-config DIR]
omihomo daemon [--core mihomo|fake] [--home DIR] [--state DIR] [--socket PATH] [--owner-file PATH]
```

Results are JSON lines on stdout; `--pretty` indents them. Pass flags before positional arguments,
and use `--` before names starting with a dash. `-h` prints command-specific help.
`OMIHOMO_SOCKET` selects a different socket for clients; the default is `/run/omihomo/omihomo.sock`.
`sub add` reads its URL from stdin so credentials never enter the process list:

```sh
omihomo sub add < /path/to/private-subscription-url
```

Keep that file owner-only. Subscription lists and logs never reveal URLs.

`claim` records the invoking sudo/pkexec account as the single owner and reconciles systemd.
On an old installation it first stages a one-time import, then removes the old user units,
pacman privilege hooks and CLI symlink, strips setuid/capabilities/ACLs from old mihomo, and
starts the new daemon. An old running proxy must successfully start in the new daemon before
cutover succeeds. A failed operation restores the old runtime. The old data is kept for recovery.

The defaults are the invoking account's `.local/share/omihomo` and `.config`. For a custom XDG
layout, run the plugin's `bin/omihomo-install`, which passes both paths, or pass the flags yourself.
Run cutover in a local terminal after ending any session that depends on the existing proxy.
Supported override fields and typed prepend rules migrate; arbitrary YAML fields are ignored.
Raw append/filter rules and prepend rules the old form could not create are rejected before
changing the old runtime, with a conversion instruction. Existing daemon state is never replaced.

| Exit | Meaning |
| ---: | --- |
| 0 | Success |
| 1 | Failure |
| 2 | Usage or invalid value |
| 10 | Daemon unreachable |
| 11 | Conflict or proxy stopped |
| 12 | Not found |
| 13 | Account refused |
| 14 | Protocol mismatch |
| 15 | Unsupported operation |
| 20 | Subscription content rejected |
| 21 | Download failed |

See [daemon.md](daemon.md) for protocol data, limits, rule types, refresh scheduling and state.
