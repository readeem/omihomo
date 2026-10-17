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
The legacy path flags default to that account's `.local/share/omihomo` and `.config`.
See [upgrading an old installation](upgrade.md) for the one-time import and cleanup.

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
