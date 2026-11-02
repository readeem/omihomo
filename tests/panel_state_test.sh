#!/usr/bin/env bash
set -euo pipefail
umask 077

tests_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$tests_dir/../tools/screenshots/stage"
work=$(mktemp -d /tmp/omihomo-panel-state.XXXXXXXX)
daemon=
cleanup() {
  if [[ -n $daemon ]]; then kill "$daemon" 2>/dev/null || true; wait "$daemon" 2>/dev/null || true; fi
  rm -rf -- "$work"
}
trap cleanup EXIT
stage "$work" "$tests_dir/panel_state_test.qml"
cp "$tests_dir/StateTest.qml" "$config/"
(cd "$root" && go build -o "$work/omihomo" ./cmd/omihomo)
start_daemon "$work"
output=$("${panel_env[@]}" QT_QPA_PLATFORM=offscreen QS_DISABLE_CRASH_HANDLER=1 \
  OMIHOMO_TEST_CLI="$work/omihomo" timeout --kill-after=2s 25s qs --no-color -p "$config" 2>&1) || {
  printf '%s\n' "$output" >&2
  exit 1
}
printf '%s\n' "$output" | rg 'PANEL_STATE_(PASS|FAIL)'
rg -q 'PANEL_STATE_PASS' <<<"$output" || { printf '%s\n' "$output" >&2; exit 1; }
