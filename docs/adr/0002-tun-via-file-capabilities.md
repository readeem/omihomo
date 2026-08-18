# TUN via file capabilities on the mihomo binary, not a root service

System-wide transparent proxying needs privileges mihomo does not have as a user process.
Koala Clash solves this with `pkexec bash -c 'chown root:root <core> && chmod +sx <core>'`
(`src/main/core/manager.ts:467`) — setuid root on the whole binary, granted once, revocable
with `chmod a-s`. It avoids a root daemon entirely, which is the good idea worth stealing.

We take the tighter version: grant `cap_net_admin,cap_net_raw,cap_net_bind_service+ep` with
`setcap`, so mihomo gets only what TUN actually needs instead of running the entire proxy as
root. AUR's `mihomo-cap-git` is precedent that this works.

## Considered options

A root-level systemd unit was rejected because it drags polkit rules in with it: the panel
would need a policy file just to start and stop the service. With capabilities the service
stays user-level and control is plain `systemctl --user`.

## Consequences

Installation is the privileged setup moment behind a pkexec prompt. Runtime operation is
unprivileged; `core repair` is an explicit maintenance path that reapplies the same capabilities
after a package upgrade, and uninstall removes them. If setcap turns out not to reach parity with
root for `auto-route` or DNS hijack, the fallback is
koala's setuid approach and nothing else in the design moves.
