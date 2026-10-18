#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if ! command -v bwrap >/dev/null || ! bwrap --unshare-all --die-with-parent --ro-bind / / -- /usr/bin/true 2>/dev/null; then
  if [[ ${OMIHOMO_REQUIRE_PACKAGE_SANDBOX:-0} == 1 ]]; then
    printf 'Privileged adapter tests require Bubblewrap and isolated user namespaces\n' >&2
    exit 1
  fi
  printf 'SKIP privileged adapter tests: isolated user namespaces are unavailable\n'
  exit
fi
work=$(mktemp -d)
trap 'rm -r -- "$work"' EXIT
mkdir "$work/fixture"
cat > "$work/pacman" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  -Q) printf "error: package 'omihomo-bin' was not found\n" >&2; exit 1 ;;
  -Qip) [[ $# == 3 && $2 == -- ]]; cat /tmp/omihomo-package-fixture/metadata ;;
  -U)
    [[ $# == 4 && $2 == --needed && $3 == -- ]]
    [[ $(stat -c '%a:%u' "$4") == 600:0 && $(stat -c '%a:%u' "${4%/*}") == 700:0 ]]
    cat "$4" > /tmp/omihomo-package-fixture/package
    [[ ! -e /tmp/omihomo-package-fixture/fail ]]
    ;;
  *) exit 99 ;;
esac
SCRIPT
chmod +x "$work/pacman"
cd "$root"
go test -c -o "$work/test-update" ./internal/update
bwrap --unshare-all --die-with-parent --new-session --uid 0 --gid 0 \
  --ro-bind / / --tmpfs /tmp --proc /proc --dev /dev \
  --ro-bind "$work/pacman" /usr/bin/pacman --bind "$work/fixture" /tmp/omihomo-package-fixture \
  --setenv OMIHOMO_ISOLATED_PACKAGE_TEST 1 \
  --ro-bind "$work/test-update" /tmp/test-update \
  -- /tmp/test-update -test.run '^TestAuthenticatedPacmanBoundary$' -test.v

cat > "$work/systemctl" <<'SCRIPT'
#!/bin/sh
printf '%s\n' "$*" >> /tmp/systemctl.log
SCRIPT
chmod +x "$work/systemctl"
cat > "$work/test-remove" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
umask 077
if [[ $1 != offline ]]; then mkdir -p /run/systemd/system; fi
mkdir -p /etc/systemd/system/tailscaled.service.d
printf 'keep\n' > /etc/systemd/system/tailscaled.service.d/other.conf
if [[ $1 != no-dropin ]]; then
  printf 'owned\n' > /etc/systemd/system/tailscaled.service.d/omihomo.conf
fi
source /tmp/omihomo.install
pre_remove
[[ ! -e /etc/systemd/system/tailscaled.service.d/omihomo.conf \
  && $(cat /etc/systemd/system/tailscaled.service.d/other.conf) == keep ]]
grep -qx 'disable omihomo.socket omihomo.service' /tmp/systemctl.log
if [[ $1 == offline ]]; then
  if grep -Eq 'stop|daemon-reload|try-restart' /tmp/systemctl.log; then
    printf 'FAIL offline removal attempted live systemd operations\n' >&2
    exit 1
  fi
else
  grep -qx 'stop omihomo.service omihomo.socket' /tmp/systemctl.log
  if [[ $1 == no-dropin ]]; then
    if grep -Eq 'daemon-reload|try-restart' /tmp/systemctl.log; then
      printf 'FAIL removal restarted Tailscale without an owned drop-in\n' >&2
      exit 1
    fi
  else
    grep -qx 'daemon-reload' /tmp/systemctl.log
    grep -qx 'try-restart tailscaled.service' /tmp/systemctl.log
  fi
fi
SCRIPT
for mode in offline online no-dropin; do
  bwrap --unshare-all --die-with-parent --new-session --uid 0 --gid 0 \
    --ro-bind / / --tmpfs /tmp --tmpfs /run --tmpfs /etc/systemd/system --proc /proc --dev /dev \
    --ro-bind "$work/systemctl" /usr/bin/systemctl \
    --ro-bind "$root/packaging/aur/omihomo.install" /tmp/omihomo.install \
    --ro-bind "$work/test-remove" /tmp/test-remove \
    -- /usr/bin/bash /tmp/test-remove "$mode"
done
printf 'PASS package removal cleans its Tailscale drop-in online and offline\n'
