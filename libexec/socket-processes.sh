#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
source "$OMIHOMO_ROOT/lib/common.sh"

empty='{"addresses":[],"sockets":[]}'
if ! addresses=$(timeout 2s ip -j address show 2>/dev/null | head -c 1048576 |
  jq -c '[.[].addr_info[]?.local]'); then
  printf '%s\n' "$empty"
  exit 0
fi
if ! sockets=$(timeout 2s ss -Hantup 2>/dev/null | head -c 1048576 | jq -Rsc '
  def endpoint:
    capture("^(?<ip>.*):(?<port>[0-9]+|\\*)$") |
    {ip: (.ip | sub("^\\["; "") | sub("\\]$"; "")), port: (.port | tonumber? // 0)};
  split("\n") | map(
    try (capture("^(?<network>tcp|udp)\\s+\\S+\\s+\\S+\\s+\\S+\\s+(?<local>\\S+)\\s+(?<peer>\\S+)\\s*(?<owners>.*)$") |
      (.local | endpoint) as $local | (.peer | endpoint) as $peer |
      (.owners | [capture("^users:\\(\\(\"(?<process>[A-Za-z0-9_.+() -]{1,128})\",pid=(?<pid>[0-9]+),fd=[0-9]+\\)\\)$")][0] // {}) as $owner |
      {network, localIP: $local.ip, localPort: $local.port, peerIP: $peer.ip, peerPort: $peer.port,
        process: ($owner.process // ""), pid: ($owner.pid // "")}) catch empty)
'); then
  printf '%s\n' "$empty"
  exit 0
fi

owned=()
owner_uid=$(id -u)
while IFS= read -r pid; do
  if [[ $(stat -c %u "/proc/$pid" 2>/dev/null) == "$owner_uid" ]]; then
    owned+=("$pid")
  fi
done < <(jq -r '[.[].pid | select(. != "")] | unique[]' <<<"$sockets")

jq -cn --argjson addresses "$addresses" --argjson sockets "$sockets" \
  --arg owned " ${owned[*]} " '{addresses: $addresses, sockets: ($sockets | map(.pid as $pid |
    if $pid == "" or ($owned | contains(" " + $pid + " ") | not) then .process = "" else . end | del(.pid)))}'
