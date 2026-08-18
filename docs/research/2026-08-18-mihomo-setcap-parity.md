# Ticket #2: setcap parity with setuid root for TUN

Research date: 2026-08-18

## Verdict

`cap_net_admin,cap_net_raw,cap_net_bind_service=ep` is sufficient for the
current Linux mihomo TUN path when auto-redirect uses its nftables backend and
the process has access to `/dev/net/tun`. It is not unconditional parity with
setuid root:

- If auto-redirect falls back to iptables, mihomo executes the external
  `iptables`/`ip6tables` binary. File capabilities on mihomo do not give that
  separately executed binary the capabilities it needs. The fallback therefore
  fails unless the iptables binaries have their own privilege mechanism or the
  design uses nftables only.
- mihomo also invokes `resolvectl` asynchronously to configure systemd-resolved.
  That is separate from DNS packet hijacking. The command is not covered by
  mihomo's file capabilities; its authorization is determined by the
  systemd-resolved/polkit setup.

For this project, keep the capability approach only if nftables is a required
runtime dependency and the iptables fallback is disabled or explicitly tested.
Otherwise ADR-0002 does not reach the promised root parity and the setuid
fallback remains necessary.

## What the current code does

The mihomo `Meta` branch at commit
[`ac017cdd`](https://github.com/MetaCubeX/mihomo/tree/ac017cdd246ce8bd547653d927e7bf77d7ee73d5)
uses `github.com/metacubex/sing-tun v0.4.22`. It performs the privileged setup
before selecting the packet stack:

1. `sing-tun` opens `/dev/net/tun` and issues `TUNSETIFF`.
2. It configures the link, addresses, routes, and policy rules through netlink.
3. mihomo optionally creates an auto-redirect object and starts nftables or
   iptables rules.
4. Only then does it construct the `gvisor`, `system`, or `mixed` stack.

The relevant mihomo call order is in
[`listener/sing_tun/server.go`](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/listener/sing_tun/server.go#L337-L545).
The stack switch itself is in
[`sing-tun/stack.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/stack.go#L40-L66).

### TUN and auto-route

`sing-tun` opens `/dev/net/tun`, calls `TUNSETIFF`, then uses netlink for
`LinkSetMTU`, `AddrAdd`, `LinkSetUp`, `RouteAdd`, and `RuleAdd`:

- [`tun_linux.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/tun_linux.go#L272-L300)
- [`tun_linux.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/tun_linux.go#L303-L406)
- [`tun_linux.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/tun_linux.go#L921-L943)

The Linux kernel TUN documentation states that `CAP_NET_ADMIN` is required for
creating or connecting to network devices not owned by the user:
[`tuntap.html`](https://www.kernel.org/doc/html/latest/networking/tuntap.html#configuration).
The capabilities manual assigns interface configuration and route-table
modification to `CAP_NET_ADMIN`:
[`capabilities(7)`](https://man.archlinux.org/man/capabilities.7.en#CAP_NET_ADMIN).
No additional capability is indicated by this code path.

### Auto-redirect and DNS hijack

On non-Android Linux, sing-tun tries nftables first and writes the rules through
its nftables netlink library. The rules include TCP redirect and DNS DNAT rules;
the mihomo handler then recognizes destination port 53 and relays the DNS
request:

- backend selection and startup:
  [`redirect_linux.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/redirect_linux.go#L80-L143)
- nftables rules, including DNS hijack:
  [`redirect_nftables_rules.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/redirect_nftables_rules.go#L637-L646),
  [`redirect_nftables_rules.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/redirect_nftables_rules.go#L942-L998)
- mihomo DNS handler:
  [`listener/sing_tun/dns.go`](https://github.com/MetaCubeX/mihomo/blob/ac017cdd246ce8bd547653d927e7bf77d7ee73d5/listener/sing_tun/dns.go#L20-L58)

This is packet interception, not mihomo binding a socket to port 53. The
capability manual assigns firewall/masquerading/accounting and transparent
proxy binding to `CAP_NET_ADMIN`; `CAP_NET_BIND_SERVICE` only permits binding
to a local port below 1024. Therefore `CAP_NET_BIND_SERVICE` is not required
for this TUN DNS-hijack path, though it may be useful if Omihomo later asks
mihomo to bind an actual privileged listener.

The iptables fallback is materially different. It invokes an external binary:
[`redirect_iptables.go`](https://github.com/MetaCubeX/sing-tun/blob/v0.4.22/redirect_iptables.go#L269-L279).
Linux capability transformation on `execve` gives a child only the capabilities
from its own file capability metadata (plus applicable inheritable/ambient
sets); mihomo's file capabilities are not automatically copied to iptables:
[`capabilities(7)`](https://man.archlinux.org/man/capabilities.7.en#Transformation_of_capabilities_during_execve).

### Stack differences

The selected stack does not change the privileged kernel setup. `system` adds
ordinary listeners on ephemeral ports; `gvisor` uses a userspace IP stack; and
`mixed` combines the two. They all consume the same already-created TUN
interface and options. Thus their capability requirements are the same for
TUN, auto-route, auto-redirect, and DNS interception. `CAP_NET_RAW` is relevant
only when mihomo's optional direct ICMP forwarding uses raw/packet sockets; the
capabilities manual lists that operation under `CAP_NET_RAW`.

## Is `+ep` correct?

The libcap text format defines `+` as raising the named capabilities in the
specified sets, and `=+ep` is equivalent to `+ep` for the named capability. A
clear exact command is:

```sh
setcap 'cap_net_admin,cap_net_raw,cap_net_bind_service=ep' /usr/bin/mihomo
```

The AUR package uses two separate `+ep` clauses. That is valid and results in
the listed capabilities being permitted and effective, but it omits
`CAP_NET_RAW`:
[`mihomo-cap.hook`](https://aur.archlinux.org/cgit/aur.git/tree/mihomo-cap.hook?h=mihomo-cap-git).
The AUR package is evidence that the basic mechanism works for its intended
transparent-proxy configuration, not proof of complete parity for every
mihomo feature.

The kernel requires the effective file bit to be enabled for file-permitted
capabilities to become effective after `execve`; `ep` is therefore the right
set for a capability-dumb Go binary that does not call libcap itself:
[`capabilities(7)`](https://man.archlinux.org/man/capabilities.7.en#File_capabilities).

## Pacman upgrades and the hook

File capabilities are stored in the file's `security.capability` extended
attribute. A package upgrade that replaces `/usr/bin/mihomo` must therefore
reapply them to the new inode. Arch's ALPM hook format supports a path trigger
for both `Install` and `Upgrade`, and `PostTransaction` runs after a successful
transaction:
[`alpm-hooks(5)`](https://man.archlinux.org/man/alpm-hooks.5.en#TRIGGERS),
[`alpm-hooks(5)`](https://man.archlinux.org/man/alpm-hooks.5.en#ACTIONS).

The current `mihomo-cap-git` AUR package installs exactly such a hook:

```ini
[Trigger]
Operation = Install
Operation = Upgrade
Type = Path
Target = usr/bin/mihomo

[Action]
When = PostTransaction
Exec = /usr/bin/setcap "cap_net_bind_service=+ep cap_net_admin=+ep" /usr/bin/mihomo
```

Source: [`PKGBUILD`](https://aur.archlinux.org/cgit/aur.git/tree/PKGBUILD?h=mihomo-cap-git),
[`mihomo-cap.hook`](https://aur.archlinux.org/cgit/aur.git/tree/mihomo-cap.hook?h=mihomo-cap-git).
The hook is the right upgrade mechanism, but Omihomo's version should include
the final chosen capability set and should fail visibly if `setcap` fails.

## Live-test status

No mihomo binary is installed on this machine, so an end-to-end test was not
possible without installing software. Creating a TUN interface, changing
routes/rules, or installing nftables rules would also mutate live networking,
which this research intentionally did not do. Read-only checks found
`/dev/net/tun` present and world-readable, the nft/iptables/ip tools installed,
and the current capability bounding set includes `CAP_NET_ADMIN` and
`CAP_NET_RAW`; no system state was changed.

## Recommendation for ADR-0002

Replace the unconditional claim of parity with a conditional one:

1. Require nftables and use the in-process nftables backend for auto-redirect.
2. Grant `CAP_NET_ADMIN` and `CAP_NET_RAW`; add `CAP_NET_BIND_SERVICE` only if
   the product intentionally binds a real listener below port 1024.
3. Install a pacman PostTransaction hook on `usr/bin/mihomo` for both install
   and upgrade, and verify the resulting file capabilities.
4. Treat iptables fallback and systemd-resolved configuration as separate
   privileged integration points. If they must work without additional
   mechanisms, supersede ADR-0002 with the setuid-root fallback.
