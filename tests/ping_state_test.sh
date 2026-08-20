#!/usr/bin/env bash

set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd "$here/.." && pwd)
test_root=$(mktemp -d /tmp/omihomo-ping-state.XXXXXX)

cleanup() {
  [[ $test_root == /tmp/omihomo-ping-state.* ]] && rm -rf -- "$test_root"
}
trap cleanup EXIT

mkdir -p "$test_root/bin"
cp "$here/ping_state_test.qml" "$test_root/shell.qml"
cp "$project_root/Service.qml" "$project_root/Model.js" "$test_root/"
cp "$here/fixtures/fake-curl" "$test_root/bin/curl"
cp -r /usr/share/omarchy/shell/Commons "$test_root/"

output=$(PATH="$test_root/bin:$PATH" WAYLAND_DISPLAY= QT_QPA_PLATFORM=offscreen \
  timeout 8s qs --no-color -p "$test_root" 2>&1) || {
  printf '%s\n' "$output" >&2
  exit 1
}
printf '%s\n' "$output"
grep -q 'PING_STATE_PASS' <<<"$output"
