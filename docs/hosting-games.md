# Real-game qualification

Qualification requires an official client joining its matching official server
through the embedded hosting adapter and official Xray. A successful build,
echo exchange, server query or handwritten game handshake does not qualify.
The fixture lives in [tests/hosting/games](../tests/hosting/games/README.md).

## Artifact provenance

Upstream stable releases checked on 2026-11-02 using the official GitHub
`releases/latest` API (excluding prereleases):

- [OpenTTD 15.3](https://github.com/OpenTTD/OpenTTD/releases/tag/15.3), released
  2026-04-04. The generic Linux archive on the official CDN returned HTTP 403.
  The official [tag source archive](https://codeload.github.com/OpenTTD/OpenTTD/tar.gz/refs/tags/15.3)
  downloaded successfully: SHA-256
  `c8c54e3619d9ee347ac869b632500af4f6740b709a6a3a7cff508ca7d5cb6fb7`.
- [Luanti 5.18.0](https://github.com/luanti-org/luanti/releases/tag/5.18.0),
  released 2026-10-29. Its release assets include no native Linux executable.
  The official [tag source archive](https://codeload.github.com/luanti-org/luanti/tar.gz/refs/tags/5.18.0)
  downloaded successfully: SHA-256
  `384f1eafeb2bbb769eeb5b20cea35396daccd04b13df22344573100f3e8d3ccd`.
  DevTest is bundled in this exact source archive.
- [OpenGFX2 Classic 0.8.1](https://github.com/OpenTTD/OpenGFX2/releases/tag/0.8.1),
  official release asset `OpenGFX2_Classic-0.8.1.tar`: SHA-256
  `2f4d0d8cd82b9b15d7c5a742581bfd11a08d4fd54fc0743f1f8a1032e3b5eeaf`.
  Its internal directory name includes the upstream build identifier
  `20251215-main-g013cbc9e70`.

Source and tool archives, extracted tools and build products are confined to
ignored `dist/hosting/games/`. CMake 4.4.4 and Xvfb 21.1.25-2 were unpacked
from distribution packages without installation. Game compilation uses the
existing C++ compiler and system development libraries; no system packages,
configuration or host routes are changed.

Build commands after extraction:

```sh
LD_LIBRARY_PATH="$PWD/dist/hosting/games/tools/usr/lib" dist/hosting/games/tools/usr/bin/cmake -S dist/hosting/games/OpenTTD-15.3 -B dist/hosting/games/openttd-build -DCMAKE_BUILD_TYPE=Release -DOPTION_USE_ASSERTS=OFF
LD_LIBRARY_PATH="$PWD/dist/hosting/games/tools/usr/lib" dist/hosting/games/tools/usr/bin/cmake --build dist/hosting/games/openttd-build -j 6
LD_LIBRARY_PATH="$PWD/dist/hosting/games/tools/usr/lib" dist/hosting/games/tools/usr/bin/cmake -S dist/hosting/games/luanti-5.18.0 -B dist/hosting/games/luanti-build -DCMAKE_BUILD_TYPE=Release -DRUN_IN_PLACE=TRUE -DBUILD_SERVER=TRUE
LD_LIBRARY_PATH="$PWD/dist/hosting/games/tools/usr/lib" dist/hosting/games/tools/usr/bin/cmake --build dist/hosting/games/luanti-build -j 6
```

## Scope

Xray is the independently pinned official 26.10.31 binary from the main
fixture. The publisher is the embedded adapter fixture, not an Xray publisher
process. The consumer is official Xray with a loopback destination forwarder;
this does not verify a particular friends' TUN implementation or bypass rules.
The transport remains raw TCP with Reality, including UDP carried over its
reliable reverse stream. Latency, loss and jitter measurements belong to the
separate networking qualification; a successful game join does not establish
acceptable WAN latency or sustained play under loss.

No broadcast discovery, original client source addresses or callback ports are
qualified. No automatic port expansion is permitted.

## Verified sessions

Both cases passed on 2026-11-02 in private user/network/mount namespaces with
loopback up and a private tmpfs `/tmp`. SDL was explicitly forced to X11 on
Xvfb; the inherited Wayland display was removed. The actual upstream game
clients were operated using XTest, without modifying game code or implementing
any game handshake in the fixture.

| Game client and server | Selected share | Observed result | Extra forwarded ports |
| --- | --- | --- | --- |
| OpenTTD 15.3 tag source build, identical binary on both ends; OpenGFX2 Classic 0.8.1 | TCP 3979 | Client joined, started company #1, server received `fixtureplayed` in game chat | None; UDP discovery was not shared |
| Luanti 5.18.0, bundled DevTest, identical binary on both ends | UDP 30000 | Client received server/game messages, joined world, server received `fixtureplayed` in game chat | None |

OpenTTD's GitHub tag archive lacks release revision metadata: this exact
unmodified source build reports `OpenTTD norev0000` and network revision
`norev0000-uunknown`. The 15.3 identity comes from the pinned official tag
archive, not from that display string. This verifies matching locally built
client/server binaries, not interoperability with a separately packaged 15.3
binary that uses a different network revision identifier.

Locally built executable SHA-256 values:

- OpenTTD client/server:
  `8adb5e34e70e41f87e2608739ab729344086bb97f80f1a4ac91a6f8663ae3beb`.
- Luanti client/server:
  `360312ab35a544f4268a19aa77daa3b235f403f82285fc159544ed992a05b349`.
- Embedded publisher fixture used for these sessions:
  `ced3063bdf41e8ca0574caa3dc17218a3f429b79af5f4bd9a79cc0070edd93b2`.

The Luanti server recorded `joins game` followed by
`CHAT: <FixturePlayer> fixtureplayed`. The OpenTTD server recorded
`joined as FixturePlayer`, `started a new company (#1)`, then
`[All] FixturePlayer: fixtureplayed`. Every child was terminated and reaped.
No host route or system configuration was changed, and no game service was
made public.

These are short direct-connect application compatibility sessions with one
player. They do not qualify long gameplay, concurrent players, loss tolerance,
UDP timeout/association budgets or sustained TCP contention. Section 9's full
performance gate must combine these results with the networking measurements;
these sessions alone do not justify a general gaming-ready claim.
