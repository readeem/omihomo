#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
source "$OMIHOMO_ROOT/lib/common.sh"

status_json() {
  local state=$1 detail=${2:-} active primary tun autostart permissions uptime tailscale tailscale_present
  active=$(omi_active_name)
  primary=
  tun=false
  autostart=false
  permissions=false
  tailscale=false
  tailscale_present=false
  uptime=
  if [[ -f $OMIHOMO_OVERRIDE_FILE ]] && omi_yq_available; then
    primary=$(omi_yq -r '.omihomo."primary-group" // ""' "$OMIHOMO_OVERRIDE_FILE")
    tun=$(omi_yq -r '.config.tun.enable // false' "$OMIHOMO_OVERRIDE_FILE")
    tailscale=$(omi_yq -r '.omihomo.tailscale // false' "$OMIHOMO_OVERRIDE_FILE")
  fi
  # Presence is the panel's cue to show the row at all, and it is the machine's
  # to answer rather than the override's.
  if omi_tailscale_present; then
    tailscale_present=true
  fi
  if [[ $state != not-installed ]] && omi_unit_enabled; then
    autostart=true
  fi
  if [[ $state != not-installed ]] && omi_tun_permissions_ok; then
    permissions=true
  fi
  if [[ $state != not-installed && $state != stopped ]]; then
    uptime=$("$OMIHOMO_SYSTEMCTL" --user show "$OMIHOMO_UNIT" --property=ActiveEnterTimestamp --value 2>/dev/null || true)
  fi
  jq -cn --arg state "$state" --arg detail "$detail" --arg active "$active" --arg primary "$primary" --arg uptime "$uptime" --argjson tun "$tun" --argjson autostart "$autostart" --argjson permissions "$permissions" --argjson tailscale "$tailscale" --argjson tailscale_present "$tailscale_present" \
    '{state: $state, status: $state, detail: (if $detail == "" then null else $detail end), ip: null, latency: null, download: null, upload: null, config: null, uptime: (if $uptime == "" then null else $uptime end), active_subscription: (if $active == "" then null else $active end), primary_group: (if $primary == "" then null else $primary end), tun_enabled: $tun, autostart_enabled: $autostart, permissions_ok: $permissions, tailscale_enabled: $tailscale, tailscale_present: $tailscale_present}'
}

# mihomo logs why the TUN adapter refused to come up and then keeps serving the
# proxy, so a missing device is all the panel would otherwise know. The reason
# is one line in the unit's journal, scoped to the current invocation so a run
# that has since been fixed never reports the failure before it.
tun_failure_reason() {
  local invocation line
  command -v journalctl >/dev/null 2>&1 || return 0
  invocation=$("$OMIHOMO_SYSTEMCTL" --user show "$OMIHOMO_UNIT" --property=InvocationID --value 2>/dev/null) || return 0
  [[ -n $invocation ]] || return 0
  line=$(journalctl --user "_SYSTEMD_INVOCATION_ID=$invocation" --output cat --no-pager 2>/dev/null |
    grep -F 'Start TUN listening error' | tail -n 1) || return 0
  [[ -n $line ]] || return 0
  line=${line#*'Start TUN listening error: '}
  line=${line%\"}
  # mihomo wraps the whole cause chain into one message and often repeats its
  # tail after an escaped newline. The first segment is the readable one.
  line=${line%%\\n*}
  printf '%s\n' "${line:0:160}"
}

# `device or resource busy` and `file exists` both mean a second proxy client
# already holds what mihomo is reaching for. Renaming the device settles the
# first, but `auto-redirect` installs nftables chains called `mihomo_prerouting`
# and friends, and those names are fixed inside the binary — so two mihomo cores
# can never both have TUN. Nothing here can repair that, and saying so is worth
# more to the user than the raw netlink error.
tun_failure_detail() {
  local reason
  reason=$(tun_failure_reason)
  case $reason in
    "") printf 'tun device is missing\n' ;;
    *'resource busy'* | *'file exists'* | *'address already in use'*)
      printf 'another proxy client is already using TUN\n' ;;
    *) printf 'tun failed to start: %s\n' "$reason" ;;
  esac
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
  if [[ -f $OMIHOMO_OVERRIDE_FILE ]] && omi_yq_available; then
    tun=$(omi_yq -r '.config.tun.enable // false' "$OMIHOMO_OVERRIDE_FILE")
  fi
  # Omihomo names its adapter after itself, but the merged runtime is the config
  # mihomo is actually running, and an override from before that default still
  # leaves the name to mihomo — which calls it `Meta`.
  device=${OMIHOMO_TUN_DEVICE:-}
  if [[ -z $device && -f $OMIHOMO_RUNTIME_FILE ]] && omi_yq_available; then
    device=$(omi_yq -r '.tun.device // ""' "$OMIHOMO_RUNTIME_FILE")
  fi
  device=${device:-Meta}
  if [[ $tun == true && ! -e /sys/class/net/$device ]]; then
    status_json degraded "$(tun_failure_detail)"
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
