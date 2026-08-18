# CLI tests

The tests run the public `bin/omihomo` interface with isolated XDG directories and command
stubs for mihomo, curl, and systemd. They need `yq` v4 because YAML merge behavior is part of the
contract. If `yq` is not on `PATH`, point the runner at an isolated executable:

```sh
OMIHOMO_TEST_YQ=/path/to/yq ./tests/run
```
