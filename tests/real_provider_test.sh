#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

for command in mihomo yq curl jq; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'real provider test requires %s\n' "$command" >&2
    exit 2
  }
done

check_provider() (
  local fixture=$1 expected_count=$2 expected_error=${3:-} directory socket config provider log response pid= output= count attempt
  directory=$(mktemp -d)
  socket="$directory/controller.sock"
  config="$directory/config.yaml"
  provider="$directory/provider.txt"
  log="$directory/mihomo.log"
  response="$directory/provider.json"
  cleanup() {
    if [[ -n ${pid:-} ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
    rm -rf -- "$directory"
  }
  trap cleanup EXIT

  cp "$fixture" "$provider"
  OMIHOMO_PREFLIGHT_PROVIDER=$provider OMIHOMO_PREFLIGHT_SOCKET=$socket yq -n -P '
    ."external-controller-unix" = strenv(OMIHOMO_PREFLIGHT_SOCKET) |
    ."proxy-providers".subscription = {
      "type": "file",
      "path": strenv(OMIHOMO_PREFLIGHT_PROVIDER)
    } |
    ."proxy-groups" = [{
      "name": "Proxy",
      "type": "select",
      "use": ["subscription"]
    }] |
    .rules = ["MATCH,Proxy"]
  ' >"$config"

  mihomo -d "$directory" -f "$config" >"$log" 2>&1 &
  pid=$!
  for attempt in {1..100}; do
    if output=$(curl -fsS --max-time 1 --unix-socket "$socket" \
      http://localhost/providers/proxies/subscription 2>/dev/null); then
      break
    fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.05
  done
  [[ -n $output ]] || { printf 'provider API never became ready for %s\n' "$fixture" >&2; exit 1; }
  printf '%s\n' "$output" >"$response"
  count=$(jq -r '.proxies | length' "$response")
  [[ $count == "$expected_count" ]] || {
    printf 'expected %s configs from %s, got %s\n' "$expected_count" "$fixture" "$count" >&2
    exit 1
  }
  if [[ -n $expected_error ]]; then
    grep -Fq "$expected_error" "$log" || {
      printf 'expected %s to report %s\n' "$fixture" "$expected_error" >&2
      exit 1
    }
  fi
)

check_provider "$repo_root/tests/fixtures/raw-plain.txt" 2
check_provider "$repo_root/tests/fixtures/raw-base64.txt" 2
check_provider "$repo_root/tests/fixtures/garbage.txt" 0 'format invalid'
printf 'PASS real mihomo provider preflight\n'
