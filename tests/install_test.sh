#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
mkdir "$test_root/bin"
export INSTALL_TEST_LOG="$test_root/commands"

# Mock sudo itself so the absolute /usr/bin/omihomo is never executed.
cat > "$test_root/bin/yay" <<'MOCK'
#!/usr/bin/env bash
printf 'yay' >> "$INSTALL_TEST_LOG"
printf ' <%s>' "$@" >> "$INSTALL_TEST_LOG"
printf '\n' >> "$INSTALL_TEST_LOG"
exit "${INSTALL_TEST_YAY_EXIT:-0}"
MOCK
cat > "$test_root/bin/sudo" <<'MOCK'
#!/usr/bin/env bash
printf 'sudo' >> "$INSTALL_TEST_LOG"
printf ' <%s>' "$@" >> "$INSTALL_TEST_LOG"
printf '\n' >> "$INSTALL_TEST_LOG"
exit "${INSTALL_TEST_CLAIM_EXIT:-0}"
MOCK
chmod +x "$test_root/bin/yay" "$test_root/bin/sudo"
export PATH="$test_root/bin:$PATH"

check_install() {
  local yay_exit=$1 claim_exit=$2 expected_exit=$3 expected_commands=$4 actual_exit=0
  : > "$INSTALL_TEST_LOG"
  INSTALL_TEST_YAY_EXIT=$yay_exit INSTALL_TEST_CLAIM_EXIT=$claim_exit \
    "$project_root/bin/omihomo-install" || actual_exit=$?
  if [[ $actual_exit != "$expected_exit" ]]; then
    printf 'FAIL installer exit: expected %s, got %s\n' "$expected_exit" "$actual_exit" >&2
    exit 1
  fi
  if [[ $(cat "$INSTALL_TEST_LOG") != "$expected_commands" ]]; then
    printf 'FAIL installer commands (yay exit %s, claim exit %s):\n' "$yay_exit" "$claim_exit" >&2
    cat "$INSTALL_TEST_LOG" >&2
    exit 1
  fi
}

commands=$'yay <-S> <omihomo-bin>\nsudo </usr/bin/omihomo> <claim>'
check_install 0 0 0 "$commands"
check_install 42 0 42 'yay <-S> <omihomo-bin>'
check_install 0 43 43 "$commands"
printf 'PASS installer installs before claiming and propagates failures\n'
