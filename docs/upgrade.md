# Upgrade

Use a published release: Omarchy's `release` branch advances only after its matching package
is available. Perform migration in a local terminal after ending sessions that depend on the
proxy; claiming changes the installed proxy and can interrupt connectivity.

## From the Bash CLI and separate mihomo package

Prepare while the old installation still works, before updating the plugin:

- Keep the old data directory as recovery data. Record the active subscription, selected
  members of each group, mode, TUN, acceleration, primary group and Tailscale settings.
- Check that subscription and provider URLs use HTTPS. HTTP subscription URLs block import;
  unsupported providers cannot supply the new runtime.
- Convert raw append/filter rules and unsupported prepend rules to supported typed prepend
  rules. The importer accepts `DOMAIN-SUFFIX`, `DOMAIN-KEYWORD`, `IP-CIDR` and `PROCESS-NAME`,
  without trailing flags. Other raw YAML overrides, including custom listeners and DNS, are
  ignored; the daemon builds those from its fixed security policy.
- Group selections are imported from the old core's cache when the group and member still
  exist in the active subscription. Removed members and automatic groups keep their normal fallback.
  A corrupt or oversized selection cache blocks migration before cutover.

Before replacing the old plugin checkout, use a separate checkout of the published release
to run its preflight as your ordinary account (requires the pinned development tools):

```sh
mise exec -- go run ./cmd/omihomo preflight \
  --legacy-data "${XDG_DATA_HOME:-$HOME/.local/share}/omihomo"
```

The command validates the legacy import in a temporary directory and removes it afterward;
it does not stop services, claim the daemon or replace installed state. Missing provider caches
may require a download. `{"import":true}` means the data can be staged; `false` means no legacy
subscription list was found, so verify the path. Resolve any failure with the still-working old
CLI before proceeding. Keep that old checkout until the live cutover is verified, so pause and
rule repairs remain available even if package installation succeeds before claim fails.

Update the widget, then reload the shell:

```sh
omarchy plugin update dev.readeem.omihomo
omarchy restart shell
```

Open the new widget and choose **Install core**. The old `mihomo-bin` or `mihomo` package is
separate from `omihomo-bin`, so this transition uses Install. The widget resolves its installer
path; no terminal working directory is required. The terminal verifies the signed release,
asks for normal sudo authentication and pacman confirmation, installs the daemon, then claims
and migrates it. The installer forwards custom `XDG_DATA_HOME` and `XDG_CONFIG_HOME` values
and enables automatic settings sync. After installing the package it refuses claim and sync
if the shell and sync unit use different config directories, before defaults can replace imported settings. Align
the two directories first; a relative `XDG_CONFIG_HOME` uses the normal `.config` fallback.

`claim` imports subscriptions, cached content, metadata, request limits, active selection, mode,
TUN, acceleration, primary group, Tailscale settings, selected group members and supported prepend rules, preserving
the proxy's on/off state. Imported refresh intervals use the same one-year maximum and 12-hour
default for missing or nonpositive values as new subscriptions. It then removes the old user services, privilege hooks and CLI launcher,
and clears privileges on the old mihomo binary.

Original data stays in the old directory; the daemon uses `/var/lib/omihomo`. Imported settings
and rules are also written to the owner's `settings.json` and `rules.json`. If either file already
exists, both are preserved and applied instead of the imported settings. Existing daemon settings
and subscriptions are never overwritten by the import.

### If package installation succeeds but claim fails

Fix the reported migration problem first. Install mode now refuses the already-installed
package, and Update does not repeat the import. Rerun claim with the original paths from your
ordinary account's terminal. Before enabling settings sync, ensure the shell and user manager
use the same config directory as checked by the installer:

```sh
sudo /usr/bin/omihomo claim \
  --legacy-data "${XDG_DATA_HOME:-$HOME/.local/share}/omihomo" \
  --legacy-config "${XDG_CONFIG_HOME:-$HOME/.config}"
systemctl --user daemon-reload
systemctl --user enable --now omihomo-apply.path omihomo-apply.service
omarchy restart shell
/usr/bin/omihomo version
/usr/bin/omihomo status
```

If the old installation used other directories, pass those exact directories instead. A failed
pause/rule preflight can require the old CLI: run it from a retained old checkout, or restore
the old plugin version to resolve the issue, then update the widget again before rerunning claim.
A failed cutover attempts to restore the old runtime. A completed migration has no automatic undo:
retain original data and reinstall the old plugin/integration for a full rollback. Do not delete
daemon state to force another import. If a completed import was followed by a settings-sync
failure, fix the owner's settings files and rerun claim; subscriptions are retained.

### Verify before cleanup

Compare subscriptions, active selection, group members, typed settings and rules. Verify imported
choices and the fallback for any removed members. Test subscription refreshes while running and stopped, DNS, browsing, LAN,
TUN with and without acceleration, and Tailscale when present. Confirm removal of the old binary
privileges and user units. Follow the [manual cutover checklist](../tests/README.md#manual-cutover-test)
for reboot restoration while on and off, offline startup, second-account refusal and package removal.

Only after validation, remove `mihomo-bin` or `mihomo` if nothing else uses it, then remove old
data below. Do not run the old uninstaller: it can remove the current Tailscale integration.

## Already using the packaged daemon

Choose **Settings → Update** in the widget. It updates the managed widget through Omarchy first,
then installs a newer signed core package. Linked development checkouts are left alone. For a
terminal update, run `"/path/to/the/installed/plugin/bin/omihomo-install" update`, replacing the
path with the installed checkout's absolute path. Routine updates preserve ownership and state;
they do not require claim.

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
