#!/usr/bin/env bash

set -euo pipefail

hook=/usr/share/libalpm/hooks/omihomo-capabilities.hook
capabilities='cap_net_admin,cap_net_raw,cap_net_bind_service=ep'

install_hook() {
  /usr/bin/install -Dm644 /dev/stdin "$hook" <<EOF
[Trigger]
Operation = Install
Operation = Upgrade
Type = Path
Target = usr/bin/mihomo

[Action]
When = PostTransaction
Exec = /usr/bin/setcap "$capabilities" /usr/bin/mihomo
EOF
}

case ${1:-} in
  prepare)
    conflict=()
    case ${2:-} in
      "") ;;
      --replace-yq) conflict=(--ask 4) ;;
      *) printf 'unknown prepare option\n' >&2; exit 1 ;;
    esac
    /usr/bin/pacman -S --needed --noconfirm "${conflict[@]}" go-yq jq libcap nftables curl
    install_hook
    if [[ -x /usr/bin/mihomo ]]; then
      /usr/bin/setcap "$capabilities" /usr/bin/mihomo
    fi
    ;;
  repair)
    binary=${2:-/usr/bin/mihomo}
    [[ $binary == /usr/bin/mihomo && -x $binary ]] || { printf 'mihomo binary not found\n' >&2; exit 1; }
    /usr/bin/setcap "$capabilities" "$binary"
    install_hook
    ;;
  uninstall)
    if [[ -e /usr/bin/mihomo ]]; then
      /usr/bin/setcap -r /usr/bin/mihomo || true
    fi
    /usr/bin/rm -f "$hook"
    ;;
  *)
    printf 'unknown privileged action\n' >&2
    exit 1
    ;;
esac
