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
  # One password prompt for the whole install: sudo caches the credential and
  # every privileged step below reuses it, so nothing pops a second dialog.
  sudo -v || omi_error "sudo is required to install mihomo" 1
  omi_install_packages
  local binary
  binary=$(omi_mihomo_bin)
  [[ -n $binary ]] || omi_error "mihomo was not installed" 10
  if ! omi_privileged "$OMIHOMO_ROOT/libexec/root.sh" install /usr/bin/mihomo; then
    omi_error "granting mihomo capabilities failed" 1
  fi
  mkdir -p "$HOME/.local/bin"
  ln -sfn "$OMIHOMO_ROOT/bin/omihomo" "$HOME/.local/bin/omihomo"
  omi_write_unit
  omi_systemctl --user daemon-reload
  omi_note "mihomo is installed. Open the panel and add a subscription."
}

# Repo packages through pacman, the core through yay, both unattended the way
# Omarchy's own install scripts run them. Output stays on the terminal: it is
# the only place the real reason for a failure is written.
omi_install_packages() {
  local conflict=() status=0
  # Arch's `yq` package is the jq wrapper and owns the same /usr/bin/yq as
  # go-yq, so pacman has to be told it may replace it.
  if pacman -Qq yq >/dev/null 2>&1; then
    omi_note "replacing the yq package (a jq wrapper) with go-yq, the yq mihomo configs need"
    conflict=(--ask 4)
  fi
  sudo pacman -S --needed --noconfirm "${conflict[@]}" go-yq jq libcap nftables curl || status=$?
  if ((status != 0)); then
    omi_error "installing the omihomo dependencies failed: pacman exited $status" 1
  fi
  yay -S --needed --noconfirm --answerdiff=None --answerclean=None mihomo-bin || status=$?
  if ((status != 0)); then
    omi_error "mihomo installation failed: yay exited $status$(omi_aur_hint)" 1
  fi
}

# yay reports an unreachable AUR as "no AUR package found", which reads like the
# package was renamed. Probing the RPC endpoint separates the two.
omi_aur_hint() {
  "$OMIHOMO_CURL" -fsS --max-time 5 -o /dev/null \
    'https://aur.archlinux.org/rpc/v5/info?arg[]=mihomo-bin' 2>/dev/null && return 0
  printf ' (aur.archlinux.org is unreachable from this machine, so the AUR build cannot start)'
}

# Removes everything `core install` created: the unit, the capabilities and
# their pacman hook, the ~/.local/bin symlink, the mihomo package, and the state
# directory. `--keep-data` spares subscriptions, override, and cache.
core_uninstall() {
  local keep_data=0
  case ${1:-} in
    "") ;;
    --keep-data) keep_data=1 ;;
    *) omi_error "uninstall expects --keep-data or no argument" 1 ;;
  esac
  if [[ -t 0 ]] && command -v sudo >/dev/null 2>&1; then
    sudo -v || omi_error "sudo is required to uninstall mihomo" 1
  fi

  "$OMIHOMO_SYSTEMCTL" --user disable --now "$OMIHOMO_UNIT" >/dev/null 2>&1 || true
  rm -f "$OMIHOMO_UNIT_FILE"
  "$OMIHOMO_SYSTEMCTL" --user daemon-reload >/dev/null 2>&1 || true

  if [[ -x $OMIHOMO_ROOT/libexec/root.sh ]]; then
    omi_privileged "$OMIHOMO_ROOT/libexec/root.sh" uninstall ||
      omi_error "removing mihomo capabilities failed" 1
  fi

  # The symlink is only ours if it still points into this checkout.
  local link="$HOME/.local/bin/omihomo"
  if [[ -L $link && $(readlink -f "$link") == "$(readlink -f "$OMIHOMO_ROOT/bin/omihomo")" ]]; then
    rm -f "$link"
  fi

  if pacman -Qq mihomo-bin >/dev/null 2>&1; then
    omi_require_command yay
    local status=0
    yay -Rns --noconfirm mihomo-bin || status=$?
    if ((status != 0)); then
      omi_error "mihomo removal failed: yay exited $status" 1
    fi
  fi

  if ((keep_data == 0)); then
    rm -rf "$OMIHOMO_DATA_DIR"
    omi_note "removed mihomo, its unit, and $OMIHOMO_DATA_DIR"
  else
    omi_note "removed mihomo and its unit; kept $OMIHOMO_DATA_DIR"
  fi
  omi_note "run 'omarchy plugin remove omihomo' to take the widget off the bar"
}

core_repair() {
  omi_require_core
  if ! omi_privileged "$OMIHOMO_ROOT/libexec/root.sh" repair /usr/bin/mihomo; then
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

# Enabling a unit that was never written fails, so autostart writes it the same
# way `core start` does rather than assuming the core has been started once.
core_autostart() {
  omi_require_core
  case ${1:-} in
    on)
      [[ -f $OMIHOMO_UNIT_FILE ]] || omi_write_unit
      omi_systemctl --user daemon-reload
      omi_systemctl --user enable "$OMIHOMO_UNIT"
      ;;
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
  uninstall) core_uninstall "${2:-}" ;;
  repair) core_repair ;;
  start) core_start ;;
  stop) core_stop ;;
  restart) core_restart ;;
  autostart) core_autostart "${2:-}" ;;
  version) core_version ;;
  *) omi_error "unknown core command" 1 ;;
esac
