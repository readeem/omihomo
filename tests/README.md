# Tests

Four suites, one runner:

- `model_test.js` exercises `widget/Model.js`, the widget's parsing and formatting seam, under
  `node --test`. Every daemon message the panel renders passes through it, and none of it needs QML.
- `daemon_state_test.sh` runs `widget/Service.qml` in isolated Quickshell configs against fake
  daemons, `fixtures/fake-daemon.js`, each on a private socket: one that answers, one whose writes
  fail, an older and a newer protocol, one that refuses the account, and a socket nobody listens
  on. `service_state_test.qml` covers the connection, optimistic writes that a stale push cannot
  undo and a failed reply rolls back, a dropped connection that keeps the last state, and a new
  runtime. `live_state_test.qml` covers an open panel: latency tests in progress, success, and
  failure, a selection that holds the egress check until its reply, traffic, connections, and a
  subscription switch. The fake daemons hold no real state and never touch the network.
- `cli_test.sh` runs the public `bin/omihomo` interface with isolated XDG directories and
  command stubs for mihomo, curl, and systemd.
- `auto_update_test.sh` uses the same isolated CLI boundary with a fixed clock. It checks
  deadlines across sessions, manual updates, failure/retry, concurrent checks, stopped cores,
  connection restoration, removed configs, rollback, and timer installation on upgrade. It
  also checks provider intervals, the 12-hour fallback, metadata changes committed only on
  success, legacy records and healthy subscriptions behind long sequences of slow failures.

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

The Go daemon and its command line are tested with `go test ./...` (see
[docs/daemon.md](../docs/daemon.md)). Those tests run a real daemon on a temporary socket against
the fake core in `internal/core/fakecore`. They connect as the owner and verify that any other uid,
a missing owner, or an untrusted owner file is refused before a request is read. CI runs them with
`go vet`, `govulncheck` and `tests/run` on every pull request.

CI runs `tests/run` in an Arch Linux container with Quickshell, and sets `OMIHOMO_REQUIRE_QML=1`,
which turns the skipped QML service suites into a failure.
