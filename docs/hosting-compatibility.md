# Hosting compatibility qualification

The embedded adapter passes the standalone real-engine acceptance gate described
in [the fixture README](../tests/hosting/README.md). Boundary, daemon, native TUN
and real-game checks supplement that gate; each check's scope is recorded below.

## Measured transport

Xray relay 26.10.31, official Linux amd64 executable SHA-256
`cc4b84d3069569f297a61f04b914f2bb37e9d202fd7b84cb50def1fc30fd40e1`.
Publisher: embedded native Reality/VLESS/Mux codecs behind hosting.Manager.
Consumer: Python VLESS test peer over authenticated TLS, not a game client.

Each run performs 200 transactions from two independent authenticated UDP
consumers while another task repeatedly transfers 64 KiB over TCP. Payloads
rotate through 1, 73, 1200, 4096 and 8190 bytes, with distinct consumer markers
and sequence/random content checked in every reply. Timing includes session
establishment; these are short local transaction measurements, not game ping
or a WAN SLA.

| Condition | Verified | Failed | Median ms | p95 ms | Successive RTT delta ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Loopback, no impairment | 200 | 0 | 3.39 | 4.06 | 0.26 |
| Private namespace, 1% loopback netem loss | 196 | 4 | 3.27 | 206.43 | 54.02 |

Each size has 40 attempts. Without impairment, all 40 pass at every size. With
1% loss, verified counts for 1/73/1200/4096/8190 bytes are 40/38/40/39/39;
all received replies retain their exact consumer identity and payload boundary.
The 256-association admission limit and 60-second bidirectional idle expiry
are separate acceptance checks, not inferred from this transaction sample.

Netem applies to every loopback packet, including target UDP. Failures cannot
be attributed solely to the reverse stream. The p95 increase is consistent
with reliable-stream retransmission stalls, but this experiment does not
isolate their cause. The baseline must not be marketed as a low-latency or
universal gaming-ready path.

The supported UDP payload range is **1–8190 bytes**. A deliberate 8192-byte
probe failed in both impairment conditions because the pinned Xray consumer
response writer requires two length bytes inside its 8192-byte buffer
([upstream implementation](https://github.com/XTLS/Xray-core/blob/v26.10.31/proxy/vless/encoding/addons.go)).
Omihomo therefore rejects larger datagrams with an oversized counter rather
than advertising the raw Mux frame capacity as the application limit. Empty
UDP datagrams are not supported by that upstream writer; the adapter counts
and discards them when visible. No datagram is truncated to fit.

## Games

[Real-game qualification](hosting-games.md) records actual OpenTTD 15.3 TCP
and Luanti 5.18.0 DevTest UDP client/server sessions, source and artifact
hashes, selected ports and limitations. Each client joined and sent in-game
chat through the embedded adapter and official Xray. OpenTTD used identical
source builds with the documented revision-metadata limitation. These brief
sessions do not qualify all clients or sustained WAN gameplay.

## Acceptance coverage

| Networking check | Observed result | Scope / remaining limit |
| --- | --- | --- |
| Endpoint DNS A → AAAA → A | PASS, real relay round trips after each change; about 5.0 s recovery with 12 s readiness bound | Injected per-link discovery and local authoritative UDP DNS; no system resolver fallback |
| Suspend / recovery | PASS, lifecycle cancellation and process SIGSTOP/SIGCONT around relay replacement; one publisher after resume | Does not physically suspend the machine or test logind |
| Duplicate publisher runs | PASS, maximum concurrent Transport.Run = 1, exactly one new run per recovery | Fixture counters wrap the real transport; not a daemon process census |
| Oversized UDP target reply | PASS, 8193-byte real target reply is dropped, oversized counter increases, next datagram succeeds | Does not establish relay-to-publisher oversized framing behavior |
| TUN policy interception | PASS, private namespaces: unbound TCP fails under capture; bound uplink DNS/TCP succeed | Both hostile policy rules and actual mihomo/sing-tun TUN tested; actual TUN exercised in global/direct/rule modes against a separate peer namespace |
| Pending admission revocation | PASS, a canceled dial finishing after disable cannot return a live socket; success waits for closure | External dial seam deliberately delays completion; timeout reports failure while retaining revocation |
| Cancel during DNS | Bounded test observes actual unanswered DNS query, cancels, requires error within 1 s | Direct uplink seam; not cancellation during Reality handshake |
| UDP queue overflow | PASS, native-frame test with a deliberately blocked local writer; excess datagrams drop | External socket boundary uses net.Pipe; manager authorization remains real in the standalone gate |
| Relay oversized framing | PASS, native 8193-byte frame is discarded and counted; following frames parse | Native-frame boundary test supplements the real relay's fixed packet-size limit |
| Stalled TCP isolation | PASS, saturated TCP queue closes its session while subsequent tunnel frames remain processable | Blocked local socket; cancellation joins both workers |

Run the policy check with `python3 tests/hosting/network_namespace.py` and the
cancellation check with `go test -race ./tests/hosting/publisher`.
For the actual Mihomo TUN check, build and run:

```sh
go build -tags with_gvisor,no_easytier -o dist/hosting/tun-check ./tests/hosting/tun
python3 tests/hosting/mihomo_namespace.py
```

Only namespace children modify links, routes and rules; namespace destruction
removes them. The policy check includes a failing unbound control connection,
so a route that accidentally bypasses capture cannot pass.

UDP idle expiry is checked at the manager boundary. Persisted credential removal,
pause and master-stop revocation are checked through the real daemon socket
and state store across restart. Fixture acknowledgements, daemon events and CLI
error tests check credential redaction. Widget tests cover non-echo input and
clearing on submission/closure.
Subscription/group/mode changes and packaging are covered outside this fixture.
Target-change tests close old sessions and verify the next connection reaches
a distinct alternate loopback service.

Authentication qualification distinguishes confirmed Reality verification failures
from untyped VLESS registration closes. Reality verification failures latch until
explicit retry or profile/credential replacement. A close without a VLESS response
is a transport failure with backoff: the wire does not distinguish an unknown
publisher UUID from relay restart or interruption at registration. The fixture
uses relay logs only as a test oracle, never as runtime authentication evidence.
