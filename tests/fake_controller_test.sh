#!/usr/bin/env bash

set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd "$here/.." && pwd)
test_root=$(mktemp -d /tmp/omihomo-fake-controller.XXXXXX)

cleanup() {
  [[ $test_root == /tmp/omihomo-fake-controller.* ]] && rm -rf -- "$test_root"
}
trap cleanup EXIT

# Each suite runs as its own Quickshell config against fixtures/fake-curl, which
# answers the controller's endpoints and keeps its state in FAKE_CONTROLLER_STATE.
for suite in ping_state_test live_state_test; do
  config="$test_root/$suite"
  mkdir -p "$config/bin"
  cp "$here/$suite.qml" "$config/shell.qml"
  cp "$project_root/Service.qml" "$project_root/Model.js" "$config/"
  cp "$here/fixtures/fake-curl" "$config/bin/curl"
  cp "$here/fixtures/fake-cli" "$config/bin/omihomo-fake-cli"
  cp -r /usr/share/omarchy/shell/Commons "$config/"

  output=$(PATH="$config/bin:$PATH" FAKE_CONTROLLER_STATE=$config WAYLAND_DISPLAY= \
    QT_QPA_PLATFORM=offscreen timeout 8s qs --no-color -p "$config" 2>&1) || {
    printf '%s\n' "$output" >&2
    exit 1
  }
  printf '%s\n' "$output"
  grep -q '_STATE_PASS' <<<"$output"
done
