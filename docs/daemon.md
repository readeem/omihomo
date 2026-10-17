# Omihomo daemon

`cmd/omihomo` is one Go binary: `omihomo daemon` is the root system service that hosts the proxy
core, and every other subcommand is a client of its socket. This page covers the socket protocol,
who may use it, the command line, and the core. The core interface is `internal/core`; the daemon
hosts mihomo in-process behind it, or the in-memory fake (`--core fake`), which proxies nothing.

## Install on Arch Linux

Install the binary package from the AUR as your ordinary user, then give that account access
to the daemon:

```sh
yay -S omihomo-bin
sudo /usr/bin/omihomo claim
/usr/bin/omihomo status
```

The plugin checkout provides `bin/omihomo-install` to run the first two commands in order. Run
that script without `sudo`; it requests privileges only for claiming the daemon (and when yay
installs the package). If installation fails it does not claim; if claiming fails the script
returns an error. Custom XDG directories are passed to `claim` by the installer.
The widget and command line both use the daemon socket.

The package installs `/usr/bin/omihomo`, `omihomo.service`, `omihomo.socket`, and a tmpfiles rule
for `/var/lib/omihomo`. It enables both units and starts them when systemd is running. Before
claiming, the socket belongs to root and the daemon refuses every client. `claim` records the
non-root account that invoked `sudo` (or `pkexec`) in `/var/lib/omihomo/owner`, writes
`/etc/systemd/system/omihomo.socket.d/owner.conf`, reloads systemd and restarts both units
so the socket reaches the daemon with the correct ownership. Every claim performs that
restart, including a retry after a failed systemd command. Running `sudo /usr/bin/omihomo claim`
from another account transfers access to that account; it does not create shared access.
Running it again from the same account repairs the setup and restarts the daemon.

For the one-time import and cleanup of an old installation, see [upgrading](upgrade.md).

The service runs as root because systemd-resolved authorizes the TUN DNS setup by effective
uid 0. There is no dedicated system user and no sysusers file. The service limits capabilities,
system calls, devices and filesystem access; its state directory is root-owned with mode
`0700`. The only writable system configuration directory is
`/etc/systemd/system/tailscaled.service.d`; Omihomo writes only its fixed proxy drop-in there. See [the service sandbox research](research/2026-10-11-hardened-root-tun-service.md)
for its constraints. The release build disables EasyTier because its JIT conflicts with
`MemoryDenyWriteExecute=yes`.

Upgrades restart an already running daemon. Removing the package disables and stops both
units but preserves `/var/lib/omihomo` and the ownership drop-in, so reinstalling can reuse the
state. Logs are available with `journalctl -u omihomo.service`.

Package publication and maintainer setup are described in [Release workflow](release.md).

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
| `subscriptions.list` | | `{"subscriptions":[Subscription]}` in the order they were added |
| `subscriptions.add` | `{"url"}` | Subscription |
| `subscriptions.update` | `{"name"}` | Subscription |
| `subscriptions.activate` | `{"name"}` | Status |
| `subscriptions.remove` | `{"name"}` | `{}` |
| `rules.add` | `{"type","value","target"}` | `{"rules":[Rule]}` |
| `rules.remove` | `{"index"}`, 1-based among the owner's rules | `{"rules":[Rule]}` |

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
While the proxy is stopped, `rules.list` returns the owner's rules only.
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
| `subscriptions` | `{"subscriptions":[Subscription]}` | on every change, a refresh the daemon ran itself included |
| `connections` | Connections | every second while running |

`status`, `groups`, `rules` and `subscriptions` are state streams. Subscribing to one pushes its current value before the reply,
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
| `fetch_failed` | a download failed: no answer, not HTTPS, too large, or a status other than 200 |
| `rejected` | a subscription's content is not usable |
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
sudo /usr/bin/omihomo claim
omihomo daemon [--core mihomo|fake] [--home DIR] [--state DIR] [--socket PATH] [--owner-file PATH]
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
| 20 | the subscription's content is not usable |
| 21 | a download failed |

## Running it

`omihomo daemon` logs to stderr, which the journal collects; mihomo logs its warnings and errors
to stdout. Under a `Type=notify` unit it reports `READY=1` once it serves and `STOPPING=1` on
shutdown; `SIGTERM` disconnects every client and stops the core. The daemon runs with umask
`077`, so everything it writes, the core's selection cache included, is root's alone.

The production package and release setup are described in [release.md](release.md).
For development, build with gVisor, which TUN without redirect needs, and without EasyTier's
JIT so the binary also runs under the packaged service's memory-execution restrictions:

```sh
CGO_ENABLED=0 go build -tags with_gvisor,no_easytier -trimpath -o /tmp/omihomo ./cmd/omihomo
```

Use [the manual test checklist](../tests/README.md#manual-cutover-test) for the packaged
service. An isolated fake daemon can exercise clients without networking; always provide private
`--state`, `--home`, `--socket` and `--owner-file` paths, track its PID, and stop it afterward.
The widget itself is tested manually in the installed shell.

## Removal

`sudo pacman -R omihomo-bin` disables and stops the system service and socket, preserving state
and the ownership drop-in. Reinstall and claim to reuse subscriptions and settings.
Removing the package also removes Omihomo's Tailscale proxy drop-in and reloads tailscaled.
For permanent removal, stop/uninstall first, then explicitly delete `/var/lib/omihomo` and
`/etc/systemd/system/omihomo.socket.d/owner.conf`. This erases subscription credentials and settings.
Old source data remains separately under the owner's data directory and can be deleted after
verifying migration.

## State

`--state` (default `/var/lib/omihomo`) is the daemon's own directory, `0700` and root's alone,
since subscription URLs carry access tokens. Every file in it is replaced whole and atomically:

```text
owner                    the owner's uid, written by the install step
settings.json            mode, TUN, redirect, Tailscale, tailnet proxy, primary group, the
                         owner's rules, and whether the proxy was on
subscriptions.json       each subscription's name, URL, quota and refresh times, and the active one
subscriptions/<id>.json  what was last downloaded and accepted for a subscription, providers included
core/                    the core's home: selection cache and geodata
```

Settings are typed and each is checked by the daemon; there is no raw YAML, so the owner account
cannot make the root daemon run something the settings do not describe. A change the core would
not run is refused, whether or not the proxy runs, and so is one whose geodata does not
download, since the core cannot check it. The service starts at
boot and turns the proxy back on if it was on, running what was last accepted, and retries every
minute until it comes up or the owner starts or stops it.

The owner's rules come before everything else. Their types are `DOMAIN`, `DOMAIN-SUFFIX`,
`DOMAIN-KEYWORD`, `DOMAIN-WILDCARD`, `GEOSITE`, `IP-CIDR`, `IP-CIDR6`, `SRC-IP-CIDR`, `GEOIP`,
`DST-PORT`, `SRC-PORT`, `PROCESS-NAME`, `PROCESS-PATH` and `NETWORK`; each value is checked for its
type and none may hold a comma. A target must be `DIRECT`, `REJECT`, `REJECT-DROP` or a group or
proxy of the active subscription. A rule whose target another subscription lacks is left out
while that subscription runs.

## Subscriptions

The daemon downloads subscriptions, their providers and geodata itself, with one downloader:
HTTPS only, redirects included, which never send a `Referer`; at most 64 MiB once decoded, for a subscription and all its
providers together; 10 seconds to connect and 60 for the
whole download, 5 minutes for geodata; `clash.meta` as the user agent; no proxy from the
environment. Errors never repeat a URL, which may carry a token, and no list shows one.

A subscription is remote content that configures a root process, so it is read into typed
structs (`internal/profile`) and anything they do not model is dropped:

- Proxies of the types ss, ssr, vmess, vless, trojan, hysteria, hysteria2, tuic, wireguard,
  snell, socks5, http, ssh, anytls, mieru and direct, with their protocol, TLS and transport
  fields. `dialer-proxy`, `interface-name` and `routing-mark` are dropped, and so is any key or
  certificate field that is not inline PEM, since mihomo would read it as a file.
- Groups of the types select, url-test, fallback and load-balance, with their test options.
- Rules of the known types; `SUB-RULE` is dropped, and so is a rule whose target or rule set is.
  Regex rules (`DOMAIN-REGEX`, `PROCESS-NAME-REGEX`, `PROCESS-PATH-REGEX`), alone, nested in a
  logic rule or in a rule set, are dropped too: mihomo matches them on every connection with a
  backtracking engine and no time limit.
- Proxy and rule providers of type `http` with an HTTPS URL, which the daemon downloads, and
  `inline` ones. Proxy providers are expanded into proxies, with their filters and name prefix
  and suffix; rule providers become rule sets, `yaml` or `text` (`mrs` is dropped). `file`
  providers are dropped.
- Nothing else: ports, listeners, LAN access, DNS, TUN, hosts, sniffer, the controller, the
  clock and geodata sources all belong to the daemon.

Numbers a proxy carries are between 0 and 2³¹−1, and port hop intervals at most a day, since
mihomo turns them into timers without checking. Group and provider filters are regexes too; all
of one subscription's filtering gets two seconds, and running out fails it. A group left without
members fails the subscription rather than reroute the rules that use it, and so does an HTTPS
provider that does not download or does not read: without it the rules it feeds, and whatever
they block, would be gone. A client certificate and its key are kept only as a pair that loads.
Share links, plain or base64, and a YAML of bare proxies run in one `Proxy` select group that
`MATCH` sends everything to. A subscription names itself from `profile-title` (base64 when it
says so), else the `content-disposition` filename, else the URL's host, made safe and at most 64
characters; a name already taken gets a numeric suffix. The first subscription added becomes
active.

The daemon refreshes each subscription after the `profile-update-interval` its server sent, in
hours, 12 by default, checking once a minute. A failed refresh never replaces what works: content
is kept only once the core has run it, when it is active and the proxy is on, or else checked
that it would; whatever fails leaves the previous content running and stored. A refresh nothing
answered is retried after a minute, doubling up to the interval, and so is one that fails on this
machine, such as geodata that does not download. One the server refused or whose content the core
refuses holds every request, the owner's too, for a whole interval, or until a longer
`Retry-After` passes. The owner's own refreshes are at least a minute apart, and so are attempts
to add the same URL. Subscription records show the last error and the next refresh.

Geodata the rules need (`GEOIP`, `GEOSITE`, ASN rules, in rule sets too) is downloaded into the
core's home before the core runs them, from MetaCubeX's releases, and again once it is a week
old. A download replaces the file in place only once the core has opened it and built the site
categories the rules use, and `CN`, which mihomo loads on every start, against the rules current
at that moment: mihomo exits on a database it cannot read. Rules match against a new site list
from the next apply. mihomo looks up the IP and ASN databases while it routes and cannot swap
them safely meanwhile, so a new one is read when the proxy next starts. Without the files the
proxy does not start.

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
- no geodata source, so mihomo never fetches anything itself: the core names the files a profile's
  rules need (`Geodata`), and the daemon downloads them into `--home` before an apply. A file
  still missing fails the apply.

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

## Tailscale host integration

Enabling the Tailscale setting opens the fixed loopback proxy on port 7899, then installs only
`/etc/systemd/system/tailscaled.service.d/omihomo.conf` with fixed HTTP/HTTPS proxy and bypass
environment values. Disabling removes that drop-in before closing the listener. The daemon asks
systemd to reload and `try-restart` tailscaled only when the file changes; failures restore the
previous file and settings. Other drop-ins are preserved. Claim restores this setting on startup;
an unclaimed package never changes an old installation's Tailscale environment.

The package owns the empty root-owned directory that the service's `ReadWritePaths` permits.
No client supplies a path, port or environment string. With the integration enabled and the
proxy stopped, tailscaled cannot use its configured proxy until the proxy starts again.
