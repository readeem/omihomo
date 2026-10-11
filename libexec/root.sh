#!/usr/bin/env bash

set -euo pipefail

hook=/usr/share/libalpm/hooks/omihomo-permissions.hook
# ADR-0002 granted file capabilities instead. Installs from that era still have
# its hook and its xattr on the binary, so every grant path clears both.
legacy_hook=/usr/share/libalpm/hooks/omihomo-capabilities.hook
# setuid root, executable only by root and by the one account that installed
# Omihomo: no other local account can run the core as root (ADR-0006).
mode=4750

owner=${SUDO_UID:-${PKEXEC_UID:-}}

# The one grant, run by repair and by the pacman hook after every upgrade. It
# drops the setuid bit first and sets it last, so a step that fails leaves a
# core nobody runs as root rather than one others can. lib/common.sh checks the
# hook for grant_version, so bumping it sends existing installs to repair.
grant_version=2
grant_command="/usr/bin/chmod 0750 /usr/bin/mihomo && /usr/bin/setfacl -b /usr/bin/mihomo && /usr/bin/chown root:root /usr/bin/mihomo && /usr/bin/setfacl -m u:$owner:rx /usr/bin/mihomo && /usr/bin/chmod $mode /usr/bin/mihomo"

require_owner() {
  [[ $owner =~ ^[0-9]+$ && $owner != 0 ]] || { printf 'run this through sudo or pkexec as the owning user\n' >&2; exit 1; }
}

install_hook() {
  /usr/bin/rm -f "$legacy_hook"
  /usr/bin/install -Dm644 /dev/stdin "$hook" <<EOF
# omihomo grant $grant_version
[Trigger]
Operation = Install
Operation = Upgrade
Type = Path
Target = usr/bin/mihomo

[Action]
When = PostTransaction
Exec = /usr/bin/sh -c '$grant_command'
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
  if [[ -x /usr/bin/setcap ]]; then
    /usr/bin/setcap -r /usr/bin/mihomo 2>/dev/null || true
  fi
  /usr/bin/sh -c "$grant_command"
}

case ${1:-} in
  prepare)
    require_owner
    conflict=()
    case ${2:-} in
      "") ;;
      --replace-yq) conflict=(--ask 4) ;;
      *) printf 'unknown prepare option\n' >&2; exit 1 ;;
    esac
    /usr/bin/pacman -S --needed --noconfirm "${conflict[@]}" go-yq jq nftables curl
    install_hook
    if [[ -e /usr/bin/mihomo ]]; then
      grant
    fi
    ;;
  repair)
    require_owner
    [[ ${2:-/usr/bin/mihomo} == /usr/bin/mihomo && -e /usr/bin/mihomo ]] || { printf 'mihomo binary not found\n' >&2; exit 1; }
    grant
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
      /usr/bin/setfacl -b /usr/bin/mihomo || true
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
