#!/usr/bin/env bash

set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd "$here/.." && pwd)
test_root=$(mktemp -d /tmp/omihomo-service-state.XXXXXX)

cleanup() {
  [[ $test_root == /tmp/omihomo-service-state.* ]] && rm -rf -- "$test_root"
}
trap cleanup EXIT

cp "$here/service_state_test.qml" "$test_root/shell.qml"
cp "$project_root/Service.qml" "$project_root/Model.js" "$test_root/"
cp -r /usr/share/omarchy/shell/Commons "$test_root/"

output=$(WAYLAND_DISPLAY= QT_QPA_PLATFORM=offscreen timeout 5s qs --no-color -p "$test_root" 2>&1) || {
  printf '%s\n' "$output" >&2
  exit 1
}
printf '%s\n' "$output"
grep -q 'SERVICE_STATE_PASS' <<<"$output"
