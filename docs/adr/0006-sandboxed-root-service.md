# TUN runs inside a sandboxed root system service

The daemon runs as a root system service because systemd-resolved authorizes the TUN DNS operations by effective uid. Systemd limits capabilities, syscalls, devices and filesystem access. No executable carries setuid or file capabilities, and no pacman hook grants them.

The service keeps networking capabilities and process-inspection privileges. `CAP_SYS_PTRACE` means the filesystem sandbox protects against mistakes, not a compromised daemon escaping through `/proc`; this remains a deliberate trust in the embedded core and its dependencies. Owner-only control and typed, allowlisted input reduce who and what can reach that privilege.

`claim` imports and validates an old install before stopping it, then removes its user units, privilege hooks and CLI symlink and strips privileges from the old binary. A failed operation rolls back and restarts the previously active units. Packaging starts the daemon at boot; it restores the proxy's last on/off state from cached content.

Tailscale integration writes only its fixed drop-in in a package-owned directory allowed by `ReadWritePaths`, then asks systemd to reload and restart tailscaled. Arbitrary system unit paths or environment values are never client inputs.
