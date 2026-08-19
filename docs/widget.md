# Omihomo widget

The plugin's bar widget. Omarchy loads `Panel.qml` as a `bar-widget`; everything else in the
plugin folder is loaded from there.

| File | Role |
| --- | --- |
| `manifest.json` | Plugin id `omihomo`, one bar-widget entry point, one setting |
| `Panel.qml` | Bar button, main popup, connections view, and every row component |
| `Service.qml` | All state: CLI calls, mihomo API calls, polling, and actions |
| `Model.js` | Pure parsing and formatting, tested by `tests/model_test.js` |
| `OmihomoIcon.qml` | The bar mark, drawn natively rather than shipped as an SVG |

## Boundaries

Per [ADR-0001](adr/0001-panel-talks-to-mihomo-directly.md) the widget has two callees:

- **`bin/omihomo`** for anything touching disk or systemd: status, subscriptions, rules, mode,
  TUN, primary group, and core lifecycle. It is invoked by absolute path derived from the
  plugin's own directory, never from `PATH`.
- **mihomo's external controller** for live state: `/proxies`, `/configs`, `/rules`,
  `/connections`, `/traffic`, delay tests, and config selection. The address and secret come
  from `omihomo api-info`, so the panel never parses YAML.

Errors are whatever the CLI put on stderr; the panel shows the message and falls back to the
exit-code table in [cli.md](cli.md) when a command dies without one.

## Panel

The main popup is 420px wide and carries, top to bottom:

1. **Hero** — active subscription, resolved status, and the on/off switch for the core.
2. **Status readout** — the six fields settled in ticket #5: one status indicator with its
   detail on the hero, then `Group › Config`, throughput, uptime, and egress IP with latency.
   Egress is the only field that costs a network call, so it is fetched on panel open, on a
   config or group change, and on a click — never on a timer.
3. **Controls** — mode, TUN, connections, and (when the core is degraded) repair.
4. **Subscriptions** — activate, update, remove, and an inline add form.
5. **Groups** — every group mihomo reports, with the primary one marked. Selecting a group
   browses it.
6. **Configs** — the browsed group's configs in a capped list, with a filter, per-config and
   whole-group latency tests, and best-effort parameters. Only a `Selector` group can be
   chosen from; the rest are read-only because they pick for themselves.
7. **Rules** — your rules, an add form over the four types from ticket #7, and below them the
   subscription's own rules, dimmed and read-only.

Configuration parameters are deliberately thin. `GET /proxies/<name>` states a config's type,
UDP support, liveness, and last delay; it does not expose address, port, or credentials, and
[the provider research](research/2026-08-18-mihomo-proxy-provider-configs.md) ruled out mapping
a runtime name back to its provider entry. The panel renders what mihomo states and nothing else.

The **connections view** is the same popup at 760px: totals, one row per connection with host,
network, chain, rule, process, transfer, and age, and close actions for one or all of them.

When the core is not installed the panel replaces its body with a single install action, which
hands the AUR build to `omarchy-launch-floating-terminal-with-presentation`. Uninstall, the last
row of the panel, takes the same terminal for the same reason: package removal needs sudo. It
arms on the first activation and only runs on the second, because it also deletes the state
directory; Esc or moving the cursor off the row cancels. Repair
stays in-panel because it is one privileged call with no build, and from the panel that call is a
`pkexec` dialog.

## Keys

Navigation is one flat cursor over every visible row, so `j`/`k` walks the whole panel.

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | Move the cursor |
| `h` / `l`, `←` / `→` | Change the value on the row (mode, rule type, rule target) |
| `enter` / `space` | Activate the row |
| `x` | Delete: subscription, rule, or connection |
| `s` | Start or stop the core |
| `t` | Toggle TUN |
| `m` | Cycle mode |
| `c` | Connections view |
| `r` | Refresh |
| `a` | Add subscription |
| `u` | Update the selected subscription |
| `n` | New rule |
| `p` | Make the selected group primary |
| `d` / `D` | Latency test the config / the whole group |
| `/` | Filter configs |
| `R` | Repair capabilities |
| `i` | Install the core (only when it is missing) |
| `esc` | Close the form, filter, or view; otherwise close the panel |

Every one of those is also reachable with the mouse. A focused text field owns `enter` and
`esc` and blocks the panel's key handling until it loses focus.

## Cost

Polling is scoped to what is on screen. The bar only needs `omihomo status`, which runs on the
shared refresh timer (`refreshIntervalSec`, 10s by default). Proxies, rules, subscriptions, and
the `/traffic` stream run only while the panel is open; `/connections` polls at 2s only while
the connections view is open.

## Installing it

```sh
omarchy plugin add https://github.com/readeem/omihomo.git --enable
```

Then place it with `omarchy bar move omihomo`. Installing the mihomo core itself is the panel's
install action, or `omihomo core install` in a terminal.

## Verifying a change

`qmllint` catches syntax and binding mistakes without a compositor:

```sh
mkdir -p /tmp/omihomo-lint/qs
cp -r /usr/share/omarchy/shell/{Commons,Ui} /tmp/omihomo-lint/qs/
/usr/lib/qt6/bin/qmllint -I /tmp/omihomo-lint Panel.qml Service.qml OmihomoIcon.qml
```

Expect warnings in the same categories the first-party panels produce (`unqualified`,
`missing-property` against the bar's `QObject`); a non-zero exit is a real error.

To see the widget run, start a nested Hyprland (`Hyprland -c <minimal config>`), run
`quickshell` inside it against a config root holding `qs/Commons`, `qs/Ui`, and a host
`shell.qml` that loads `Panel.qml` and injects a stand-in `bar`. Point the CLI at fixtures with
`XDG_DATA_HOME`, `OMIHOMO_SYSTEMCTL`, `OMIHOMO_MIHOMO_BIN`, and `OMIHOMO_YQ`, and serve the
controller endpoints from a local stub. That runs the real widget against a fake core without
touching the live bar or the user's mihomo state.
