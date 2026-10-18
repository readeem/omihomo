# Upgrade from the old installation

Use a local terminal after ending sessions that depend on the proxy. Update the plugin, then run:

```sh
bin/omihomo-install install
omarchy restart shell
/usr/bin/omihomo status
```

The installer preserves custom XDG directories when claiming and importing the old data.
If the packaged daemon is already installed, use `bin/omihomo-install update` instead;
run `sudo /usr/bin/omihomo claim` separately only when importing an old installation or
claiming the daemon for this account. Custom migration paths can be passed as
`--legacy-data DIR` and `--legacy-config DIR` to `claim`.

`claim` imports subscriptions, cached content, metadata, active selection, mode, TUN, redirect,
primary group, Tailscale settings and typed prepend rules, preserving the proxy's on/off state.
Raw append/filter rules and unsupported prepend rules must be converted first; other raw YAML
fields are ignored. It then removes the old user services, privilege hooks and CLI launcher,
and clears privileges on the old mihomo binary. A failed cutover attempts to restore the old runtime.

Original data stays in the old data directory for recovery; the daemon uses `/var/lib/omihomo`.
Imported settings and rules are also written to the owner's `settings.json` and `rules.json`.
If either file already exists, both are preserved and applied instead of the imported settings.
Existing daemon settings and subscriptions are never overwritten by the import.
Enable automatic applies after the claim:

```sh
systemctl --user daemon-reload
systemctl --user enable --now omihomo-apply.path omihomo-apply.service
```
 After checking connectivity
and imported settings, remove `mihomo-bin` or `mihomo` if nothing else needs it. A successful
cutover has no automatic undo; returning to the old version requires reinstalling its integration.

## Remove the old data

After verifying the imported settings and connectivity, run as your ordinary user:

```sh
rm -rI -- "${XDG_DATA_HOME:-$HOME/.local/share}/omihomo"
```

If you passed `--legacy-data`, use that exact directory instead. This deletes the old
subscriptions, credentials, overrides, runtime and caches. The new daemon never reads them.
Keep `/var/lib/omihomo` and the current systemd units and drop-ins; no service restart is needed.
Do not run the old version's uninstaller, which can remove the current Tailscale integration.

If `tools/test-cutover` retained a previous plugin, remove only the backup directory printed
by the script (`.omihomo-before-cutover.XXXXXX`, including its `plugin` child) with `rm -rI --`.
Keep the active `dev.readeem.omihomo` plugin and the checkout it points to.
