# Tests

Run the Go tests and static checks with the release build tags:

```sh
go test -race $(go list ./... | rg -v '/internal/core/mihomo$')
go test -tags with_gvisor,no_easytier ./internal/core/mihomo
go vet -tags with_gvisor,no_easytier ./...
go run golang.org/x/vuln/cmd/govulncheck@latest -tags with_gvisor,no_easytier ./...
./tests/run
```

The embedded core has upstream data races, so its tests run separately without `-race`.
Migration tests use private fixtures and injected systemd operations: normal and raw caches,
provider caches, typed settings, path traversal, symlinks, FIFOs, invalid input, refusal to
overwrite daemon state, and rollback after cleanup or startup failure. No real service is changed.

`tests/run` tests the installer command boundary, the pure Node widget model, and isolated
Quickshell service state against fake daemons on private sockets. It does not render the panel,
contact subscription servers, or touch installed state. Set `OMIHOMO_REQUIRE_QML=1` to require
Quickshell rather than skip its service tests. CI does this.

## Manual cutover test

Use a local terminal. Switching the proxy can briefly interrupt connections, so finish sessions
that depend on it first. The following commands change the system; the package build does not.

1. Build from the checkout with `./tools/build-package`. The result is
   `dist/omihomo-bin-2.0.0-1-x86_64.pkg.tar.zst`, with release archive/checksum and rendered AUR
   metadata beside it. Never publish a locally modified test build as a release.
2. Note the old subscription names, active subscription, mode, TUN and primary group. Keep the
   original `~/.local/share/omihomo` for recovery; do not delete it during testing.
3. Run `./tools/test-cutover` from a local terminal after this agent session. It installs the
   prepared package, claims and migrates your account (including custom XDG paths), preserves the
   installed plugin as a backup, links this checkout into Omarchy, and restarts the shell.
   Pacman asks for confirmation before installation. A failed claim leaves the installed plugin
   in place. The daemon is switched before linking the widget.

   The underlying privileged commands are:

   ```sh
   sudo pacman -U dist/omihomo-bin-2.0.0-1-x86_64.pkg.tar.zst
   sudo /usr/bin/omihomo claim
   ```

4. Compare subscriptions, active selection and typed settings. Test switching subscriptions,
   selecting a config, group latency, egress, rules, and the live connections/traffic views.
   Add a subscription with a private URL on stdin; update it and confirm failures retain its
   working cached content. Test both running and stopped refreshes.
5. Toggle TUN and acceleration separately; check browsing and DNS, LAN access, and Tailscale
   coexistence if installed. Toggle its proxy integration and confirm only Omihomo's tailscaled
   drop-in is added/removed, with other drop-ins intact. Check the journal with `journalctl -u omihomo.service -b` for errors.
6. Verify privilege cleanup and old service removal:

   ```sh
   stat -c '%a %n' /usr/bin/mihomo
   getcap /usr/bin/mihomo
   systemctl --user is-active omihomo.service omihomo-subscription-update.timer
   systemctl is-active omihomo.service omihomo.socket
   /usr/bin/omihomo version
   ```

   Old mihomo should be `755` with no capabilities; old user units inactive/not found, system
   units active. The old permissions/capabilities hooks must be absent from
   `/usr/share/libalpm/hooks/`, and the old `~/.local/bin/omihomo` launcher absent. Keep the
   separate mihomo package until satisfied with testing.
7. Restart `omihomo.service`, then reboot: the proxy should restore its last on/off state,
   settings and cached subscription. Test once with it on and once off. An offline boot must
   preserve cached content; refresh should retry after connectivity returns.
8. Run `sudo /usr/bin/omihomo claim` again; subscriptions must remain unchanged. From a second
   account the socket must be inaccessible; even root's ordinary client request is refused.
9. Remove `omihomo-bin` with pacman. It must stop both system units while keeping
   `/var/lib/omihomo`. Reinstall and claim to verify retained subscriptions and settings.

For a fresh-install test, use a disposable Arch/Omarchy installation with no old state. Install
and claim, verify the widget starts empty, then add a subscription. Do not delete real daemon
state just to simulate a fresh installation.
