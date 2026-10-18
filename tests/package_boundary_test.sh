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
