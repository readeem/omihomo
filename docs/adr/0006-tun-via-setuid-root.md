# TUN via setuid root on the mihomo binary

Supersedes ADR-0002.

Capabilities cover everything mihomo does in its own process, and TUN still asked for a root
password three times per toggle. The prompts come from mihomo, not from us: when the TUN adapter
comes up, `sing-tun` shells out to `resolvectl` three times to point systemd-resolved at the
tunnel, and once more to revert on the way down.

```go
// sing-tun tun_linux.go, setSearchDomainForSystemdResolved
_ = shell.Exec(ctlPath, "domain", t.options.Name, "~.").Run()
_ = shell.Exec(ctlPath, "default-route", t.options.Name, "true").Run()
_ = shell.Exec(ctlPath, append([]string{"dns", t.options.Name}, ...)...).Run()
```

Each is a separate process and a separate polkit action — `set-domains`,
`set-default-route`, `set-dns-servers` — and Arch defaults all three to `auth_admin_keep`, so
polkit's per-action credential cache cannot collapse them into one dialog. File capabilities do
not survive `execve`, so mihomo's `cap_net_admin` never reached `resolvectl`. Neither would
ambient capabilities: systemd 261 calls `sd_bus_query_sender_privilege(call, -1)` in
`bus_verify_polkit_async_full`, which removed the capability bypass.

So the core runs setuid root, which is what Koala Clash ships
(`build/linux/postinst`: `chmod +sx .../sidecar/mihomo`, plus a `pkexec` repair path at
`src/main/core/manager.ts:467`). Unlike Koala, the binary is `4750 root:root` with an ACL entry
granting execute to the one account that installed Omihomo, read from `SUDO_UID` or `PKEXEC_UID`.
`resolvectl` then inherits euid 0, and systemd-resolved — which
runs as `systemd-resolve`, not root — authorizes a root sender without consulting polkit at all
(`sd_bus_query_sender_privilege`: "Sender is root, we are not root"). Zero prompts after install.

## Considered options

A polkit rule granting the four `org.freedesktop.resolve1` actions was the tighter fit for
ADR-0002's design, but it authorizes the user rather than the core: every process you run gets to
reconfigure resolved's DNS. Shimming `resolvectl` on the unit's `PATH` to reach a narrow helper
was tighter still and materially more machinery for one already-solved problem. Koala's mechanism
is proven on this exact core, so it wins on being boring.

## Consequences

The whole proxy runs as root, which is the real cost and the reason ADR-0002 avoided it. Whoever
can execute the core can run it as root with a config of their choosing, so execution is limited
to the owning account: other local accounts cannot run it at all, and a core they could execute
reads as broken and surfaces as a repair action. The owning account, or a process running as it,
can still get root through the core without a password; that is the trust this design accepts.
Since a subscription is remote input to a root process, the merge drops its `external-*`,
`secret` and `ntp` keys and the `path` of its downloading providers: only the override decides how
the core is controlled, a download cannot overwrite Omihomo's own state, and the core never sets
the system clock for a subscription. The core writes its cache and geodata into the state
directory as root from now on; the directory stays user-owned, so uninstall still clears it. The
grant stays revocable with `chmod a-s` and is checked on every status read, so a core that lost
it surfaces as a repair action rather than as a failing toggle. The privileged moments are
unchanged: install, `core repair`, uninstall, and a pacman `PostTransaction` hook that reapplies
`chmod 4750` and the owner's ACL entry after every mihomo upgrade. Grant paths also run `setcap -r` first, because a binary
that carries file capabilities computes its privileges from them instead of from the setuid bit.
