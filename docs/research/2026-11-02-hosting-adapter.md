# Hosting adapter qualification: blocked

Checked 2026-11-02 against Mihomo v1.19.33 and Xray v26.10.31. The
[standalone experiment](../../tests/hosting/README.md) proves native reverse
TCP/UDP forwarding and local destination enforcement, but rejects the
unmodified Xray helper on retry and session admission behavior. Hosting is
not implemented; no runtime dependency or service sandbox change is selected.

## Adapter evidence

Mihomo's pinned VLESS implementation supports TCP, UDP and Mux commands, not
Xray's reverse publisher command. Ordinary VLESS support cannot implement this
contract. Sources: [command definitions](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/transport/vless/vless.go),
[client framing](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/transport/vless/conn.go),
[inbound dispatch](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/sing_vless/service.go).

Xray's pinned publisher accepts `settings.reverse` on its simplified VLESS
outbound, creates a reverse worker and sends request command `0x04`. The relay
requires a reverse-enabled UUID and registers the corresponding outbound.
This is native VLESS reverse, with the exact JSON exercised by the fixture.
Sources: [publisher configuration](https://github.com/XTLS/Xray-core/blob/v26.10.31/infra/conf/vless.go#L217-L327),
[publisher implementation](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/outbound/outbound.go#L98-L126),
[relay handling](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/inbound/inbound.go#L616-L674).

The relay supplies each destination to the publisher's routing dispatcher.
The fixture matches an exact virtual address, protocol and port before
substituting a fixed loopback target; it defaults to rejection. Xray's freedom
outbound additionally defaults to block-all for `vless-reverse` traffic, with
explicit `finalRules` checked first. Sources:
[reverse dispatch](https://github.com/XTLS/Xray-core/blob/v26.10.31/app/reverse/bridge.go#L198-L234),
[final rules](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/freedom/freedom.go#L156-L186),
[destination substitution](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/freedom/freedom.go#L272-L290).

UDP travels as length-prefixed Mux packets over the reliable reverse TCP
stream. Mux session IDs and optional XUDP GlobalIDs determine reply isolation.
The fixture verifies two simultaneous consumers and 1/73/1200/4096-byte
payloads; it does not establish a maximum safe datagram size, congestion
behavior, or compatibility with any game or existing TUN client. Sources:
[packet reader](https://github.com/XTLS/Xray-core/blob/v26.10.31/common/mux/reader.go#L27-L62),
[session handling](https://github.com/XTLS/Xray-core/blob/v26.10.31/common/mux/server.go#L180-L281).

## Measured blockers

1. **Failed credentials keep retrying.** With a publisher UUID absent from the
   live relay, the fixture observes three distinct authentication rejection
   events about two seconds apart. The publisher uses a fixed two-second
   monitor, without the required one-to-thirty-second jittered backoff or a
   credential rejection latch. VLESS rejects an unknown UUID by closing the
   stream; there is no typed authentication rejection response for a helper
   supervisor to consume. A relay log is an oracle in this test, not a control
   surface available to the installed daemon. Sources:
   [monitor setup](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/outbound/outbound.go#L119-L126),
   [worker creation](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/outbound/outbound.go#L424-L476),
   [UUID rejection](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/encoding/encoding.go).
2. **No reverse admission cap.** All 257 concurrently held TCP connections in
   the experiment reach their loopback target, exceeding the required 256.
   The relay uses an unrestricted Mux client strategy and the publisher adds
   workers as connection load increases. The tested JSON adapter cannot
   enforce the required per-installation TCP/UDP caps and bounded queues.
   Sources: [relay strategy](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/inbound/inbound.go#L645-L674),
   [publisher scaling](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/outbound/outbound.go#L424-L466).

The experiment stops after reproducing these qualification failures. Stopping
the publisher process does close all 257 forwarded TCP sessions. Per-share
revocation, established UDP revocation, reconnect after DNS change, suspend,
TUN exemptions, state migration and full daemon/CLI/widget parity have not
been implemented or qualified.

## Integration and dependency decision

**Decision: do not ship the unmodified Xray helper.** An embedded adapter
needs an independent dependency decision plus tested hooks for bounded session
admission, observable reverse status, authentication failure, retry ownership,
session cancellation and UDP queues. Neither process liveness nor a generated
valid JSON config proves a live reverse connection.

The current [service](../../packaging/omihomo.service) is root-owned and lacks
`CAP_SETUID`/`CAP_SETGID`; it also filters privileged syscalls. A child cannot
simply inherit this privilege and satisfy the helper contract. Changing the
capabilities, syscall filter or service structure requires explicit agreement
under [ADR-0006](../adr/0006-sandboxed-root-service.md). No such change is made.

Relay DNS and connections must use the direct networking boundary,
[`internal/uplink`](../../internal/uplink/uplink.go), which rediscovers uplinks
and binds sockets to their device and source address. Loopback forwarding must
use separate direct sockets. An ordinary Xray helper does not automatically
use that boundary. TUN recapture and IPv4/IPv6 endpoint changes remain
unverified; no subscription, group or `Core.DialContext` routing is proposed.

Dependency assessment for any future Xray integration:

- **Pin and size:** [v26.10.31](https://github.com/XTLS/Xray-core/releases/tag/v26.10.31),
  commit `a4a3dff5b20c2faae567b742c172c7b95e75c083`; official Linux amd64
  archive 16,692,690 bytes, extracted executable 35,151,996 bytes. Archive and
  executable SHA-256 values are pinned in the fixture instructions and test.
  Embedding would need a separate measurement of binary and dependency growth.
- **License:** Xray-core uses [MPL-2.0](https://github.com/XTLS/Xray-core/blob/v26.10.31/LICENSE).
  Omihomo's existing combined binary is GPL-3.0-only under
  [ADR-0003](../adr/0003-embedded-core-and-arch-package.md). Any distribution
  needs a license/notice/source assessment; no conclusion about incompatibility
  is implied by the different licenses.
- **Maintenance:** [recent releases](https://github.com/XTLS/Xray-core/releases)
  are frequent and the reverse implementation is tied to upstream internals.
  Pin upgrades and rerun interoperability tests; the fixture is authoritative
  over moving documentation.
- **Advisories:** [GHSA-5wf9-h793-w73c](https://github.com/XTLS/Xray-core/security/advisories/GHSA-5wf9-h793-w73c)
  concerns certificate pinning for other transports. That does not certify
  VLESS/Reality or the complete transitive dependency graph. Any integration
  must add the engine to dependency scanning and examine reachable findings
  before approval. The experiment adds no Go module or runtime dependency.

Real game compatibility and latency/jitter/loss under controlled impairment
remain unmeasured. Broadcast discovery, original source addresses and
unselected callbacks are outside this qualification. No broader port exposure
or new reverse protocol is proposed as a workaround.
