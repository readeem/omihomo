# The panel talks to mihomo's API directly; the CLI owns disk and systemd

The obvious shape would be a single seam where the QML panel only ever shells out to the CLI.
We rejected it: live state is a streaming `curl -N http://127.0.0.1:9090/traffic`, and putting
our own process in that path adds a layer whose only job is reformatting JSON.

The split is: **API for live state, CLI for anything that touches disk or systemd.** The panel
curls the external controller for proxies, groups, delay tests, traffic, connections, and mode.
It calls the CLI for subscriptions, config parameters, service lifecycle, and overrides.

Ticket #8 refined the persisted-settings edge of this split. `set mode` writes the desired mode
to the override layer and reloads the generated config; `set group` stores the panel's primary
group metadata and does not select a mihomo group. Live group/config selection, latency,
connections, and parameter lookup remain direct API calls.

## Consequences

The panel's fast paths keep working while the CLI is mid-refactor, and neither surface has to
know the other's internals. The cost is two callers of mihomo instead of one, so the
external-controller address and secret have to be discoverable by both.
