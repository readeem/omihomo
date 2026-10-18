#!/usr/bin/env bash
set -euo pipefail
umask 077

project_root=$(cd "$(dirname "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -r -- "$test_root"' EXIT
mkdir -p "$test_root/bin" "$test_root/project/bin" "$test_root/project/internal/update"
cp "$project_root/bin/omihomo-install" "$test_root/project/bin/"
cp "$project_root/manifest.json" "$test_root/project/"
export INSTALL_TEST_ROOT="$test_root" INSTALL_TEST_LOG="$test_root/commands"
openssl genpkey -algorithm ED25519 -out "$test_root/private.pem"
openssl pkey -in "$test_root/private.pem" -pubout -out "$test_root/project/internal/update/release.pub"
cat > "$test_root/installer" <<'MOCK'
#!/usr/bin/env bash
printf 'package %s %s\n' "$1" "$2" >> "$INSTALL_TEST_LOG"
exit "${INSTALL_TEST_PACKAGE_EXIT:-0}"
MOCK
size=$(stat -c %s "$test_root/installer")
hash=$(sha256sum "$test_root/installer" | cut -d' ' -f1)
printf '%s\n' OMIHOMO-RELEASE-1 version=1.0.0 pkgrel=1 arch=x86_64 package-size=1 \
  "package-sha256=$(printf '%064d' 0)" "installer-size=$size" "installer-sha256=$hash" > "$test_root/release"
openssl pkeyutl -sign -rawin -inkey "$test_root/private.pem" -in "$test_root/release" -out "$test_root/signature"

cat > "$test_root/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
destination=''
while (($#)); do
  case "$1" in
    --output) destination=$2; shift 2 ;;
    *) url=$1; shift ;;
  esac
done
if [[ ${INSTALL_TEST_REDIRECT:-0} == 1 ]]; then
  printf '302\nhttps://example.org/untrusted'
  exit
fi
case "$url" in
  */omihomo-release.txt) cp "$INSTALL_TEST_ROOT/release" "$destination" ;;
  */omihomo-release.txt.sig) cp "$INSTALL_TEST_ROOT/signature" "$destination" ;;
  */omihomo-update-1.0.0-x86_64) cp "$INSTALL_TEST_ROOT/installer" "$destination" ;;
  *) exit 2 ;;
esac
printf '200\n'
MOCK
cat > "$test_root/bin/pacman" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ $* == '-Q omihomo-bin' ]] || exit 99
if [[ -n ${INSTALL_TEST_INSTALLED:-} ]]; then printf 'omihomo-bin %s\n' "$INSTALL_TEST_INSTALLED"
else printf "error: package 'omihomo-bin' was not found\n" >&2; exit 1; fi
MOCK
cat > "$test_root/bin/sudo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == /usr/bin/env ]]; then
  [[ $2 == -i && $3 == PATH=/usr/bin:/bin && $4 == HOME=/root && $5 == TERM=* ]] || exit 98
  shift 5
  # Run the authenticated shell under the ordinary test account. Its only
  # executable download is the fixture above; pacman and network are fakes.
  "$@"
else
  [[ $1 == /usr/bin/omihomo && $2 == claim ]] || exit 97
  printf 'claim\n' >> "$INSTALL_TEST_LOG"
  exit "${INSTALL_TEST_CLAIM_EXIT:-0}"
fi
MOCK
cat > "$test_root/bin/systemctl" <<'MOCK'
#!/usr/bin/env bash
printf 'systemctl %s\n' "$*" >> "$INSTALL_TEST_LOG"
MOCK
chmod +x "$test_root/bin/"* "$test_root/installer"
export PATH="$test_root/bin:$PATH"
installer="$test_root/project/bin/omihomo-install"

run_install() {
  local expected_exit=$1 mode=$2 expected_commands=$3 actual_exit=0
  : > "$INSTALL_TEST_LOG"
  "$installer" "$mode" >"$test_root/out" 2>"$test_root/err" || actual_exit=$?
  if [[ $actual_exit != "$expected_exit" || $(cat "$INSTALL_TEST_LOG") != "$expected_commands" ]]; then
    printf 'FAIL installer %s: expected exit %s, got %s\n' "$mode" "$expected_exit" "$actual_exit" >&2
    cat "$test_root/err" "$INSTALL_TEST_LOG" >&2
    exit 1
  fi
}

commands=$'package install 1.0.0\nclaim\nsystemctl --user daemon-reload\nsystemctl --user enable --now omihomo-apply.path omihomo-apply.service'
run_install 0 install "$commands"
INSTALL_TEST_PACKAGE_EXIT=42 run_install 42 install 'package install 1.0.0'
INSTALL_TEST_CLAIM_EXIT=43 run_install 43 install $'package install 1.0.0\nclaim'
INSTALL_TEST_INSTALLED=0.9.0-1 run_install 0 update 'package update 1.0.0'
INSTALL_TEST_INSTALLED=1.0.0-1 run_install 1 update ''
INSTALL_TEST_INSTALLED=2.0.0-1 run_install 1 update ''
run_install 1 update ''
INSTALL_TEST_INSTALLED=1.0.0-1 run_install 1 install ''
run_install 0 check ''
grep -q '"available":"1.0.0-1"' "$test_root/out"
INSTALL_TEST_REDIRECT=1 run_install 1 install ''
printf 'tampered\n' >> "$test_root/installer"
run_install 1 install ''
printf 'tampered\n' >> "$test_root/release"
run_install 1 install ''
run_install 1 check ''
grep -q '"error":' "$test_root/out"
printf 'PASS signed bootstrap, installation ordering, upgrade guards, redirects, tampering and failure propagation\n'
