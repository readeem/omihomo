# Omihomo

Manage a [mihomo](https://wiki.metacubex.one) proxy core from the Omarchy 4 bar: subscriptions,
groups, configs, latency, and live connection state — with a small bash CLI underneath that owns
the core's install, its systemd unit, and everything that touches disk.

## Install

```sh
omarchy plugin add https://github.com/readeem/omihomo.git --enable
```

Place it with `omarchy bar move omihomo`. The panel's install action builds the core from the
AUR in a floating terminal; `omihomo core install` does the same by hand.

### Install from a local checkout

For development, link the checkout into Omarchy's user plugin directory:

```sh
ln -sfn "$(pwd)" ~/.config/omarchy/plugins/omihomo
omarchy plugin enable omihomo
omarchy bar move omihomo
omarchy-shell shell rescanPlugins
```

Run those commands from the repository root. Omarchy will use the checkout directly, so saved
changes reload automatically. Verify that the plugin was discovered with:

```sh
omarchy plugin list --json | jq '.[] | select(.id == "omihomo")'
```

To remove the local installation without deleting the checkout:

```sh
rm ~/.config/omarchy/plugins/omihomo
```

## Use

Left click opens the panel, right click starts or stops the core. Inside the panel `j`/`k`
moves, `enter` activates, and single letters do the rest — `s` core, `t` TUN, `m` mode, `c`
connections, `a` add subscription, `n` new rule, `d` latency. The full key table is in
[docs/widget.md](./docs/widget.md).

Subscription URLs may return a full Mihomo YAML config or a raw share-link format supported by
the installed Mihomo version. Raw subscriptions get one generated `Proxy` selector and a final
`MATCH,Proxy` rule.

## Layout

```text
Panel.qml, Service.qml, Model.js, OmihomoIcon.qml   the bar widget
bin/omihomo, libexec/, lib/                          the CLI
tests/run                                            both test suites
```

## Docs

Status: **charted.** The design was worked out as a
[wayfinder map](../../issues?q=label%3Awayfinder%3Amap).

- [CONTEXT.md](./CONTEXT.md) — vocabulary
- [docs/adr/](./docs/adr/) — decisions
- [docs/cli.md](./docs/cli.md) — the CLI contract
- [docs/widget.md](./docs/widget.md) — the widget's structure, keys, and how to verify a change
