#!/usr/bin/env bash

set -euo pipefail
umask 077

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd "$here/.." && pwd)
test_root=$(mktemp -d /tmp/omihomo-daemon-state.XXXXXX)
mkdir "$test_root/runtime"
daemons=()

cleanup() {
  for pid in "${daemons[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  [[ $test_root == /tmp/omihomo-daemon-state.* ]] && rm -rf -- "$test_root"
}
trap cleanup EXIT

# Each suite runs as its own Quickshell config against its own fake daemons
# (fixtures/fake-daemon.js), one per behaviour, so no state leaks between
# suites. `none.sock` is never created: no daemon answers there.
go build -o "$test_root/omihomo" "$project_root/cmd/omihomo"
cat > "$test_root/edit" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
export XDG_CONFIG_HOME="${OMIHOMO_SOCKET%.sock}.config"
exec "$(dirname "$0")/omihomo" "$@"
SCRIPT
chmod +x "$test_root/edit"

for suite in service_state_test live_state_test; do
  config="$test_root/$suite"
  sockets="$config/sockets"
  mkdir -p "$sockets"
  sed 's|import "../widget"|import "."|' "$here/$suite.qml" >"$config/shell.qml"
  cp "$here/StateTest.qml" "$project_root/widget/Service.qml" "$project_root/widget/Model.js" "$config/"

  sed -i "s|/usr/bin/omihomo|$test_root/edit|g" "$config/Service.qml"

  start_daemon() {
    local name=$1
    shift
    node "$here/fixtures/fake-daemon.js" "$sockets/$name.sock" "$@" >"$sockets/$name.log" 2>&1 &
    daemons+=("$!")
  }
  start_daemon ok --latency 150
  start_daemon fail --latency 50 --fail start,settings.apply,subscriptions.add
  start_daemon old --protocol 0
  start_daemon new --protocol 3
  start_daemon refuse --refuse
  start_daemon busy --busy-once
  for name in ok fail old new refuse busy; do
    for _ in $(seq 100); do [[ -S $sockets/$name.sock ]] && break; sleep 0.05; done
    [[ -S $sockets/$name.sock ]] || { cat "$sockets/$name.log" >&2; exit 1; }
  done

  output=$(OMIHOMO_TEST_SOCKETS="$sockets" WAYLAND_DISPLAY='' QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME='' \
    XDG_RUNTIME_DIR="$test_root/runtime" QS_DISABLE_CRASH_HANDLER=1 \
    timeout --kill-after=2s 20s qs --no-color -p "$config" 2>&1) || {
    printf '%s\n' "$output" >&2
    exit 1
  }
  printf '%s\n' "$output" | grep -E 'STATE_(PASS|FAIL)'
  grep -q 'STATE_PASS' <<<"$output" || { printf '%s\n' "$output" >&2; exit 1; }

  for pid in "${daemons[@]}"; do kill "$pid" 2>/dev/null || true; done
  daemons=()
done
