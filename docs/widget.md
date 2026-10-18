# Omihomo widget

The plugin's bar widget, in `widget/`. Omarchy loads `widget/Panel.qml` as a `bar-widget`; everything
else in that folder is loaded from there.

| File | Role |
| --- | --- |
| `manifest.json` | Plugin id `dev.readeem.omihomo`, one bar-widget entry point |
| `widget/Panel.qml` | Bar button, the popup's header, tabs, and key hints; the panel state and the cursor and key handling for every view |
| `widget/views/` | One file per view (`ConfigsView`, `SubscriptionsView`, `RulesView`, `ConnectionsView`, `SettingsView`) and the rows only that view uses |
| `widget/components/` | Shared pieces: `Theme`, text, buttons, fields, the switch, tabs, `TrafficGraph`, `FlowDiagram`, and `NavRow`, the base of every cursor row |
| `widget/Service.qml` | The connection to the daemon, its streams, and actions |
| `widget/UpdateService.qml` | Cached release checks through the installer, independent of the daemon |
| `widget/Model.js` | Pure reading of daemon messages and formatting, tested by `tests/model_test.js` |
| `widget/OmihomoIcon.qml` | The bar mark, drawn natively rather than shipped as an SVG |

Views and rows take the panel as `panel`, and read its state, its `service`, and its `theme` from
it; they never reach the panel's ids by name, and neither do `NavRow` and `ViewTab`, which take the
panel too. The other shared components take only the `theme`. A view exposes the fields and lists
the panel's cursor needs to focus and scroll, so the cursor and the keys stay in `Panel.qml`.

## Boundaries

The proxy controls talk to the daemon's socket, `/run/omihomo/omihomo.sock`, or `OMIHOMO_SOCKET`
when it is set (see [daemon.md](daemon.md)). `widget/Service.qml` holds one `Socket` to it,
sends requests with an id and matches each reply to its call, and applies the events the daemon
pushes. It runs no processes and polls nothing.

The `status` stream is always subscribed, since the bar icon needs it. `groups`, `rules`,
`subscriptions`, `traffic`, and `connections` are subscribed while the panel is open and dropped
when it closes. Subscribing pushes each state stream's current value at once, and later pushes
arrive whoever made the change, so the panel stays current when the CLI changes something.

Before anything else the daemon greets the panel, and the panel speaks only to a daemon that
greets it with its own protocol version. Otherwise it replaces its body with one card saying
why, and the command that fixes it, run in a terminal since it asks for a password:

| Situation | Card | Command |
| --- | --- | --- |
| No daemon answers on the socket | Install core | `bin/omihomo-install install` |
| The daemon speaks an older protocol | Update | `bin/omihomo-install update` |
| The daemon speaks a newer protocol | Update widget | `bin/omihomo-install update` |
| The daemon refuses this account, or the socket is not this account's | Claim core | `sudo /usr/bin/omihomo claim` |

The panel tries again with a backoff, every 5 seconds while no daemon answers, and at once when
it opens. A connection that drops keeps the last status and data on screen, so a daemon restarted
under socket activation does not flash the install card, and fails every call still in flight.

Errors are the daemon's `error.message`; the panel shows it and falls back to wording for the
error's code when a message is empty.

Core power, TUN, acceleration, Tailscale, the tailnet proxy, and mode keep confirmed and desired
values separate. A click updates the desired value immediately, pushes that left the daemon
before the change continue updating only the confirmed value, and the change's reply, which is
the daemon's status after it, clears the desired value. A failed call clears it and rolls the
control back. This is the same optimistic-state pattern used by Omarchy's Tailscale panel.

A config selection stays pending until its reply, which carries the group as the core now has
it, and an egress check waits for it, so it never measures the route being replaced.

Everything read from the core belongs to one runtime: the active subscription and the proxy's
start time. A new start time or another subscription drops groups, latency results, the
connection log, and traffic.

## Panel

Every view is the same 420px surface at a fixed height, so switching tabs never moves or resizes
the popup. Top to bottom it carries:

1. **Header** — active subscription, resolved status (`ON · rule mode · TUN`), and the on/off
   switch for the core. On the configs and subscriptions views it adds the current rates, a graph of
   the last minute of traffic, and a readout of uptime, egress IP, and latency. Egress is the
   only field that costs a network call, so the daemon's `egress.check` runs on panel open, on a
   start, on a config, group, or subscription change, and on a click on egress or latency — never
   on a timer. It always measures the route through the proxy. The other views fold the header into one line to leave room for their lists.
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
reopened, which glued it to the name. Emoji trailing a name follow it. Every emoji in a name is an
image pango renders once into `~/.cache/omihomo/emoji`, not a font glyph: Qt 6.11 draws colour glyphs
on the popup's first open and blank on every reopen after it. Until an emoji's image exists, its glyph
stands in. The chosen config's row fades
from the accent colour, and a choice still waiting for the core fades more faintly. A pill takes
more of the urgent colour the closer it is to the group's slowest measured delay, none at its
fastest, and all of it on a timeout; the legend under the list says so and holds the whole-group test with how long ago it last ran. Browsing a group that is not the primary one offers a
`make primary` button next to its name. Only a `select` group can be chosen
from; the rest pick for themselves and are read-only. An empty view always says why — no active
subscription, a stopped core, a dropped connection, or a subscription that declares no groups.

Config details stay out of the list on purpose. A group's members carry their type, liveness, and
last delay, and nothing more: the daemon never exposes a config's address, port, or credentials.
A test is `delay.test` on the config's group, for one config or the whole group.

The **subscriptions view** is one card per subscription — activate, update, and remove, with its
quota as a bar and its expiry — and an add field that asks only for a URL, since the subscription
names itself. The URL goes to the daemon and no list shows it again: it usually carries a token. Removing asks for a second confirmation on the card.

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
the Tailscale integration and the tailnet proxy follow under their own heading, read-only; then
the subscription's, read-only. `rules.list` tags each rule with its source, so the panel never
guesses which is which. The list keeps the order
mihomo checks it in. A rule's target is DIRECT, REJECT, a group, or any config a group lists: `h`/`l`
cycles through them, and activating the target swaps the list for a filterable picker. `n` opens the
view with the form taking keys from anywhere in the panel.

The **connections view** is the same popup over a log rather than a live list. The `connections`
stream only states what is open right now, so `widget/Service.qml` keeps its own record: every push is diffed
against the one before it, and an id that has stopped appearing is retained as a closed entry with
the last figures it reported. The view draws open traffic as process → config flows.
Labels use the process name the core found; internal core
connections are labeled `mihomo`. The core runs as root and looks the process up itself, so
the panel needs no socket lookup of its own; forwarded traffic remains unidentified. Each flow
is as thick as the bytes it carried, with the pair under the cursor lit; past six processes or four configs the
smallest fold into `other`. Below it, the log stacks by process, destination, and state — Firefox's
four open sockets to `github.com` are one row reading `×4`, and the ones it has finished with are a
second, dimmed row. A bar behind each row is its bytes on a log scale. Open stacks sort above
closed ones, newest first within each, so a push only ever updates a row in place or puts a new
one at the top; nothing flashes and nothing disappears from under the cursor.

Open and closed are carried by a leading `●`/`○` and by brightness, not by hue: the panel has a
foreground, a dim, an accent, and an urgent, and no colour to spare for another state. Activating
an open stack closes every connection in it, in one `connections.close` call; activating a closed
one drops it from the log, which is the only place it exists. Closed entries are capped at 200,
oldest first, and the whole log is discarded when the core stops.

The **settings view**, behind the gear, holds state that is changed rarely: mode and TUN, TUN
acceleration, the Tailscale integration, release updates, and uninstall. Acceleration is the `tun_redirect`
setting, which moves `auto-redirect` and the TUN stack together. It is off by default; turning it
off is the repair for a core that reports `TUN acceleration cannot start on this machine`, and
its row says so while that is the reason the core is degraded. The Tailscale rows show only when
tailscaled is installed. There is no autostart and no repair: the service starts at boot and
restores the last on/off state, and as a root service it has no permissions to repair. Uninstall
hands `sudo pacman -R omihomo-bin` to `omarchy-launch-floating-terminal-with-presentation`. It
arms on the first activation and only runs on the second; Esc or moving the cursor off the button
cancels. Subscriptions and settings stay in `/var/lib/omihomo`.

Settings shows the installed package, the widget's version and the latest release, with check,
update, and release notes controls. Update is offered only after a successful check confirms a
newer package or a widget older than the release. Update moves the widget first, through
`omarchy plugin update`, then runs the updated checkout's installer for the package, so the
widget and the core move together. A checkout Omarchy does not manage, such as a linked
development checkout, is left alone. Checks run
as the normal user through `bin/omihomo-install check`; install and update run in the existing
floating terminal so authentication remains interactive. Only a locally validated HTTPS release
URL under the fixed project repository can open externally. Remote text never becomes a command.
Check errors stay in Settings and preserve the last successful result.

`UpdateService` checks once when enabled and every 12 hours thereafter. The cache lives for the
service's lifetime, including across panel opens; opening Settings does not trigger a request.
Manual checks bypass the cache. Checks reject malformed responses and output over 8 KiB, and
kill the check process after 30 seconds. `commandPath` is injectable for isolated tests and demos;
it defaults to the plugin's installer. `completed` fires after each attempt settles, including failures.

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
| `r` | Refresh: the daemon pushes every stream again, and the egress is checked again |
| `d` / `D` | Latency test the config / the whole group (configs) |
| `g` | Next group (configs) |
| `p` | Make the browsed group primary (configs) |
| `/` | Filter configs (configs); focus the rule tester (rules) |
| `u` | Update the selected subscription (subs); check for releases (settings) |
| `a` | Add a subscription (subs) |
| `A` | Close every open connection (conns); toggle TUN acceleration (settings) |
| `L` | Clear the connection log (conns) |
| `T` | Toggle the Tailscale integration (settings, when tailscaled is installed) |
| `P` | Toggle routing the tailnet through the proxy (settings, when tailscaled is installed) |
| `i` | Install, update, or claim the core (only on the card that offers it) |
| `esc` | Close the field, filter, or confirmation; otherwise go back to configs, then close the panel |

Close-all is `A` rather than the `X` its siblings would suggest, because `PanelKeyCatcher` claims
both cases of `x` for its delete key and an uppercase one never reaches the panel.

Every one of those is also reachable with the mouse. A focused text field owns `enter` and
`esc` and blocks the panel's key handling until it loses focus.

## Cost

Release checks run every 12 hours while the updater is enabled. Proxy state does not poll: the
bar costs one open socket and a `status` push whenever the status changes.
The panel's streams run only while it is open: the daemon pushes traffic and connections once a
second, and groups, rules, and subscriptions when they change. A connection that opens and
closes with the panel shut is never logged — the deliberate trade for a bar widget that costs
nothing for those streams when it is not on screen. Egress checks are
event-driven (see Panel).

## Installing it

```sh
omarchy plugin add https://github.com/readeem/omihomo.git --enable
```

Then place it with `omarchy bar move dev.readeem.omihomo`. Installing the core itself is the
panel's install card, or `bin/omihomo-install` in a terminal.

## Development

After building with `./tools/build-package`, run `./tools/test-cutover` from a local terminal.
It installs and claims the daemon before linking this checkout as the user plugin, preserves
an existing plugin directory as a backup, enables and places the widget, and runs
`omarchy restart shell`. This changes the installed proxy and can interrupt connections.
Use [the manual checklist](../tests/README.md#manual-cutover-test) after ending dependent sessions.

When the plugin already links this checkout, later QML changes need only `omarchy restart shell`.
Rebuild and reinstall the local package for Go changes.

## Verifying a change

`tests/run` includes Quickshell-native service tests, `tests/daemon_state_test.sh`. They run
`widget/Service.qml` against fake daemons (`tests/fixtures/fake-daemon.js`) on private sockets:
one that answers, one whose writes fail, one of an older and one of a newer protocol, one that
refuses the account, and a socket nobody listens on. They inject stale status pushes between a
click and its reply, drop the connection, and switch runtimes, and never touch the real daemon.

`tests/update_state_test.sh` runs only the non-rendering `UpdateService` controller with a fake
installer command. Its event-driven assertions cover successful checks, package revisions,
uninstalled and current packages, cached re-enabling, retained results on errors, malformed and
oversized output, rejected URLs, process failures, and the timeout. It never invokes the installer
or the live proxy, and waits on `completed` rather than polling.

`qmllint` catches syntax and binding mistakes without a compositor:

```sh
mkdir -p /tmp/omihomo-lint/qs
cp -r /usr/share/omarchy/shell/{Commons,Ui} /tmp/omihomo-lint/qs/
cd widget && /usr/lib/qt6/bin/qmllint -I /tmp/omihomo-lint *.qml components/*.qml views/*.qml
```

Expect warnings in the same categories the first-party panels produce (`unqualified`,
`missing-property` against the bar's `QObject`); a non-zero exit is a real error.

To see the widget run against a fake core, start a development daemon (`omihomo daemon --core
fake`, see [daemon.md](daemon.md#running-it)) and point the shell at it with `OMIHOMO_SOCKET`.

`tools/screenshots/drive` runs the real panel against the screenshot fixtures and drives it as
a user would, for an agent or a script to check a change:

```sh
tools/screenshots/drive start          # offscreen at 2×; --visible opens a desktop window instead,
                                       # --no-core starts with no core installed
tools/screenshots/drive keys 3 / Down  # characters, Qt key names, shift+Tab
tools/screenshots/drive type youtube.com
tools/screenshots/drive click 295 190  # screenshot pixels
tools/screenshots/drive shot /tmp/rules.png
tools/screenshots/drive state          # view, cursor, edit and error as JSON
tools/screenshots/drive stop
```

Keys and clicks go through Qt's input path from `QtTest`'s `TestEvent`, reached over `qs ipc`.
The fake daemon keeps the panel's writes in memory, and settings edits run the CLI built from the
checkout against it, in the session's private `HOME`. One session runs at a time, in
`$XDG_RUNTIME_DIR/omihomo-drive`, and ends on its own after an hour. The panel stands in for the
layer-shell popup as the screenshots do.

The fake installer, `tools/screenshots/omihomo-install`, keeps its package version in a file.
With `--no-core`, Install core installs 1.0.0-1 and starts the fake daemon; release 1.1.0-1 then
appears, and Update in Settings moves the package and the widget's version to it. Nothing is downloaded. Of the commands the panel hands
to the terminal, only those two run, without a shell; the rest, such as uninstall's `sudo`, are only
logged, so `drive log` shows what would have run.

## Screenshots

Screenshot capture rewrites the README images from invented fixture data:

```sh
tools/screenshots/capture            # rewrites preview.png and assets/screenshots/
tools/screenshots/capture /tmp/shots # or writes them somewhere else
```

It renders the real `widget/Panel.qml` offscreen in Quickshell at 2× and saves each view, then composes
`preview.png` from the configs, connections, and rules views over the Tokyo Night wallpaper. Each
view is 420×720 plus a one-pixel wallpaper edge; the preview is a 1600×900 desktop at 1.5×.

Everything that could vary is pinned, so a commit gives the same images on the same Omarchy
shell, Qt, font, and wallpaper:

- `tools/screenshots/data/` is the invented subscription, configs, rules, connections, and
  traffic, in the daemon's shapes, and `settings/` the owner's settings files. The fake daemon next to it, `daemon.js`, answers the panel from
  those files on a private socket, so no request leaves the machine and the live core and state are never touched. Use only invented
  names and documentation addresses (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`) there.
- `tools/screenshots/theme/` is the theme and font, read from a throwaway `HOME`. A stub
  `hyprctl` keeps the running desktop's rounding and gaps out, and a stub `xdg-open` the browser.
  `omihomo-install` there answers an update check with a newer release.
- Times are relative to the run and sit mid-unit, so uptime, update ages, and connection ages
  read the same.

`KeyboardPanel.qml` there replaces Omarchy's layer-shell window with the same card drawn in place,
because offscreen Quickshell has no layer shell. The images still follow the installed Omarchy
shell's `Commons` and `Ui`, the JetBrains Mono font, and the wallpaper from `omarchy-dev`, so
an Omarchy update can change them.
