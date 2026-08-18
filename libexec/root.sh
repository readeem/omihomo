#!/usr/bin/env bash

set -euo pipefail

hook=/usr/share/libalpm/hooks/omihomo-capabilities.hook
capabilities='cap_net_admin,cap_net_raw,cap_net_bind_service=ep'

case ${1:-} in
  install|repair)
    binary=${2:-/usr/bin/mihomo}
    [[ $binary == /usr/bin/mihomo && -x $binary ]] || { printf 'mihomo binary not found\n' >&2; exit 1; }
    /usr/bin/setcap "$capabilities" "$binary"
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
