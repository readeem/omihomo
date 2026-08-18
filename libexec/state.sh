#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
source "$OMIHOMO_ROOT/lib/common.sh"

status_json() {
  local state=$1 detail=${2:-} active primary tun uptime
  active=$(omi_active_name)
  primary=
  tun=false
  uptime=
  if [[ -f $OMIHOMO_OVERRIDE_FILE ]] && command -v "$OMIHOMO_YQ" >/dev/null 2>&1; then
    primary=$("$OMIHOMO_YQ" -r '.omihomo."primary-group" // ""' "$OMIHOMO_OVERRIDE_FILE")
    tun=$("$OMIHOMO_YQ" -r '.config.tun.enable // false' "$OMIHOMO_OVERRIDE_FILE")
  fi
  if [[ $state != not-installed && $state != stopped ]]; then
    uptime=$("$OMIHOMO_SYSTEMCTL" --user show "$OMIHOMO_UNIT" --property=ActiveEnterTimestamp --value 2>/dev/null || true)
  fi
  jq -cn --arg state "$state" --arg detail "$detail" --arg active "$active" --arg primary "$primary" --arg uptime "$uptime" --argjson tun "$tun" \
    '{state: $state, status: $state, detail: (if $detail == "" then null else $detail end), ip: null, latency: null, download: null, upload: null, config: null, uptime: (if $uptime == "" then null else $uptime end), active_subscription: (if $active == "" then null else $active end), primary_group: (if $primary == "" then null else $primary end), tun_enabled: $tun}'
}

command_status() {
  if ! omi_core_installed; then
    status_json not-installed "mihomo is not installed"
    return 0
  fi
  if ! omi_unit_active; then
    status_json stopped "mihomo service is stopped"
    return 0
  fi
  if ! omi_api_reachable; then
    status_json starting "mihomo controller is unreachable"
    return 0
  fi
  local tun device
  tun=false
  if [[ -f $OMIHOMO_OVERRIDE_FILE ]] && command -v "$OMIHOMO_YQ" >/dev/null 2>&1; then
    tun=$("$OMIHOMO_YQ" -r '.config.tun.enable // false' "$OMIHOMO_OVERRIDE_FILE")
  fi
  device=${OMIHOMO_TUN_DEVICE:-mihomo}
  if [[ $tun == true && ! -e /sys/class/net/$device ]]; then
    status_json degraded "tun device is missing"
    return 0
  fi
  status_json on
}

command_api_info() {
  omi_require_core
  [[ -f $OMIHOMO_OVERRIDE_FILE ]] || omi_error "Omihomo is not initialized" 1
  local address secret
  address=$(omi_api_address)
  secret=$(omi_api_secret)
  jq -cn --arg address "$address" --arg secret "$secret" '{address: $address, secret: $secret}'
}

case ${1:-} in
  status) command_status ;;
  api-info) command_api_info ;;
  *) omi_error "unknown state command" 1 ;;
esac
