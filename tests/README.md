# Tests

Five suites, one runner:

- `model_test.js` exercises `Model.js`, the widget's parsing and formatting seam, under
  `node --test`. Everything the panel renders passes through it, and none of it needs QML.
- `service_state_test.sh` runs `Service.qml` in an isolated Quickshell config and verifies that
  a stale status response cannot repaint over a pending toggle, that an unconfirmed toggle gives
  up after its deadline, and that a controller outage keeps the last snapshot. Its fake CLI is
  `/usr/bin/true`, so it cannot touch the real core, systemd unit, files, or controller.
- `fake_controller_test.sh` runs `ping_state_test.qml` and `live_state_test.qml` against a fake
  controller. The first holds requests open to verify the in-progress, success, and persistent
  failure states for single, bulk, and egress latency checks. The second verifies that a read
  already in flight cannot undo a selection and that a traffic stream which ends reconnects.
- `cli_test.sh` runs the public `bin/omihomo` interface with isolated XDG directories and
  command stubs for mihomo, curl, and systemd.
- `auto_update_test.sh` uses the same isolated CLI boundary with a fixed clock. It checks
  deadlines across sessions, manual updates, failure/retry, concurrent checks, stopped cores,
  connection restoration, removed configs, rollback, and timer installation on upgrade.

`real_provider_test.sh` is an optional contract check against the installed Mihomo binary. It
starts an isolated core in a temporary directory, verifies plain and base64 share-link providers,
and confirms that garbage produces no configs and a provider parse error. It never contacts a
subscription URL or the running Omihomo service. Run it with the rest of the suite when changing
subscription import behavior:

```sh
OMIHOMO_REAL_CORE_TEST=1 ./tests/run
```

The CLI tests need `yq` v4 because YAML merge behavior is part of the contract. If `yq` is not
on `PATH`, point the runner at an isolated executable:

```sh
OMIHOMO_TEST_YQ=/path/to/yq ./tests/run
```

The rest of the panel QML is checked with `qmllint` and by running the real widget against
fixture data in a nested compositor. See [docs/widget.md](../docs/widget.md).
