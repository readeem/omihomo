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

tailscale_dropin_dir=/etc/systemd/system/tailscaled.service.d
tailscale_dropin=$tailscale_dropin_dir/omihomo.conf

# tailscaled honours these for its control plane, its DERP relays, and its logs,
# which is the whole of what a proxy can carry: peer-to-peer WireGuard is UDP
# and never sees them. NO_PROXY keeps the tailnet's own address space, and the
# loopback the proxy itself lives on, out of the proxy.
#
# A drop-in rather than /etc/default/tailscaled, which the tailscale package
# owns: Omihomo owns this whole file, so turning the integration off is one rm.
write_tailscale_dropin() {
  local port=$1
  /usr/bin/install -Dm644 /dev/stdin "$tailscale_dropin" <<EOF
[Service]
Environment=HTTP_PROXY=http://127.0.0.1:$port
Environment=HTTPS_PROXY=http://127.0.0.1:$port
Environment=NO_PROXY=localhost,127.0.0.1,::1,100.64.0.0/10,fd7a:115c:a1e0::/48
EOF
}

remove_tailscale_dropin() {
  /usr/bin/rm -f "$tailscale_dropin"
  /usr/bin/rmdir "$tailscale_dropin_dir" 2>/dev/null || true
}

# `try-restart` is what makes tailscaled re-read its environment, and it leaves
# a tailscaled the user has stopped stopped.
reload_tailscaled() {
  /usr/bin/systemctl daemon-reload
  /usr/bin/systemctl try-restart tailscaled.service
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
  tailscale)
    case ${2:-} in
      on)
        port=${3:-}
        [[ $port =~ ^[0-9]+$ ]] || { printf 'tailscale port must be a number\n' >&2; exit 1; }
        write_tailscale_dropin "$port"
        ;;
      off) remove_tailscale_dropin ;;
      *) printf 'tailscale expects on or off\n' >&2; exit 1 ;;
    esac
    reload_tailscaled
    ;;
  uninstall)
    # Best effort: a machine that never had tailscaled has no unit to restart.
    remove_tailscale_dropin
    reload_tailscaled || true
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
