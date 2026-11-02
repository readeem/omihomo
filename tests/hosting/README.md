# Embedded hosting acceptance

The primary gate runs the real standalone Xray relay against the embedded
adapter and real Manager admission/lifecycle state. Native Xray publisher mode
(`--adapter` omitted) is an optional comparison and is not the production gate.

## Reproduce

Requirements: Linux amd64, Python 3.11+, OpenSSL, and the pinned official
[Xray v26.10.31 release](https://github.com/XTLS/Xray-core/releases/tag/v26.10.31).
The following explicit download installs only a test executable into ignored
`dist/hosting/`; neither the experiment nor the application downloads an engine
at runtime. The archive and executable are pinned independently.

From the repository root:

```sh
python3 - <<'PY'
import hashlib
import io
import os
from pathlib import Path
import urllib.request
import zipfile

os.umask(0o077)
url = 'https://github.com/XTLS/Xray-core/releases/download/v26.10.31/Xray-linux-64.zip'
with urllib.request.urlopen(url, timeout=30) as response:
    archive = response.read(32 * 1024 * 1024 + 1)
assert len(archive) <= 32 * 1024 * 1024, 'archive too large'
assert hashlib.sha256(archive).hexdigest() == 'e4bd9119f50ae7c41658b1abd0679ae06f98bd864a2907ff33f2f2cf7081b214'
with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
    info = bundle.getinfo('xray')
    assert info.file_size == 35151996, 'unexpected executable size'
    binary = bundle.read(info)
assert hashlib.sha256(binary).hexdigest() == 'cc4b84d3069569f297a61f04b914f2bb37e9d202fd7b84cb50def1fc30fd40e1'
directory = Path('dist/hosting')
directory.mkdir(parents=True, exist_ok=True, mode=0o700)
with (directory / 'xray').open('xb') as output:
    output.write(binary)
(directory / 'xray').chmod(0o700)
PY
go build -o dist/hosting/publisher ./tests/hosting/publisher
python3 -u tests/hosting/experiment.py --xray dist/hosting/xray --adapter dist/hosting/publisher
```

If the pinned executable already exists, run only the last command. The
experiment verifies its digest before execution and refuses other builds.
It creates fresh UUIDs, a Reality X25519 keypair, short ID and temporary TLS
certificate. Nothing secret is passed in process arguments or printed.
Generated files live in a temporary `0700` directory under `dist/hosting/`,
with `0600` files, and are removed on success, failure and cancellation.
Raw engine output stays in bounded in-memory queues and is never printed:
Xray's rejection messages can include UUIDs.

The global deadline is 180 seconds, readiness follows engine events and actual
round trips, and every child is terminated and reaped in cleanup. No service
is left running. All listeners bind IPv4 loopback and request ephemeral ports;
Xray's JSON requires releasing its reserved port immediately before start, so
a concurrent port collision fails the run. No root or TUN manipulation is used.

## Reference relay contract exercised

The fixture generates the relay and publisher configurations in Python; there
are no checked-in secrets or static operational profiles.

- Publisher: native VLESS `settings.reverse.tag`, `network: raw`, Reality with
  an expected server name, pinned public key/password and short ID. The relay
  identity's inbound user has its own `reverse.tag`. No ordinary outbound is
  mislabeled as a reverse connection.
- Consumers: two independent VLESS UUIDs on a loopback TLS inbound, verified
  against the generated certificate. The Python clients send real VLESS TCP
  and length-prefixed UDP requests. They are protocol test peers, not a claim
  that a particular third-party TUN client or game has been qualified.
- Publisher has **no listeners**. Manager matches exact virtual IPv4, protocol
  and exposed port before dialing the configured numeric loopback target.
  Sniffing, PROXY headers, DNS routing and ordinary proxy outbounds are absent.
- The fixture deliberately routes **all authenticated consumer destinations**
  into the reverse connection, simulating a relay that ignores destination
  policy. Negative tests therefore exercise the publisher's enforcement.
  An independently configured real relay must additionally restrict consumers
  to the operator-assigned virtual address, require authentication, and reject
  everything else with no public game listener or internet fallback.
- `10.254.198.31` is documentation-only test data, never assigned to an interface.
  It is not a recommended production subnet. An operator must allocate a
  private address after checking host/client LAN, Tailscale, fake-DNS and other
  routing conflicts. Friends must capture and route that address to the same
  authenticated relay before private-address bypass rules.

## Expanded acceptance

The embedded run verifies two TCP consumers; concurrent UDP identity and payload
boundaries; negative authentication/address/port/protocol cases; established TCP
and UDP revocation on disable, remove, target edit, pause and master off;
unaffected TCP preservation across another share's disable and disabled edit;
enable before target start and target restart without relay reload; relay loss
and recovery; TCP and UDP admission budgets; shutdown; Reality verification failure latch,
explicit retry, registration-close backoff and credential replacement. Success is printed only after actual
round trips or observed session closure. Temporary credential values are checked
absent from control acknowledgements.

Fixture control is newline-delimited typed JSON on stdin, bounded to 16 KiB per
line, with unknown and trailing fields rejected. Updates call Manager.Apply
synchronously, with a ten-second deadline. `ack` lines contain a sequence, success
flag and credential-free Manager snapshot; `hosting` lines signal state events.
No control listener is added. The publisher dials via real uplink.Network with
injected loopback link discovery and IPv4/IPv6 source prefixes. This exercises
socket/device binding, but does not itself prove TUN exemption or DNS bootstrap.

## Performance

```sh
python3 -u tests/hosting/experiment.py --xray dist/hosting/xray --adapter dist/hosting/publisher --performance
unshare -Urn sh -eu -c 'ip link set lo up; tc qdisc add dev lo root netem loss 1%; python3 -u tests/hosting/experiment.py --xray dist/hosting/xray --adapter dist/hosting/publisher --performance'
```

The second command changes only its private namespace. Namespace destruction
removes the qdisc. Never run that tc command directly in the host namespace.
The performance mode sends 200 independently verified datagrams from two
consumers, rotating through 1/73/1200/4096/8190-byte payloads while another task
repeatedly exchanges 64 KiB TCP payloads. It reports verified count, failures,
per-size results, median/p95 elapsed time and mean absolute successive RTT delta.
Timing includes TLS/VLESS session establishment for each datagram; these are
transaction measurements, not steady-state game ping. It exits after measurement,
so intentional loss does not invalidate the strict lossless acceptance checks.
Loopback netem affects target UDP as well as the reverse TCP path. Report that
scope with measurements; it does not simulate loss exclusively on a WAN link.

See [compatibility status](../../docs/hosting-compatibility.md) for measured
results, application sessions and scope limits. Echo tests do not qualify games.

## Networking boundary checks

The embedded gate uses a local authoritative DNS fixture for endpoint.example,
changes its A/AAAA answer between real relay restarts, and requires a verified
round trip within 12 seconds. Transport.Run counters in credential-free acks
check no overlapping runs and no restart on unchanged updates. running=false
simulates suspend; it does not exercise actual machine sleep. An 8193-byte UDP
target reply must increase the oversized counter without preventing subsequent
normal datagrams.

```sh
go test -race ./tests/hosting/publisher
python3 tests/hosting/network_namespace.py
```

The first command observes an unanswered DNS query and cancels its uplink dial,
requiring return within one second. The second creates its own `unshare -Urn`
namespace, loopback documentation addresses and a TUN capture sink. An unbound
TCP control must fail under capture policy; the real uplink.Network must reach
the local DNS and TCP server through an explicit device-bound exemption.
All route/rule/link mutations occur only inside that namespace. This tests a
synthetic policy; the additional check below runs actual Mihomo TUN rules.
Native-frame tests exercise queue overflow and inbound oversized frames.

`./tools/test-hosting` automates the pinned test-only download and primary gate.
For additional private network policy and actual Mihomo TUN checks:

```sh
python3 tests/hosting/network_namespace.py
go build -tags with_gvisor,no_easytier -o dist/hosting/tun-check ./tests/hosting/tun
python3 tests/hosting/mihomo_namespace.py
```

The latter creates a separate peer namespace and a real Mihomo TUN, checks that
ordinary connections encounter rejection, and verifies the hosting uplink's
source/device-bound DNS and TCP path in global, direct and rule modes. It does
not alter the host network. SIGSTOP/SIGCONT in the primary fixture tests recovery
from a stopped publisher process while its relay is replaced; no test suspends
the machine. Native-frame tests cover blocked target writes, UDP queue overflow,
oversized relay frames, target replacement and cancellation without bypassing
the real manager's authorization tests.

[Real-game sessions](../../docs/hosting-games.md) cover OpenTTD and Luanti with
actual clients and selected TCP/UDP ports. They complement the transport and
performance checks; none constitutes a universal gaming-ready guarantee.
