# Tests

Three suites, one runner:

- `model_test.js` exercises `Model.js`, the widget's parsing and formatting seam, under
  `node --test`. Everything the panel renders passes through it, and none of it needs QML.
- `service_state_test.sh` runs `Service.qml` in an isolated Quickshell config and verifies that
  a stale status response cannot repaint over a pending toggle. Its fake CLI is `/usr/bin/true`,
  so it cannot touch the real core, systemd unit, files, or controller.
- `cli_test.sh` runs the public `bin/omihomo` interface with isolated XDG directories and
  command stubs for mihomo, curl, and systemd.

The CLI tests need `yq` v4 because YAML merge behavior is part of the contract. If `yq` is not
on `PATH`, point the runner at an isolated executable:

```sh
OMIHOMO_TEST_YQ=/path/to/yq ./tests/run
```

The rest of the panel QML is checked with `qmllint` and by running the real widget against
fixture data in a nested compositor. See [docs/widget.md](../docs/widget.md).
