# Upgrade from the old installation

Use a local terminal after ending sessions that depend on the proxy. Update the plugin, then run:

```sh
yay -S omihomo-bin
sudo /usr/bin/omihomo claim
omarchy restart shell
/usr/bin/omihomo status
```

For custom XDG directories, run `bin/omihomo-install` instead of the first two commands.
Alternatively, pass `--legacy-data DIR` and `--legacy-config DIR` to `claim`.

`claim` imports subscriptions, cached content, metadata, active selection, mode, TUN, redirect,
primary group, Tailscale settings and typed prepend rules, preserving the proxy's on/off state.
Raw append/filter rules and unsupported prepend rules must be converted first; other raw YAML
fields are ignored. It then removes the old user services, privilege hooks and CLI launcher,
and clears privileges on the old mihomo binary. A failed cutover attempts to restore the old runtime.

Original data stays in the old data directory for recovery; the daemon uses `/var/lib/omihomo`.
Existing daemon settings and subscriptions are never overwritten. After checking connectivity
and imported settings, remove `mihomo-bin` or `mihomo` if nothing else needs it. A successful
cutover has no automatic undo; returning to the old version requires reinstalling its integration.
