# Hosting adapter and dependency decision

Checked 2026-11-02. The selected adapter embeds Xray's native Reality, VLESS
reverse and Mux framing behind `internal/hosting/xray`. Omihomo owns admission,
loopback substitution, session budgets, revocation, status and retry policy.
The [standalone fixture](../../tests/hosting/README.md) verifies interoperability
with the pinned upstream relay; [qualification results](../hosting-compatibility.md)
record transport, networking and actual game checks with their scope limits.

## Why this adapter

Mihomo v1.19.33 supports ordinary VLESS TCP, UDP and Mux commands, without the
reverse publisher command. Sources: [command definitions](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/transport/vless/vless.go),
[inbound dispatch](https://github.com/MetaCubeX/mihomo/blob/v1.19.33/listener/sing_vless/service.go).
Xray v26.10.31 registers a publisher using VLESS command `0x04` and carries
reverse sessions using Mux.Cool. The fixture pins the actual relay configuration
and exercises TCP and concurrent independent UDP consumers. Sources:
[publisher](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/outbound/outbound.go),
[relay](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/inbound/inbound.go),
[framing](https://github.com/XTLS/Xray-core/blob/v26.10.31/common/mux/frame.go).

An unmodified Xray executable retries at fixed intervals and admits more than
256 sessions. These are reasons to own policy in the adapter, rather than use
that executable as the installed publisher. Its privileged child process would
also need a separate privilege boundary. The installed feature starts no helper
and opens no host listener. It does not change the service's capabilities,
syscall filter, filesystem permissions or control socket policy.

Only numeric destinations reaching the exact virtual address, protocol and
exposed port of an enabled Share are admitted. The adapter substitutes the
owner's fixed loopback target. Upstream control messages never enter local
routing. Relay establishment uses `internal/uplink` for DNS and sockets;
local targets use separate direct loopback sockets. Neither uses
`Core.DialContext`, subscription routing or the selected proxy group.

The reverse connection becomes connected only after Reality verification and
the VLESS registration response. A registration close is a transport failure:
VLESS has no typed UUID-rejection response, so it cannot distinguish that close
from relay interruption. Only confirmed Reality certificate/security verification
failures latch as authentication refusal. Invalid UUID syntax is rejected before
saving; a syntactically valid but unprovisioned UUID reports relay unavailability.
Later connection failures use cancellable exponential backoff. UDP is carried
on a reliable TCP stream; retransmission can delay every association. The
verified UDP payload maximum is 8190 bytes, with 64 queued datagrams per session
(at most 512 KiB of payload), 256 TCP/256 UDP admissions and 60-second UDP idle
expiry. A saturated TCP queue closes that session to keep the shared frame reader live.
No claim of game health follows from a live reverse connection.

## Dependency implications

- **Pin:** upstream [v26.10.31](https://github.com/XTLS/Xray-core/releases/tag/v26.10.31),
  commit `a4a3dff5b20c2faae567b742c172c7b95e75c083`, represented by Go module
  `v1.260327.1-0.20261031132019-a4a3dff5b20c`. Internal codec interfaces require
  rerunning the real-engine fixture on every upgrade.
- **Size:** comparable stripped, static Linux amd64 release builds are
  51,265,696 bytes before integration and 53,620,896 bytes with integration:
  approximately 2.25 MiB added. The test-only official Xray executable is
  35,151,996 bytes and is never packaged or downloaded at runtime. The fixture
  pins its archive and executable hashes.
- **Maintenance:** the upstream release history is active, but reverse support
  uses internal engine APIs. Keep the module and reference relay pinned together;
  generated JSON validation alone is not an upgrade gate.
- **License and source:** Xray's [MPL-2.0 license](https://github.com/XTLS/Xray-core/blob/v26.10.31/LICENSE)
  permits distribution subject to its notice and source obligations. The project
  retains its existing GPL-3.0 distribution policy. Packaging collects vendored
  notices into `THIRD_PARTY_LICENSES` and publishes the vendored source archive;
  no upstream source files are patched. This adds another maintained dependency
  to the privileged daemon's trusted code, despite introducing no new privilege.
- **Vulnerabilities:** [GHSA-5wf9-h793-w73c](https://github.com/XTLS/Xray-core/security/advisories/GHSA-5wf9-h793-w73c)
  concerns certificate pinning in other transports and was patched in 26.7.11.
  The integrated release-build `govulncheck` reports zero reachable findings;
  four findings exist in required modules without imported vulnerable packages.
  The existing CI scan covers the new adapter and transitive dependencies.

The embedded code, transport dependencies and changed binary remain subject to
manual security review before release or deployment. This implementation does
not authorize either operation. Game compatibility and network qualification
must be read with their recorded limitations; unsupported discovery, original
source-IP preservation and unselected callbacks do not justify broader shares.
