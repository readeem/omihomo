# Upgrading from the Bash CLI

This is the one-time move from the old Bash CLI and separate mihomo package to the packaged
daemon. Do it in a local terminal after ending sessions that depend on the proxy: claiming
replaces the installed proxy and can interrupt connectivity.

## Prepare

While the old installation still works:

- Record the active subscription, group selections, mode, TUN, acceleration, primary group and
  Tailscale settings, to compare afterwards.
- Make every subscription and provider URL HTTPS; an HTTP subscription blocks the import.
- Convert raw overrides to typed prepend rules: `DOMAIN-SUFFIX`, `DOMAIN-KEYWORD`, `IP-CIDR` or
  `PROCESS-NAME`, without trailing flags. Other raw YAML, such as custom listeners and DNS, is
  ignored.

Optionally, from a separate checkout of the published release, check that the import will pass
without changing anything:

```sh
mise exec -- go run ./cmd/omihomo preflight \
  --legacy-data "${XDG_DATA_HOME:-$HOME/.local/share}/omihomo"
```

`{"import":false}` means no old subscription list was found at that path. Fix any failure with
the old CLI, and keep that checkout until the cutover is verified.

## Cut over

```sh
omarchy plugin update dev.readeem.omihomo
omarchy restart shell
```

Then choose **Install core** in the widget. Claim imports subscriptions, the active selection,
typed settings, group selections and supported rules, keeps the proxy's on/off state, and
removes the old user services, privilege hooks, launcher and the old binary's capabilities. It
writes the imported settings to `settings.json` and `rules.json`, unless either already exists,
in which case the existing files win.

If the package installs but claim fails, fix the reported problem and choose **Install core**
again from the same account with the same XDG paths; it resumes the claim. A failed cutover
restores the old runtime, but a completed one has no undo: the old data directory is the
rollback. Never delete `/var/lib/omihomo` to force another import.

## Clean up

Verify with the [manual cutover test](../tests/README.md), then remove `mihomo-bin` or `mihomo`
if nothing else uses it, and the old data:

```sh
rm -rI -- "${XDG_DATA_HOME:-$HOME/.local/share}/omihomo"
```

Do not run the old uninstaller: it can remove the current Tailscale integration.
