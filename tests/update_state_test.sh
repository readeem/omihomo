#!/usr/bin/env bash
set -euo pipefail
umask 077

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd "$here/.." && pwd)
test_root=$(mktemp -d /tmp/omihomo-update-state.XXXXXX)
cleanup() {
  [[ $test_root == /tmp/omihomo-update-state.* ]] && rm -rf -- "$test_root"
}
trap cleanup EXIT
mkdir "$test_root/runtime" "$test_root/config" "$test_root/fixtures"
cp "$here/update_state_test.qml" "$test_root/config/shell.qml"
cp "$project_root/widget/UpdateService.qml" "$test_root/config/"
cat > "$test_root/fixtures/command" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
[[ $# == 1 && $1 == check ]] || exit 99
installed=1.0.0-1
available=1.0.1-1
plugin=1.0.0
update=true
url=https://github.com/readeem/omihomo/releases/tag/v1.0.1
case ${0##*/} in
  error) printf '%s\n' '{"error":"Network unavailable"}'; exit 1 ;;
  malformed) printf '%s\n' 'not json'; exit ;;
  array) printf '%s\n' '[]'; exit ;;
  multiple) printf '%s\n' '{}' '{}'; exit ;;
  url) url=https://example.com/releases/tag/v1.0.1 ;;
  type) update='"true"' ;;
  downgrade) installed=2.0.0-1; plugin=1.0.1 ;;
  stale) update=false ;;
  plugin) plugin=1.0.0-1 ;;
  revision) installed=1.0.1-1; available=1.0.1-2; plugin=1.0.1 ;;
  widget) installed=1.0.1-1; available=1.0.1-1 ;;
  equal) installed=1.0.1-2; available=1.0.1-2; plugin=1.0.1; update=false ;;
  absent) installed=; update=false ;;
  oversized) printf '%9000s' x; exit ;;
  hang) read -r line < "${0%/*}/block"; exit ;;
esac
printf '{"installed":"%s","available":"%s","plugin":"%s","update_available":%s,"release_url":"%s"}\n' "$installed" "$available" "$plugin" "$update" "$url"
[[ ${0##*/} != nonzero ]]
SCRIPT
chmod +x "$test_root/fixtures/command"
mkfifo "$test_root/fixtures/block"
for fixture in 'good command' error malformed array multiple url type downgrade stale plugin nonzero oversized revision widget equal absent hang; do
  ln -s command "$test_root/fixtures/$fixture"
done

output=$(OMIHOMO_UPDATE_FIXTURES="$test_root/fixtures" WAYLAND_DISPLAY='' QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME='' \
  XDG_RUNTIME_DIR="$test_root/runtime" QS_DISABLE_CRASH_HANDLER=1 \
  timeout --kill-after=2s 45s qs --no-color -p "$test_root/config" 2>&1) || {
  printf '%s\n' "$output" >&2
  exit 1
}
printf '%s\n' "$output" | grep -E 'UPDATE_STATE_(PASS|FAIL)'
[[ $output == *UPDATE_STATE_PASS* && $output != *UPDATE_STATE_FAIL* ]] || {
  printf '%s\n' "$output" >&2
  exit 1
}
