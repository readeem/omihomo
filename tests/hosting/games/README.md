# Real-game qualification

`qualify.py` runs actual upstream clients and servers. It does not implement a
handwritten game peer. See `docs/hosting-games.md` for measured results and
artifact provenance. This fixture imports the existing hosting experiment's
engine lifecycle and command helpers but runs no echo target.

Place compiled OpenTTD 15.3 at `dist/hosting/games/openttd-build/openttd`,
with its language files and OpenGFX2 under the build's `baseset` directory.
Place a run-in-place Luanti 5.18.0 client/server build at
`dist/hosting/games/luanti-5.18.0/bin/`, retaining its bundled DevTest game.
Unpack Xvfb and required shared libraries under
`dist/hosting/games/tools/usr/`. Use the pinned Xray and embedded publisher
from the main hosting experiment. No system installation is necessary.

Run each game in a separate private network and mount namespace:

```sh
unshare -Urnm sh -eu -c 'mount -t tmpfs -o mode=1777 tmpfs /tmp; ip link set lo up; python3 -u tests/hosting/games/qualify.py openttd'
unshare -Urnm sh -eu -c 'mount -t tmpfs -o mode=1777 tmpfs /tmp; ip link set lo up; python3 -u tests/hosting/games/qualify.py luanti'
```

The game connects to a loopback official-Xray destination forwarder, which
requests the virtual address through an authenticated VLESS/TLS consumer.
The official relay then traverses the Reality reverse connection to the
embedded adapter and selected loopback game server. The target game port
and forwarder port differ. Only the selected protocol is shared. This tests
application protocol compatibility; it does not qualify friends' TUN clients.

The fixture uses a private X display and XTest to operate the official game
client. Success requires server evidence of a join and subsequent client
chat. Each run has a 120-second deadline and terminates/reaps every owned
process. A private evidence directory is printed. Raw engine logs are never
printed or retained; temporary UUID configurations and TLS private keys are
deleted even after failure. Game logs remain owner-only in ignored output.
