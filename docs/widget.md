# Omihomo widget

The plugin's bar widget. Omarchy loads `Panel.qml` as a `bar-widget`; everything else in the
plugin folder is loaded from there.

| File | Role |
| --- | --- |
| `manifest.json` | Plugin id `omihomo`, one bar-widget entry point, one setting |
| `Panel.qml` | Bar button, main popup, connections, rules and manage views, and every row component |
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

Core power, TUN, autostart, and mode keep confirmed and desired values separate. A click updates
the desired value immediately, stale reads continue updating only the confirmed value, and the
desired value disappears once a read agrees. A failed command clears it and rolls the control
back. This is the same optimistic-state pattern used by Omarchy's Tailscale panel.

## Panel

Every view is 420px wide. The main popup carries, top to bottom:

1. **Hero** — active subscription, resolved status, and the on/off switch for the core.
2. **Status readout** — the six fields settled in ticket #5: one status indicator with its
   detail on the hero, then `Group › Config`, throughput, uptime, and egress IP with latency.
   Egress is the only field that costs a network call, so it is fetched on panel open, on a
   config or group change, and on a click — never on a timer.
3. **Configs** — the browsed group's configs in a capped list, with a filter, per-config and
   whole-group latency tests, and best-effort parameters. Only a `Selector` group can be
   chosen from; the rest are read-only because they pick for themselves. Picking a config in
   the primary group is the one thing done every session, so it sits directly under the readout.
4. **Groups** — subscription groups, with the primary one marked. Mihomo's `GLOBAL` system
   group stays hidden because Omihomo generates and manages it. Selecting a group browses it,
   which is what the config list above shows.
5. **Subscriptions** — activate, update, remove, and an inline add form that asks only for a
   URL, since the subscription names itself. With no subscriptions the URL field is the section;
   once there is one, it hides behind an add row.
6. **Controls** — one line pinned to the bottom of the popup: mode and TUN on the left, because
   they are state changed in place, and rules, connections, and manage on the right, because they
   open a view. Each cell is its own cursor target and carries its key in a tooltip. Everything
   above it scrolls; the footer does not, so the controls are reachable from anywhere in a long
   panel.

Configuration parameters are deliberately thin. `GET /proxies/<name>` states a config's type,
UDP support, liveness, and last delay; it does not expose address, port, or credentials, and
[the provider research](research/2026-08-18-mihomo-proxy-provider-configs.md) ruled out mapping
a runtime name back to its provider entry. The panel renders what mihomo states and nothing else.

The **connections view** is the same popup, over a log rather than a live list. `GET /connections`
only states what is open right now, so `Service.qml` keeps its own record: every poll is diffed
against the one before it, and an id that has stopped appearing is retained as a closed entry with
the last figures it reported. The view then stacks that log by process, destination, and state —
Firefox's four open sockets to `github.com` are one row reading `×4`, and the ones it has finished
with are a second, dimmed row. Open stacks sort above closed ones, newest first within each, so a
poll only ever updates a row in place or puts a new one at the top; nothing flashes and nothing
disappears from under the cursor.

Open and closed are carried by a leading `●`/`○` and by brightness, not by hue: the panel has a
foreground, a dim, and an urgent, and no colour to spare for a third state. Activating an open
stack closes every connection in it, in one `curl` with several URLs; activating a closed one drops
it from the log, which is the only place it exists. Closed entries are capped at 200, oldest first,
and the whole log is discarded when the core stops.

The **rules view** holds what used to be the panel's last two sections: your rules with an add
form over the four types from ticket #7, and below them the subscription's own rules, dimmed and
read-only. It is a view rather than a section because a subscription ships hundreds of rules and
none of them are read in a normal session. `n` still means "new rule": from the main panel it
opens the view with the form already up.

The **manage view** holds the three
operations that outlive a session: autostart, permission repair, and uninstall. Autostart is
`systemctl --user enable` behind `omihomo core autostart`, reported back by `status`. Repair
stays in-panel because it is one privileged call with no build, and from the panel that call is a
`pkexec` dialog; a degraded core is surfaced on the manage cell in the controls rather than by
growing them. Uninstall hands the removal to
`omarchy-launch-floating-terminal-with-presentation`, because package removal needs sudo. It arms
on the first activation and only runs on the second, because it also deletes the state directory;
Esc or moving the cursor off the row cancels.

When the core is not installed the panel replaces its body with a single install action, which
hands the AUR build to the same terminal for the same reason.

## Keys

Navigation is one flat cursor over every visible row, so `j`/`k` walks the whole panel.

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | Move the cursor |
| `h` / `l`, `←` / `→` | Change the value on the row (mode, rule type, rule target) |
| `enter` / `space` | Activate the row |
| `x` | Delete: subscription, connection stack, or rule (rules view) |
| `s` | Start or stop the core |
| `t` | Toggle TUN |
| `m` | Cycle mode |
| `c` | Connections view |
| `n` | Rules view, with the add-rule form open |
| `M` | Manage view |
| `r` | Refresh |
| `a` | Add subscription |
| `u` | Update the selected subscription |
| `p` | Make the selected group primary |
| `d` / `D` | Latency test the config / the whole group |
| `/` | Filter configs |
| `A` | Close every open connection (connections view) |
| `L` | Clear the connection log (connections view) |
| `R` | Repair permissions |
| `b` | Toggle autostart (manage view) |
| `i` | Install the core (only when it is missing) |
| `esc` | Close the form, filter, or view; otherwise close the panel |

In the connections view `c` goes back and in the manage view `M` does; the rules view leaves
on Esc, since `n` opens its add form. `n` inside the rules view adds a rule. Close-all is `A`
rather than the `X` its siblings would suggest, because `PanelKeyCatcher` claims both cases of
`x` for its delete key and an uppercase one never reaches the panel.

Every one of those is also reachable with the mouse. A focused text field owns `enter` and
`esc` and blocks the panel's key handling until it loses focus.

## Cost

Polling is scoped to what is on screen. The bar only needs `omihomo status`, which runs on the
shared refresh timer (`refreshIntervalSec`, 10s by default). Proxies, rules, subscriptions, and
the `/traffic` stream run only while the panel is open. Opening the panel reads them once
immediately, from `Service.onPanelOpenChanged` rather than from the panel's own open handler, so
the first open does not sit empty until the next timer tick.

`/connections` is the one read gated by two things: it runs whenever the panel is open, at 2s in
the connections view and on the shared refresh interval elsewhere in the panel, which is enough to
notice that a connection has gone. It does not run with the panel shut, so a connection that opens
and closes while nobody is looking is never logged — the deliberate trade for a bar widget that
costs nothing when it is not on screen.

## Installing it

```sh
omarchy plugin add https://github.com/readeem/omihomo.git --enable
```

Then place it with `omarchy bar move omihomo`. Installing the mihomo core itself is the panel's
install action, or `omihomo core install` in a terminal.

## Verifying a change

`tests/run` includes a Quickshell-native service test that injects stale status and config reads
between a click and its confirmation. It uses `/usr/bin/true` and `/usr/bin/false` as fake CLIs,
so it does not touch the real service or configuration.

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
