#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
source "$OMIHOMO_ROOT/lib/common.sh"

core_install() {
  if [[ ! -t 0 ]]; then
    omi_error "core install must be run from a terminal" 1
  fi
  omi_require_command yay
  omi_with_lock omi_init_layout
  if ! yay -S --needed mihomo-bin libcap nftables yq jq curl >/dev/null 2>&1; then
    omi_error "mihomo installation failed" 1
  fi
  local binary
  binary=$(omi_mihomo_bin)
  [[ -n $binary ]] || omi_error "mihomo was not installed" 10
  if ! pkexec "$OMIHOMO_ROOT/libexec/root.sh" install /usr/bin/mihomo >/dev/null 2>&1; then
    omi_error "granting mihomo capabilities failed" 1
  fi
  mkdir -p "$HOME/.local/bin"
  ln -sfn "$OMIHOMO_ROOT/bin/omihomo" "$HOME/.local/bin/omihomo"
  omi_write_unit
  omi_systemctl --user daemon-reload
}

core_uninstall() {
  omi_require_command yay
  "$OMIHOMO_SYSTEMCTL" --user disable --now "$OMIHOMO_UNIT" >/dev/null 2>&1 || true
  rm -f "$OMIHOMO_UNIT_FILE" "$HOME/.local/bin/omihomo"
  "$OMIHOMO_SYSTEMCTL" --user daemon-reload >/dev/null 2>&1 || true
  if [[ -x $OMIHOMO_ROOT/libexec/root.sh ]]; then
    pkexec "$OMIHOMO_ROOT/libexec/root.sh" uninstall >/dev/null 2>&1 || omi_error "removing mihomo capabilities failed" 1
  fi
  if ! yay -Rns mihomo-bin >/dev/null 2>&1; then
    omi_error "mihomo removal failed" 1
  fi
}

core_repair() {
  omi_require_core
  if ! pkexec "$OMIHOMO_ROOT/libexec/root.sh" repair /usr/bin/mihomo >/dev/null 2>&1; then
    omi_error "repairing mihomo capabilities failed" 1
  fi
}

core_start() {
  omi_require_core
  omi_require_command nft
  omi_with_lock omi_init_layout
  [[ -n $(omi_active_name) ]] || omi_error "no active subscription" 13
  [[ -f $OMIHOMO_RUNTIME_FILE ]] || omi_error "active runtime config is missing" 13
  [[ -f $OMIHOMO_UNIT_FILE ]] || omi_write_unit
  omi_systemctl --user daemon-reload
  omi_systemctl --user start "$OMIHOMO_UNIT"
}

core_stop() {
  omi_require_core
  omi_systemctl --user stop "$OMIHOMO_UNIT"
}

core_restart() {
  omi_require_core
  omi_systemctl --user restart "$OMIHOMO_UNIT"
}

core_autostart() {
  omi_require_core
  case ${1:-} in
    on) omi_systemctl --user enable "$OMIHOMO_UNIT" ;;
    off) omi_systemctl --user disable "$OMIHOMO_UNIT" ;;
    *) omi_error "autostart expects on or off" 1 ;;
  esac
}

core_version() {
  omi_require_core
  local version
  if ! version=$($(omi_mihomo_bin) -v 2>/dev/null | head -n 1); then
    omi_error "mihomo version could not be read" 1
  fi
  jq -cn --arg version "$version" '{version: $version}'
}

omi_write_unit() {
  mkdir -p "$(dirname "$OMIHOMO_UNIT_FILE")"
  cat >"$OMIHOMO_UNIT_FILE" <<EOF
[Unit]
Description=Omihomo mihomo proxy core
After=network-online.target

[Service]
ExecStart=$(omi_mihomo_bin) -d $OMIHOMO_DATA_DIR -f $OMIHOMO_RUNTIME_FILE
Restart=on-failure

[Install]
WantedBy=default.target
EOF
}

case ${1:-} in
  install) core_install ;;
  uninstall) core_uninstall ;;
  repair) core_repair ;;
  start) core_start ;;
  stop) core_stop ;;
  restart) core_restart ;;
  autostart) core_autostart "${2:-}" ;;
  version) core_version ;;
  *) omi_error "unknown core command" 1 ;;
esac
