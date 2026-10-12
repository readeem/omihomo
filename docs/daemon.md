# Omihomo daemon

`cmd/omihomo` is one Go binary: `omihomo daemon` is the root system service that hosts the proxy
core, and every other subcommand is a client of its socket. This page covers the socket protocol,
who may use it, the command line, and the core. The core interface is `internal/core`; the daemon
hosts mihomo in-process behind it, or the in-memory fake (`--core fake`), which proxies nothing.

## Socket and owner

The daemon listens on `/run/omihomo/omihomo.sock`. Under systemd the socket comes from socket
activation, so PID 1 creates it with `SocketUser=` set to the owner and `SocketMode=0600`: the
sandboxed daemon has no `CAP_CHOWN` to hand a socket over itself. Started by hand, it binds
`--socket` itself with mode `0600`, and as root chowns it to the owner. It does so only in a
directory that belongs to its own account and that nobody else can write, so the path cannot be
swapped between the bind and the chown. It refuses to replace a socket another daemon still
answers on.

Exactly one account owns an install. The install step records its uid, in decimal, in
`/var/lib/omihomo/owner`. The daemon reads that file on every connection, so a claim takes effect
without a restart, and trusts it only when it is a regular file, not a symlink, owned by the
daemon's own uid and not writable by group or others.

Every connection is checked against the peer credentials the kernel recorded at `connect()`
(`SO_PEERCRED`), which the peer cannot forge. Anyone but the owner, including root, receives one
`refused` message and is disconnected without a request being read. A missing or untrusted owner
file refuses everyone. The daemon serves at most 16 clients at once and refuses more as `busy`.

## Protocol

JSON lines: one JSON object per line, UTF-8, at most 1 MiB. A longer line from a client ends the
connection. The daemon never sends one: a reply that would exceed the limit is sent as a
`too_large` error for that request instead, and an event as an error event (see Streams).
The vocabulary is proxies, groups, rules and connections; it never names the core behind them.

The daemon speaks first:

```json
{"type":"hello","protocol":1,"version":"v2.0.0"}
{"type":"refused","error":{"code":"forbidden","message":"this account does not own omihomo"}}
```

`protocol` is bumped on any change an older peer would misread. A client that sees another value
must not send requests; the widget shows "update core".

After the greeting the client sends requests. `id` is any positive integer the client picks and is
echoed by the one reply each request gets. Replies can arrive out of request order, so a slow delay
test does not hold up a status read. A client may have 8 calls in flight; more are answered `busy`.

```json
{"id":7,"op":"groups.select","args":{"group":"Proxy","proxy":"Tokyo"}}
{"type":"reply","id":7,"result":{"name":"Proxy","type":"select","now":"Tokyo",...}}
{"type":"reply","id":8,"error":{"code":"not_found","message":"not found"}}
```

Requests and their arguments are strict: an unknown field, a wrong type, or anything after the
object, even a stray `}`, is `bad_request`. The error carries the request's `id` when the line is a
JSON object with a numeric `id`, and `0` when it is not. Ops without arguments accept none, `null`
or `{}`.

| Op | Args | Result |
| --- | --- | --- |
| `status` | | Status |
| `subscribe`, `unsubscribe` | `{"streams":[...]}` | `{}` |
| `start`, `stop` | | Status |
| `settings.set` | any of `mode`, `tun`, `tun_redirect`, `tailscale`, `tailnet_proxy`, `primary_group` | Status |
| `groups.list` | | `{"groups":[Group]}` |
| `groups.select` | `{"group","proxy"}` | Group |
| `delay.test` | `{"group","proxies"?}`, every member when `proxies` is absent | `{"delays":{name: ms}}`, `0` is a failure |
| `rules.list` | | `{"rules":[Rule]}` in match order, each with its `source`: `user`, `omihomo` or `subscription` |
| `connections.list` | | Connections |
| `connections.close` | `{"ids":[...]}` or `{"all":true}` | `{}` |
| `egress.check` | | `{"ip","latency"}`, measured through the proxy |
| `subscriptions.list`, `.add` `{"url"}`, `.update`/`.activate`/`.remove` `{"name"}` | | not implemented yet: `unsupported` |
| `rules.add` `{"type","value","target"}`, `rules.remove` `{"index"}` | | not implemented yet: `unsupported` |

Autostart and repair are gone by design: the service starts at boot and restores the last
on/off state, and as a root service it has no permissions to repair. The field-level shapes are
the Go types in `internal/protocol`. Status is:

```json
{"state":"on","detail":"","active_subscription":"","primary_group":"","mode":"rule",
 "tun":false,"tun_redirect":false,"tailscale":false,"tailscale_present":true,
 "tailnet_proxy":false,"started_at":1791741355865,"tun_bypass":["0.0.0.0/8","10.0.0.0/8",...]}
```

`state` is `stopped`, `starting`, `on` or `degraded`; `detail` says why a start failed. A new
`started_at` is a new runtime, and anything read from the previous one is stale. `settings.set`
on a running proxy applies the change to the core first; if the core refuses it, nothing changes.
`tun_bypass` is the address space TUN routes around: loopback, private, CGNAT, link-local,
documentation and multicast ranges. With `tailnet_proxy` the tailnet leaves it, all but
Tailscale's MagicDNS address.

Connections lists at most the 1000 newest connections, so the list fits in one message; `open`
counts them all, and the totals cover them all.

### Streams

After `subscribe`, the daemon pushes events between replies:

```json
{"type":"event","stream":"traffic","data":{"up":2048,"down":65536}}
```

| Stream | Data | When |
| --- | --- | --- |
| `status` | Status | on every change |
| `groups` | `{"groups":[Group]}` | after a selection, a delay test, a new runtime, or a change the core made itself, checked every 5 seconds |
| `rules` | `{"rules":[Rule]}` | when the runtime's rules change |
| `traffic` | `{"up","down"}` bytes per second | every second while running |
| `connections` | Connections | every second while running |

The first three are state streams. Subscribing to one pushes its current value before the reply,
and later pushes reach every subscriber whoever caused the change, so a widget stays current when
the CLI changes something. The periodic streams cost nothing while no client subscribes. A client
that stops reading until 256 messages are queued for it is disconnected.

A value too large for one message is pushed as an error in place of `data`. The subscriber's copy
of that stream is stale until a later push carries data again:

```json
{"type":"event","stream":"groups","error":{"code":"too_large","message":"..."}}
```

### Errors

| Code | Meaning |
| --- | --- |
| `bad_request` | not a request, or args that do not decode |
| `unknown_op` | no such op |
| `invalid` | a value is not allowed |
| `not_found` | a named group, proxy or connection does not exist |
| `not_running` | the call needs the proxy running |
| `conflict` | the call contradicts the current state, e.g. Tailscale without tailscaled |
| `unsupported` | a known op this daemon does not implement yet |
| `busy` | too many calls or clients |
| `forbidden` | the peer is not the owner |
| `core_failed` | the core refused or failed the change |
| `too_large` | the result does not fit in one message |
| `internal` | a daemon bug |

## Command line

```text
omihomo status
omihomo start | stop
omihomo set mode <rule|global|direct>
omihomo set tun|tun-redirect|tailscale|tailnet-proxy <on|off>
omihomo set group <name>
omihomo sub list | add | update <name> | activate <name> | remove <name>
omihomo rule list | add <type> <value> <target> | remove <index>
omihomo group list | select <group> <proxy> | test <group> [proxy...]
omihomo conn list | close <id>... | close --all
omihomo egress
omihomo watch [stream...]
omihomo version
omihomo daemon [--core mihomo|fake] [--home DIR] [--socket PATH] [--owner-file PATH] [--profile FILE]
```

Each command is one call. The result is one JSON line on stdout, indented with `--pretty`; a call
with nothing to say prints nothing. Flags go before a command's arguments (`omihomo set tun
--pretty on`), and `--` ends them, so a name that starts with a dash can still be passed. `-h` on
any command prints its usage and exits 0. `sub add` reads the URL from stdin so its token never appears
in a process list. `watch` subscribes to the named streams, all of them by default, and prints
`{"stream","data"}` lines until interrupted. `OMIHOMO_SOCKET` points the client at another socket.
Errors are one line on stderr.

| Exit | Meaning |
| ---: | --- |
| 0 | success |
| 1 | failure |
| 2 | usage, or a value the daemon rejected as invalid; usage is printed after the error |
| 10 | no daemon answers on the socket |
| 11 | the call conflicts with the current state, or needs the proxy running |
| 12 | not found |
| 13 | this account does not own omihomo |
| 14 | the daemon speaks another protocol version |
| 15 | not implemented yet |

## Running it

`omihomo daemon` logs to stderr, which the journal collects; mihomo logs its warnings and errors
to stdout. Under a `Type=notify` unit it reports `READY=1` once it serves and `STOPPING=1` on
shutdown; `SIGTERM` disconnects every client and stops the core. The daemon runs with umask
`077`, so everything it writes, the core's selection cache included, is root's alone.

Build it the way mihomo's releases are built, with gVisor, which TUN without redirect needs:

```sh
CGO_ENABLED=0 go build -tags with_gvisor -trimpath -o /tmp/omihomo ./cmd/omihomo
```

For development, against the fake core:

```sh
dir=$(mktemp -d) && id -u > "$dir/owner" && chmod 600 "$dir/owner"
/tmp/omihomo daemon --core fake --socket "$dir/sock" --owner-file "$dir/owner" &
OMIHOMO_SOCKET="$dir/sock" /tmp/omihomo start
```

Until the daemon downloads subscriptions itself, `--profile` runs the proxies, groups, rules and
inline rule providers of a Clash YAML file, and logs what it leaves out: proxy providers and
rule providers that are not inline, with the rules that use them. It allowlists nothing, so give
it only a file you trust. TUN needs root:

```sh
sudo install -d -m 711 /run/omihomo-dev
sudo sh -c "id -u $USER > /run/omihomo-dev/owner && chmod 600 /run/omihomo-dev/owner"
sudo /tmp/omihomo daemon --home /run/omihomo-dev/core --socket /run/omihomo-dev/sock \
  --owner-file /run/omihomo-dev/owner --profile ~/subscription.yaml &
export OMIHOMO_SOCKET=/run/omihomo-dev/sock
/tmp/omihomo start && /tmp/omihomo set tun on
```

## The mihomo core

`internal/core/mihomo` is the only package that imports mihomo; a test fails the build when
another one does, or when mihomo's controller (`hub`, `hub/route`) is linked. mihomo keeps its
state in package globals, so a process hosts one core, and `--home` (default
`/var/lib/omihomo/core`) is its directory for the selection cache and geodata.

The core builds mihomo's whole config itself. A profile contributes proxies, groups, rules and
inline rule sets; everything else is fixed by the core:

- no controller, secret or dashboard, and no inbound but `127.0.0.1:7890`, plus
  `127.0.0.1:7899` for tailscaled when the Tailscale integration is on;
- DNS over HTTPS to Cloudflare, through the global group, and `+.ts.net` to Tailscale's
  resolver;
- TUN as the `omihomo` device with auto-route, `any:53` DNS hijack, the daemon's bypass, and
  `tailscale0` excluded; gVisor, or the mixed stack when redirect is on;
- a `GLOBAL` group on the primary group: the one named in settings, else the first `select`
  group, else the first group. Global mode and Omihomo's own Tailscale rules go through it, and
  a profile's own `GLOBAL` is dropped;
- no geodata source, so mihomo never fetches anything as root: GEOIP and GEOSITE rules need their
  files in `--home` already, or the config fails to apply.

`apply` is all or nothing. A config that does not parse changes nothing: mihomo applies a
config's general settings to the running engine while it parses, so the core parses in the
running mode and sets the new one only once the config is accepted, and a value mihomo panics
on fails the apply instead. mihomo applies a config whatever fails on the way and only logs it,
so the core reads its log: if TUN, the local proxy port or the Tailscale port does not come up,
the core stops, puts the previous config back if there was one, and the error carries mihomo's
reason. When the previous config does not come back either, the proxy stays stopped and the
daemon shows it so, with both reasons in `detail`.

Before applying, the core also refuses what mihomo would accept and quietly run differently: a
rule whose value would move its target (only logic and regex rules may have commas in their
value), a rule set with lines mihomo cannot read, and a group interval, timeout or tolerance out
of range, which crashes mihomo's health check.

Stopping applies an empty config: no inbounds, no TUN and no DNS. mihomo cannot cancel a dial in
flight and tracks a connection only once its dial is through, so for the dial timeout after a
stop (5 seconds) the core closes whatever lands; what such a connection sends in the few
milliseconds before that still gets out. Selections persist in `cache.db` across restarts.

mihomo pins `golang.org/x/net` at a release with advisories govulncheck finds reachable; `go.mod`
requires a newer one. It copies mihomo's `replace` of protobuf, which CI checks against mihomo's
on every change, and sets `godebug default=go1.20` to match mihomo's runtime defaults.
Dependabot proposes mihomo bumps.
