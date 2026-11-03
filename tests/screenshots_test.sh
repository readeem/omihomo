#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
git=(git -C "$work" -c user.name=test -c user.email=test@example.org)
"${git[@]}" init -q
mkdir -p "$work/widget" "$work/assets/screenshots" "$work/tools/screenshots"
touch "$work/widget/Panel.qml" "$work/preview.png" "$work/README.md" "$work/tools/screenshots/stage"
"${git[@]}" add -A && "${git[@]}" commit -qm base
fresh() { (cd "$work" && "$root/tools/screenshots/fresh" "$("${git[@]}" rev-list --max-parents=0 HEAD)") 2>/dev/null; }
change() { printf '%s\n' "$RANDOM" >> "$work/$1"; "${git[@]}" commit -qam "$2"; }

change README.md docs
fresh || { echo 'FAIL a change outside the demo needs no screenshots'; exit 1; }
change widget/Panel.qml widget
! fresh || { echo 'FAIL a widget change without screenshots passed'; exit 1; }
change README.md $'docs\n\nScreenshots:'
! fresh || { echo 'FAIL an empty rationale passed'; exit 1; }
change README.md $'docs\n\nScreenshots: unchanged, the timer is invisible'
fresh || { echo 'FAIL a rationale trailer was refused'; exit 1; }
"${git[@]}" reset -q --hard HEAD~2
change tools/screenshots/stage demo
! fresh || { echo 'FAIL a demo change without screenshots passed'; exit 1; }
"${git[@]}" rm -q preview.png && "${git[@]}" commit -qm 'drop shots'
! fresh || { echo 'FAIL deleting screenshots counted as a recapture'; exit 1; }
"${git[@]}" reset -q --hard HEAD~2
change preview.png shots
fresh || { echo 'FAIL recaptured screenshots were refused'; exit 1; }
printf 'PASS stale screenshots need a recapture or a rationale\n'
