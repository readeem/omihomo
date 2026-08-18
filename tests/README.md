# Tests

Two suites, one runner:

- `model_test.js` exercises `Model.js`, the widget's parsing and formatting seam, under
  `node --test`. Everything the panel renders passes through it, and none of it needs QML.
- `cli_test.sh` runs the public `bin/omihomo` interface with isolated XDG directories and
  command stubs for mihomo, curl, and systemd.

The CLI tests need `yq` v4 because YAML merge behavior is part of the contract. If `yq` is not
on `PATH`, point the runner at an isolated executable:

```sh
OMIHOMO_TEST_YQ=/path/to/yq ./tests/run
```

The panel's QML has no unit tests; it is checked with `qmllint` and by running the real widget
against fixture data in a nested compositor. See [docs/widget.md](../docs/widget.md).
