#!/usr/bin/env bash

set -euo pipefail

hook=/usr/share/libalpm/hooks/omihomo-permissions.hook
# ADR-0002 granted file capabilities instead. Installs from that era still have
# its hook and its xattr on the binary, so every grant path clears both.
legacy_hook=/usr/share/libalpm/hooks/omihomo-capabilities.hook
# setuid plus setgid root, which is what Koala Clash's packaging applies.
mode=6755

install_hook() {
  /usr/bin/rm -f "$legacy_hook"
  /usr/bin/install -Dm644 /dev/stdin "$hook" <<EOF
[Trigger]
Operation = Install
Operation = Upgrade
Type = Path
Target = usr/bin/mihomo

[Action]
When = PostTransaction
Exec = /usr/bin/chmod $mode /usr/bin/mihomo
EOF
}

# A binary that carries file capabilities computes its privileges from them
# rather than from the setuid bit, so they go before the bits go on.
grant() {
  local binary=$1
  if [[ -x /usr/bin/setcap ]]; then
    /usr/bin/setcap -r "$binary" 2>/dev/null || true
  fi
  /usr/bin/chown root:root "$binary"
  /usr/bin/chmod "$mode" "$binary"
}

case ${1:-} in
  prepare)
    conflict=()
    case ${2:-} in
      "") ;;
      --replace-yq) conflict=(--ask 4) ;;
      *) printf 'unknown prepare option\n' >&2; exit 1 ;;
    esac
    /usr/bin/pacman -S --needed --noconfirm "${conflict[@]}" go-yq jq nftables curl
    install_hook
    if [[ -x /usr/bin/mihomo ]]; then
      grant /usr/bin/mihomo
    fi
    ;;
  repair)
    binary=${2:-/usr/bin/mihomo}
    [[ $binary == /usr/bin/mihomo && -x $binary ]] || { printf 'mihomo binary not found\n' >&2; exit 1; }
    grant "$binary"
    install_hook
    ;;
  uninstall)
    if [[ -e /usr/bin/mihomo ]]; then
      /usr/bin/chmod 755 /usr/bin/mihomo || true
      if [[ -x /usr/bin/setcap ]]; then
        /usr/bin/setcap -r /usr/bin/mihomo 2>/dev/null || true
      fi
    fi
    /usr/bin/rm -f "$hook" "$legacy_hook"
    ;;
  *)
    printf 'unknown privileged action\n' >&2
    exit 1
    ;;
esac
