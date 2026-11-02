# Share a local service through an authenticated relay

Hosting is available under **Settings → Hosting** and `omihomo hosting`. It
requires a separately provisioned compatible relay. The daemon needs no fleet
controller, enrollment service or subscription update to create, edit or
remove a share. A clean install has no relay, no credential and no exposure.

## Provision the relay once

Ask the operator for the relay DNS name or IP, port, expected Reality server
name, public key/password, short ID, publisher UUID and assigned private IPv4
address. The address is a routing identity, not a host interface address.
Check it against the host and friends' LANs, Tailscale, fake-DNS and other
reserved routing ranges; there is no universally collision-free subnet.
Omihomo rejects a virtual address overlapping one of the host's local IPv4
networks. V1 supports one relay and one active raw-TCP/Reality reverse tunnel.

Configure the public fields in Settings → Hosting, or import a JSON relay
profile through stdin or an unprivileged file read:

```sh
omihomo hosting relay set --file relay.json
omihomo hosting credential set --file publisher-credential
```

The credential file must be a regular file owned by the current account with
permissions no wider than `0600`; symlinks are refused. Alternatively pipe
the UUID into `omihomo hosting credential set` through stdin. Never put the
UUID in a command argument or editable settings file. The widget's credential
field does not echo and is cleared after submission. Configuration is validated
before changes; unknown profile keys and imports over 16 KiB are rejected.

The typed relay JSON has these fields; values must come from the operator:

```text
version: 1
label: display label (at most 64 UTF-8 bytes)
host: relay DNS name or numeric IP
port: relay port
address: assigned private IPv4 routing identity
reality:
  server_name: expected server name
  public_key: X25519 public key/password in base64
  short_id: nonempty hexadecimal short ID
```

`credential_ref` is optional; the daemon associates the single protected
credential and returns only its independent identifier and presence. Engine
JSON, executables, paths, listeners, routing scripts and environment fields
are not accepted.

## Select a local service

Make the game listen on `127.0.0.1` or `::1`. A game bound only to a LAN address
must have its bind setting changed first. Create a share with its label,
loopback address, local port, exposed port and TCP/UDP/both. The exposed port
defaults to the local port. Creation **always saves a disabled share**:

```sh
omihomo hosting share add --label example-game --local-port 25565 --protocol tcp
omihomo hosting share enable SHARE_ID
omihomo hosting status --pretty
```

Enabling is the explicit exposure action. A selected share stays selected
when the game stops or restarts; failed connection attempts do not remove it.
No process discovery, automatic publishing or relay reload is involved. Copy
only the displayed private address and exposed port to friends. It never
contains a credential or enrollment link.

Friends need an existing compatible proxy client in TUN/VPN mode. Their
configuration must capture that private address and send it to the same
authenticated relay **before private-address bypass rules**. Other nodes do
not automatically discover this host. Omihomo does not edit friends' clients.

## Control and status

Use Settings → Hosting or these equivalent CLI commands:

```sh
omihomo hosting share edit SHARE_ID --local-port 25566
omihomo hosting share disable SHARE_ID
omihomo hosting share remove SHARE_ID
omihomo hosting pause
omihomo hosting resume
omihomo hosting retry
omihomo hosting credential remove
omihomo hosting relay remove
```

Pause preserves selections and closes all sessions; it survives restart.
The proxy master switch must be on, but host TUN may be off. Turning the
proxy off closes hosting. Internet-proxy modes, group selection and remote
subscription refreshes do not change the relay or broaden shares.

Transport state is idle, connecting, connected, reconnecting or error, with a
sanitized reason, transition time and per-share active count. **Connected
means a live reverse control connection**, not proven game reachability.
Omihomo never probes UDP to decide whether a game is healthy. Confirmed Reality
verification failures stop retries; correct the profile/credential or explicitly
retry. Transport failures use cancellable exponential backoff from one second
up to 30 seconds with jitter. A stream closed before VLESS registration is a
transport failure: VLESS cannot distinguish an unknown publisher UUID from a
relay restart at that point. Verify provisioning with the operator when this
persists; Omihomo never falls back to ordinary proxy credentials.

Disabling, removing or changing a target closes that share's previous TCP
and UDP sessions before success; unaffected shares keep their sessions.
Relay or credential replacement closes the whole old tunnel before starting
the new identity. Credential deletion persists an unprovisioned state, so
restart or subscription imports cannot restore it. State and credentials are
atomic owner-only files, independent of subscriptions.

## Trust and limits

The relay operator controls remote authentication and consumer membership.
Authorized friends and a compromised relay can send arbitrary traffic to
**enabled** services. A vulnerable game is still a vulnerable game: the
allowlist limits destinations, not what its selected server does with input.
No remote message can choose another local destination, reach the LAN,
normal proxy outbounds, filesystem or daemon control socket through hosting.

There are at most 32 shares, 256 simultaneous TCP connections and 256 UDP
associations. Connections have a ten-second establishment timeout. UDP idle
timeout is 60 seconds, refreshed by traffic in either direction. Per-session
queues hold at most 64 datagrams and remain below 1 MiB. Excess sessions are
refused; oversized or excess datagrams are dropped and counted. Supported UDP
payloads are 1–8190 bytes; the pinned upstream consumer writer does not support
empty datagrams. The measured limits and game results are recorded with the
[standalone fixture](../tests/hosting/README.md).

The raw-TCP reverse path carries UDP over a reliable stream and can stall
under packet loss. See the compatibility results before assuming a game will
work. Broadcast/multicast discovery, Ethernet bridging, ICMP, original source
addresses and arbitrary callbacks are outside v1. Only explicitly selected
ports are exposed; Omihomo never opens extra ports to make a game work.

## Operator contract

The reference relay uses pinned Xray v26.10.31 native VLESS reverse. Configure
a dedicated publisher UUID with `reverse.tag` on its VLESS inbound client and
route authenticated consumers' requests for the assigned virtual IPv4 into
that reverse outbound. The publisher uses raw TCP with Reality; configure its
expected server name, X25519 key and short ID together. Keep the first/default
outbound a rejection, allow only intended authenticated consumers, and reject
other destinations. There must be no public unauthenticated game listener or
forwarding fallback to the internet.

The executable fixture generates a complete disposable reference relay with
fresh credentials; its native reverse shape and UDP encoding are authoritative
for the pinned version. Its intentionally permissive consumer destination
routing tests the host against a hostile relay and must be narrowed for real
operation. Configure your own relay independently; no production deployment,
public release or dependency approval is performed by the test.
