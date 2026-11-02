# Native VLESS reverse experiment

This is the first adapter qualification gate for hosting, **not a shipped
hosting feature**. It demonstrates TCP and concurrent UDP interoperability,
then reproduces two blockers in the unmodified Xray helper. Exit status zero
means the experiment completed; `RESULT` explicitly reports that the helper
fails qualification. It does not claim the complete hosting acceptance suite.

No daemon, CLI, widget, package dependency, system service or installed setting
is changed. There is no exposure on a clean install. The experiment needs no
external relay, production credentials, container runtime or control service.

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
python3 -u tests/hosting/experiment.py --xray dist/hosting/xray
```

If the pinned executable already exists, run only the last command. The
experiment verifies its digest before execution and refuses other builds.
It creates fresh UUIDs, a Reality X25519 keypair, short ID and temporary TLS
certificate. Nothing secret is passed in process arguments or printed.
Generated files live in a temporary `0700` directory under `dist/hosting/`,
with `0600` files, and are removed on success, failure and cancellation.
Raw engine output stays in bounded in-memory queues and is never printed:
Xray's rejection messages can include UUIDs.

The global deadline is 60 seconds, readiness follows engine events and actual
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
- Publisher has **no listeners**. Exact virtual IPv4 `/32`, network and exposed
  port rules select fixed numeric loopback redirects. Every other route is
  blackholed; each redirect also has an exact final destination allow rule.
  Sniffing, PROXY headers, DNS routing and ordinary proxy outbounds are absent.
- The fixture deliberately routes **all authenticated consumer destinations**
  into the reverse connection, simulating a relay that ignores destination
  policy. Negative tests therefore exercise the publisher's enforcement.
  An independently configured real relay must additionally restrict consumers
  to the operator-assigned virtual address, require authentication, and reject
  everything else with no public game listener or internet fallback.
- `192.0.2.77` is documentation-only test data, never assigned to an interface.
  It is not a recommended production subnet. An operator must allocate a
  private address after checking host/client LAN, Tailscale, fake-DNS and other
  routing conflicts. Friends must capture and route that address to the same
  authenticated relay before private-address bypass rules.

## Observed result

On Xray 26.10.31 (`a4a3dff`, Go 1.27.3, linux/amd64):

```text
PASS: Reality native reverse; two authenticated TCP consumers
PASS: 128 concurrent UDP datagrams, 1/73/1200/4096 bytes, independent reply paths
PASS: unauthorized UUID, wrong address/port/protocol, loopback/LAN/domain destinations denied
BLOCKER: unmodified publisher admits 257 active TCP sessions, exceeding the 256 budget
PASS: stopping publisher closes all 257 forwarded TCP sessions
BLOCKER: rejected publisher credential automatically retried; intervals (s): [1.99, 2.0]
RESULT: interoperability demonstrated; unmodified helper fails retry and session-budget contracts
```

UDP peers use the same exposed port concurrently, with distinct loopback
associations and byte-for-byte replies. The payload lengths above are tested
sizes, **not a verified maximum** or a game compatibility guarantee. Timing
varies slightly between runs. The retry test matches relay rejection events
to each publisher attempt, so duplicate log lines do not count as retries.

## Stop decision

Mihomo has no native VLESS reverse publisher. Xray's unmodified executable is
rejected as the production adapter: no enforceable reverse-session budget and
no authentication rejection stop or cancellable jittered backoff. Killing and
restarting a helper on an opaque connection close would not distinguish a
rejected credential from a transient network failure.

An embedded Xray adapter may be a future option, but has not passed this gate.
It needs a separate dependency decision and verified hooks for admission,
credential failure, live reverse status, cancellation, bounded UDP queues,
revocation, and direct dialing through the networking boundary. An unprivileged
helper would additionally need a privilege model compatible with the service
sandbox. See the [source and dependency assessment](../../docs/research/2026-11-02-hosting-adapter.md).

Until an adapter passes these requirements, daemon state and credential
import, CLI/widget controls, runtime packaging, a real operator setup guide,
the full lifecycle/TUN/DNS acceptance suite, controlled-loss measurements and
TCP/UDP game compatibility tests remain blocked. No gaming suitability is
claimed and no hosting dependency or sandbox relaxation is approved here.
