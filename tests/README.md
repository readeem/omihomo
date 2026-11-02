# Manual cutover test

The automated tests never run a privileged package transaction, a real TUN proxy or the
installed shell. This checklist covers them. Use a local terminal and finish sessions that
depend on the proxy first: switching it can briefly interrupt connections.

1. Build with `mise run package`. Never publish a locally modified build as a release.
2. Note the old subscription names, active subscription, mode, TUN and primary group. Keep the
   original `~/.local/share/omihomo` for recovery.
3. Run `./tools/test-cutover` yourself. It installs the package, claims and migrates your
   account, backs up the installed plugin, links this checkout and restarts the shell.
4. Compare subscriptions, active selection and typed settings. Test switching subscriptions,
   selecting a config, group latency, egress, rules, and the live connections and traffic views.
   Add a subscription with a private URL on stdin; update it and confirm a failure keeps its
   cached content. Test refreshes while running and stopped.
5. Toggle TUN and acceleration separately; check browsing, DNS, LAN access, and Tailscale
   coexistence if installed. Toggle the Tailscale integration and confirm only Omihomo's
   tailscaled drop-in is added and removed. Check `journalctl -u omihomo.service -b` for errors.
6. Verify the old install is gone:

   ```sh
   stat -c '%a %n' /usr/bin/mihomo
   getcap /usr/bin/mihomo
   systemctl --user is-active omihomo.service omihomo-subscription-update.timer
   systemctl is-active omihomo.service omihomo.socket
   ```

   Old mihomo should be `755` with no capabilities, the old user units inactive or not found,
   and the system units active. The old hooks must be absent from `/usr/share/libalpm/hooks/`,
   and the old `~/.local/bin/omihomo` launcher absent.
7. Restart `omihomo.service`, then reboot, once with the proxy on and once off: it should restore
   its on/off state, settings and cached subscription. An offline boot keeps cached content, and
   refresh retries once connectivity returns.
8. Run `sudo /usr/bin/omihomo claim` again; subscriptions must remain. From a second account the
   socket must be inaccessible, and even root's client request is refused.
9. Remove `omihomo`. Both system units must stop while `/var/lib/omihomo` stays. Reinstall
   and claim to verify retained subscriptions and settings.

For a fresh-install test, use a disposable Omarchy installation: install and claim, check the
widget starts empty, then add a subscription. Do not delete real daemon state to simulate one.

## Direct download warning

Use the real widget against the local fake daemon; this changes no installed state and makes no
external requests:

```sh
tools/screenshots/drive start --visible
tools/screenshots/drive fallback
```

The panel opens a warning at the bottom explaining the IP and destination exposure. Cancel is
selected initially. Press Escape: the update fails without downloading. Run `drive fallback`
again and click **Allow once** (or press `y`): the update completes. Run it again: permission
must be requested again, and Enter must choose Cancel. Tab switches the selected button.
Leaving a prompt unanswered for 45 seconds must cancel it. End the session:

```sh
tools/screenshots/drive stop
```

The daemon tests cover the first-subscription exception, separate approvals for providers,
replayed decisions and address-policy refusals. To test bootstrap on an actual fresh install,
use disposable state as described above: the first subscription and its providers may use
direct recovery without prompting. Later adds and refreshes require approval for each download.

All subscription, provider and geodata HTTPS destinations must resolve to public addresses,
including after redirects. There is no private HTTPS exception, even for the first subscription
or an approved retry. Locally configured DNS servers and proxy endpoints may be private;
these endpoints cannot exempt a downloaded URL from address validation. DNS bootstrap can
contact the configured resolver before the HTTPS fallback is approved.

For CLI-driven downloads, keep the widget open to answer the prompt, or use
`omihomo download list` and `omihomo download allow <id>` / `deny <id>` in another terminal.
Approvals are never saved and pending requests expire if nobody answers.
