#!/usr/bin/env bash
# Loads the panel on the shell of each Omarchy channel, the latest stable tag
# and the edge branch, fetched from Omarchy's repository. A suite file, such as
# panel_state_test.qml, runs that suite instead.
set -euo pipefail
umask 077

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${OMARCHY_REPO:-https://github.com/basecamp/omarchy.git}
edge=${OMARCHY_EDGE_BRANCH:-quattro}
work=$(mktemp -d /tmp/omihomo-omarchy.XXXXXXXX)
trap 'rm -rf -- "$work"' EXIT

stable=$(git ls-remote --tags --refs "$repo" 'v*' | awk '{ sub("refs/tags/", "", $2); print $2 }' |
  grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n 1)
[[ -n $stable ]] || { printf 'no stable Omarchy tag in %s\n' "$repo" >&2; exit 1; }

for ref in "$stable" "$edge"; do
  checkout=$work/$ref
  git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$ref" --filter=blob:none --sparse "$repo" "$checkout"
  git -C "$checkout" sparse-checkout set shell
  printf 'Omarchy %s (%s): ' "$ref" "$(git -C "$checkout" rev-parse --short HEAD)"
  OMARCHY_SHELL=$checkout/shell "$here/panel_state_test.sh" "${1:-panel_load_test.qml}"
done
