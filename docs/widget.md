# Omihomo widget

The plugin's bar widget. Omarchy loads `Panel.qml` as a `bar-widget`; everything else in the
plugin folder is loaded from there.

| File | Role |
| --- | --- |
| `manifest.json` | Plugin id `omihomo`, one bar-widget entry point, one setting |
| `Panel.qml` | Bar button, the popup's header, tabs, five views, and every row component |
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
  from `omihomo api-info`, so the panel never parses YAML. They are cached, and a read the core
  rejects outright — curl's exit 22, which a timeout or a refused connection never produces —
  throws that cache away so the next status read fetches them again. Without that, a rotated
  secret leaves the panel authenticated with the old one while `status` keeps reporting a healthy
  core, and every live section silently renders empty.

Errors are whatever the CLI put on stderr; the panel shows the message and falls back to the
exit-code table in [cli.md](cli.md) when a command dies without one.

Core power, TUN, autostart, and mode keep confirmed and desired values separate. A click updates
the desired value immediately, stale reads continue updating only the confirmed value, and the
desired value disappears once a read agrees. A failed command clears it and rolls the control
back, and so does a successful one that no read confirms within `settleTimeoutMs`, with an error
saying so. This is the same optimistic-state pattern used by Omarchy's Tailscale panel.

Reads that confirm a write replace any copy of themselves already in flight, since that copy began
before the write landed. A config selection stays pending until such a read agrees, and an egress
check waits for it and for the proxy port, so it never measures the route being replaced.

Everything read from the controller belongs to one runtime: the active subscription and the unit's
start time. A controller that stops answering leaves both alone, so the panel keeps its last groups,
configs, connections, and latency results; a stop, restart, or subscription switch drops them.

## Panel

Every view is the same 420px surface at a fixed height, so switching tabs never moves or resizes
the popup. Top to bottom it carries:

1. **Header** — active subscription, resolved status (`ON · rule mode · TUN`), and the on/off
   switch for the core. On the configs and subscriptions views it adds the current rates, a graph of
   the last minute of `/traffic`, and a readout of uptime, egress IP, and latency. Egress is the
   only field that costs a network call, so it is fetched on panel open, on a config, group, or
   subscription change, and on a click on egress or latency — never on a timer. It always goes through the core's mixed
   port. The other views fold the header into one line to leave room for their lists.
2. **Tabs** — Configs, Subs, Rules, and Conns, each with its key and a count, and a gear for the
   settings view. The Subs tab shows `!` while a subscription action has failed.
3. **The view** — one of the five below. Each view scrolls at most one list; its edges fade and its
   scroll bar stays visible while there is more to see.
4. **Key hints** — the keys that matter in this view. Each hint is also a button.

An error is shown next to the thing that caused it — under the subscription that failed to update,
under the rule form, in the settings list — whenever that is on screen, and under the header
otherwise, so it is never out of sight. `Service.errorScope` records where each one belongs.

The **configs view** is the browsed group as chips, the primary one marked with a dot, and its
configs in one list with a latency pill and a test button each. A leading flag or emoji is drawn in a slot
of its own two cells wide, because Qt sometimes lays a colour emoji out narrower once the popup has
reopened, which glued it to the name. The chosen config's row fades
from the accent colour, and a choice still waiting for the core fades more faintly. A pill takes
more of the urgent colour the closer it is to the group's slowest measured delay, none at its
fastest, and all of it on a timeout; the legend under the list says so and holds the whole-group test with how long ago it last ran. Browsing a group that is not the primary one offers a
`make primary` button next to its name. Only a `Selector` group can be chosen
from; the rest pick for themselves and are read-only. An empty view always says why — no active
subscription, a stopped core, an unreachable controller, or a subscription that declares no groups.

Config details stay out of the list on purpose. Direct configs expose their type, UDP support,
liveness, and last delay through `GET /proxies/<name>`, but provider-backed configs are only listed
inside their group's `all` array, so the panel tests them through `GET /group/<name>/delay` and
does not assume a top-level resource exists. Neither endpoint exposes address, port, or
credentials, and [the provider research](research/2026-08-18-mihomo-proxy-provider-configs.md)
ruled out mapping a runtime name back to its provider entry.

The **subscriptions view** is one card per subscription — activate, update, and remove, with its
quota as a bar and its expiry — and an add field that asks only for a URL, since the subscription
names itself. Removing asks for a second confirmation on the card.

The **rules view** opens on a tester: type a domain, a URL, or an IP and the list lights the rule
that would catch it, marks the ones above it as passed, and fades the ones below. It walks the
rules in mihomo's order, your prepended rules first, and judges what a host string can tell:
domain, suffix, keyword, IPv4 CIDR, private GeoIP, and the final match. A rule that depends on
anything else — a DNS answer, a rule set, a geo database, the process — is marked `?`, and the
verdict says how many such rules sat above the hit and marks its target as tentative, because the
core may stop at one of them. An IPv4 address excluded by the active TUN routes is shown as outside
the VPN, using the system route, before any rule is tested. This describes transparent TUN traffic;
an application explicitly configured to use the proxy can still send an excluded address to it. A bar
under the tester shows how the rules divide between targets. Your rules are editable and deletable,
and the add form under the list covers the four types from ticket #7. The rules Omihomo injects for
the Tailscale integration and the tailnet proxy follow under their own heading, read-only, as
`omihomo rule injected` lists them; then the subscription's, read-only. The list keeps the order
mihomo checks it in. A rule's target is DIRECT, REJECT, a group, or any config a group lists: `h`/`l`
cycles through them, and activating the target swaps the list for a filterable picker. `n` opens the
view with the form taking keys from anywhere in the panel.

The **connections view** is the same popup over a log rather than a live list. `GET /connections`
only states what is open right now, so `Service.qml` keeps its own record: every poll is diffed
against the one before it, and an id that has stopped appearing is retained as a closed entry with
the last figures it reported. The view draws open traffic as process → config flows.
Labels use the process path or the API's process name; internal core
connections are labeled `mihomo`. For unidentified TUN traffic, a local socket lookup
fills a name only when the source address is local and exactly one matching socket has an owner
visible to the current account. Ambiguous and forwarded traffic remains unidentified. Each flow
is as thick as the bytes it carried, with the pair under the cursor lit; past six processes or four configs the
smallest fold into `other`. Below it, the log stacks by process, destination, and state — Firefox's
four open sockets to `github.com` are one row reading `×4`, and the ones it has finished with are a
second, dimmed row. A bar behind each row is its bytes on a log scale. Open stacks sort above
closed ones, newest first within each, so a poll only ever updates a row in place or puts a new
one at the top; nothing flashes and nothing disappears from under the cursor.

Open and closed are carried by a leading `●`/`○` and by brightness, not by hue: the panel has a
foreground, a dim, an accent, and an urgent, and no colour to spare for another state. Activating
an open stack closes every connection in it, in one `curl` with several URLs; activating a closed
one drops it from the log, which is the only place it exists. Closed entries are capped at 200,
oldest first, and the whole log is discarded when the core stops.

The **settings view**, behind the gear, holds state that is changed rarely: mode and TUN, TUN
acceleration, autostart, the Tailscale integration, permission repair, and uninstall. Acceleration
is `omihomo set tun-redirect`, which moves `auto-redirect` and the TUN stack together. It is off by
default; turning it off is the repair for a core that reports
`TUN acceleration cannot start on this machine`, and its row says so while that is the reason the
core is degraded. Autostart is `systemctl --user enable` behind `omihomo core autostart`, reported
back by `status`. Repair stays in-panel because it is one privileged call with no build, and from
the panel that call is a `pkexec` dialog. A core whose root permissions are missing, or still open
to other accounts as installs before the owner-only grant left them, turns the gear urgent, puts
an urgent dot on the bar icon, and puts a notice under the header on every other view; clicking it opens settings.
All three clear on the first status read after the repair. Uninstall hands
the removal to `omarchy-launch-floating-terminal-with-presentation`, because package removal needs
sudo. It arms on the first activation and only runs on the second, because it also deletes the
state directory; Esc or moving the cursor off the button cancels.

When the core is not installed the panel replaces its body with a single install action, which
hands the AUR build to the same terminal for the same reason.

## Keys

Navigation is one flat cursor over the current view's rows, so `j`/`k` walks the view top to bottom.

| Key | Action |
| --- | --- |
| `1` – `4` | Configs, Subs, Rules, Conns |
| `M` | Settings, and back |
| `j` / `k`, `↓` / `↑` | Move the cursor |
| `h` / `l`, `←` / `→` | Change the value on the row: group, mode, a switch, rule type, rule target |
| `enter` / `space` | Activate the row |
| `x` | Delete: subscription (asks twice), rule, or connection stack |
| `s` | Start or stop the core |
| `t` | Toggle TUN |
| `m` | Cycle mode |
| `c` | Connections view |
| `n` | Rules view, with the add-rule form taking keys |
| `r` | Refresh |
| `R` | Repair permissions |
| `d` / `D` | Latency test the config / the whole group (configs) |
| `g` | Next group (configs) |
| `p` | Make the browsed group primary (configs) |
| `/` | Filter configs (configs); focus the rule tester (rules) |
| `u` | Update the selected subscription (subs) |
| `a` | Add a subscription (subs) |
| `A` | Close every open connection (conns); toggle TUN acceleration (settings) |
| `L` | Clear the connection log (conns) |
| `b` | Toggle autostart (settings) |
| `T` | Toggle the Tailscale integration (settings, when tailscaled is installed) |
| `P` | Toggle routing the tailnet through the proxy (settings, when tailscaled is installed) |
| `i` | Install the core (only when it is missing) |
| `esc` | Close the field, filter, or confirmation; otherwise go back to configs, then close the panel |

Close-all is `A` rather than the `X` its siblings would suggest, because `PanelKeyCatcher` claims
both cases of `x` for its delete key and an uppercase one never reaches the panel.

Every one of those is also reachable with the mouse. A focused text field owns `enter` and
`esc` and blocks the panel's key handling until it loses focus.

## Cost

Loading the widget runs `omihomo core sync` once, so a runtime left behind by an older plugin
version is replaced as soon as the shell picks the update up.

Polling is scoped to what is on screen. The bar only needs `omihomo status`, which runs on the
shared refresh timer (`refreshIntervalSec`, 10s by default). Proxies, rules, subscriptions, and
the `/traffic` stream run only while the panel is open. Opening the panel reads them once
immediately, from `Service.onPanelOpenChanged` rather than from the panel's own open handler, so
the first open does not sit empty until the next timer tick. A `/traffic` stream that ends while
the panel is open zeroes its rates and reconnects with a backoff from 1s to 30s.

`/connections` is the one read gated by two things: it runs whenever the panel is open, at 2s in
the connections view and on the shared refresh interval elsewhere in the panel, which is enough to
notice that a connection has gone. It does not run with the panel shut, so a connection that opens
and closes while nobody is looking is never logged — the deliberate trade for a bar widget that
costs nothing when it is not on screen.

## Installing it

```sh
omarchy plugin add https://github.com/readeem/omihomo.git --enable
```

Then place it with `omarchy bar move dev.readeem.omihomo`. Installing the mihomo core itself is
the panel's install action, or `omihomo core install` in a terminal.

## Verifying a change

`tests/run` includes Quickshell-native service tests that inject stale status, config, and proxy
reads between a click and its confirmation. They use fake CLIs and a fake `curl`, so they do not
touch the real service or configuration.

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

## Screenshots

The README images are generated, never taken by hand:

```sh
tools/screenshots/capture            # rewrites preview.png and assets/screenshots/
tools/screenshots/capture /tmp/shots # or writes them somewhere else
```

It renders the real `Panel.qml` offscreen in Quickshell at 2× and saves each view, then composes
`preview.png` from the configs, connections, and rules views over the Tokyo Night wallpaper. Each
view is 420×720 plus a one-pixel wallpaper edge; the preview is a 1600×900 desktop at 1.5×.

Everything that could vary is pinned, so a commit gives the same images on the same Omarchy
shell, Qt, font, and wallpaper:

- `tools/screenshots/data/` is the invented subscription, configs, rules, connections, and
  traffic. The fake `omihomo` and `curl` next to it answer the panel from those files, so no
  request leaves the machine and the live core and state are never touched. Use only invented
  names and documentation addresses (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`) there.
- `tools/screenshots/theme/` is the theme and font, read from a throwaway `HOME`. A `hyprctl`
  stub keeps the running desktop's rounding and gaps out.
- Times are relative to the run and sit mid-unit, so uptime, update ages, and connection ages
  read the same.

`KeyboardPanel.qml` there replaces Omarchy's layer-shell window with the same card drawn in place,
because offscreen Quickshell has no layer shell. The images still follow the installed Omarchy
shell's `Commons` and `Ui`, the JetBrains Mono font, and the wallpaper from `omarchy-dev`, so
an Omarchy update can change them.
