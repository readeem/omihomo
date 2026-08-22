#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
REAL_PATH=${PATH}

setup_test() {
  unset OMIHOMO_TEST_TITLE OMIHOMO_TEST_NO_TITLE OMIHOMO_TEST_SUBSCRIPTION_BODY OMIHOMO_TEST_SUBSCRIPTION_FIXTURE OMIHOMO_TEST_UNIT_ACTIVE OMIHOMO_TEST_UNIT_ENABLED OMIHOMO_TEST_ACTIVE_ENTER OMIHOMO_TEST_API_UNREACHABLE OMIHOMO_TEST_FETCH_FAIL OMIHOMO_TEST_PKGS OMIHOMO_TEST_PERMISSIONS OMIHOMO_TUN_DEVICE OMIHOMO_TAILSCALED_BIN OMIHOMO_TEST_INVOCATION OMIHOMO_TEST_JOURNAL
  TEST_ROOT=$(mktemp -d)
  export TEST_ROOT
  export HOME="$TEST_ROOT/home"
  export XDG_DATA_HOME="$TEST_ROOT/data"
  export XDG_CONFIG_HOME="$TEST_ROOT/config"
  export PATH="$TEST_ROOT/bin:$REAL_PATH"
  export OMIHOMO_YQ=${OMIHOMO_TEST_YQ:-yq}
  mkdir -p "$HOME" "$TEST_ROOT/bin" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME"

  cat >"$TEST_ROOT/bin/mihomo" <<'EOF'
#!/usr/bin/env bash
if [[ ${1:-} == -t ]]; then
  file=${3:-}
  grep -q INVALID "$file" && exit 1
  exit 0
fi
if [[ ${1:-} == -d ]]; then
  file=${4:-}
  cp "$file" "$TEST_ROOT/mihomo-preflight.yaml"
  provider=$(yq -r '."proxy-providers".subscription.path' "$file")
  cp "$provider" "$TEST_ROOT/mihomo-preflight-body"
  if grep -Eq 'INVALID|this is not a subscription' "$provider"; then
    printf 'level=error msg="convert v2ray subscribe error: format invalid near trojan://password@example.test:443"\n' >&2
  fi
  trap 'exit 0' TERM
  while :; do sleep 0.05; done
fi
if [[ ${1:-} == -v ]]; then
  printf 'mihomo version test\n'
  exit 0
fi
exit 0
EOF
  chmod +x "$TEST_ROOT/bin/mihomo"

  cat >"$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=
headers=
url=
agent=
method=GET
while (($#)); do
  case $1 in
    -o) output=$2; shift 2 ;;
    -A) agent=$2; shift 2 ;;
    -D) headers=$2; shift 2 ;;
    -X) method=$2; shift 2 ;;
    --unix-socket|--max-time) shift 2 ;;
    --data|--data-raw|--data-binary|--json) shift 2 ;;
    -w) shift 2 ;;
    -s|-S|-f|-L|-N|-k|-H) shift; [[ $1 == *:* ]] && shift || true ;;
    http*) url=$1; shift ;;
    *) shift ;;
  esac
done
if [[ $method == PUT ]]; then
  printf '%s\n' "$url" >>"$TEST_ROOT/curl-put.log"
  exit 0
fi
if [[ $url == http://localhost/providers/proxies/subscription ]]; then
  [[ -f $TEST_ROOT/mihomo-preflight-body ]] || exit 1
  if grep -Eq 'INVALID|this is not a subscription' "$TEST_ROOT/mihomo-preflight-body"; then
    printf '{"proxies":[]}\n'
  else
    printf '{"proxies":[{"name":"Tokyo"},{"name":"Berlin"}]}\n'
  fi
  exit 0
fi
if [[ ${OMIHOMO_TEST_API_UNREACHABLE:-no} == yes && $url == *127.0.0.1* ]]; then
  exit 1
fi
if [[ $url == *subscription* ]]; then
  printf '%s\n' "$agent" >>"$TEST_ROOT/curl-agent.log"
  [[ ${OMIHOMO_TEST_FETCH_FAIL:-no} == yes ]] && exit 1
  if [[ -n ${OMIHOMO_TEST_SUBSCRIPTION_FIXTURE:-} ]]; then
    cp "$OMIHOMO_TEST_SUBSCRIPTION_FIXTURE" "$output"
  else
    printf '%s\n' "${OMIHOMO_TEST_SUBSCRIPTION_BODY:-proxies: []}" >"$output"
  fi
  if [[ -n ${headers:-} ]]; then
    # The subscription names itself, so the stub titles it after the last path
    # segment: /subscription/work answers to "work".
    printf 'subscription-userinfo: upload=10; download=20; total=100; expire=200\r\n' >"$headers"
    if [[ ${OMIHOMO_TEST_NO_TITLE:-no} != yes ]]; then
      printf 'profile-title: %s\r\n' "${OMIHOMO_TEST_TITLE:-${url##*/}}" >>"$headers"
    fi
  fi
  exit 0
fi
if [[ $url == *127.0.0.1* ]]; then
  printf '{"version":"test"}\n'
  exit 0
fi
exit 1
EOF
  chmod +x "$TEST_ROOT/bin/curl"

  cat >"$TEST_ROOT/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
while [[ ${1:-} == --user ]]; do shift; done
case ${1:-} in
  is-active) [[ ${OMIHOMO_TEST_UNIT_ACTIVE:-no} == yes ]] ;;
  is-enabled) [[ ${OMIHOMO_TEST_UNIT_ENABLED:-no} == yes ]] ;;
  show)
    case " $* " in
      *--property=InvocationID*) printf '%s\n' "${OMIHOMO_TEST_INVOCATION:-}" ;;
      *) printf '%s\n' "${OMIHOMO_TEST_ACTIVE_ENTER:-}" ;;
    esac
    ;;
  *) printf '%s\n' "$*" >>"$TEST_ROOT/systemctl.log" ;;
esac
EOF
  chmod +x "$TEST_ROOT/bin/systemctl"

  # The unit's log, as `omihomo status` reads it to explain a TUN failure.
  # OMIHOMO_TEST_JOURNAL is the whole of what the invocation logged.
  cat >"$TEST_ROOT/bin/journalctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "${OMIHOMO_TEST_JOURNAL:-}"
EOF
  chmod +x "$TEST_ROOT/bin/journalctl"

  # Package and privilege stubs, so uninstall can be exercised without touching
  # the machine. OMIHOMO_TEST_PKGS lists what pacman should report as installed.
  cat >"$TEST_ROOT/bin/pacman" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == -Qq ]]; then
  [[ " ${OMIHOMO_TEST_PKGS:-} " == *" ${2:-} "* ]] || exit 1
  printf '%s\n' "$2"
  exit 0
fi
printf '%s\n' "$*" >>"$TEST_ROOT/pacman.log"
EOF
  chmod +x "$TEST_ROOT/bin/pacman"

  cat >"$TEST_ROOT/bin/yay" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_ROOT/yay.log"
EOF
  chmod +x "$TEST_ROOT/bin/yay"

  # Which of the two the CLI reaches for depends on whether stdin is a terminal,
  # so both also append to one log a test can assert on without caring.
  cat >"$TEST_ROOT/bin/pkexec" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_ROOT/pkexec.log"
printf '%s\n' "$*" >>"$TEST_ROOT/privileged.log"
EOF
  chmod +x "$TEST_ROOT/bin/pkexec"

  cat >"$TEST_ROOT/bin/sudo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_ROOT/sudo.log"
printf '%s\n' "$*" >>"$TEST_ROOT/privileged.log"
EOF
  chmod +x "$TEST_ROOT/bin/sudo"

  # A test cannot own a root:root setuid file, so the ownership and mode read
  # is the seam: OMIHOMO_TEST_PERMISSIONS decides what the core looks like.
  cat >"$TEST_ROOT/bin/stat" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${OMIHOMO_TEST_PERMISSIONS:-no} == yes ]]; then
  printf '0 0 6755\n'
else
  printf '1000 1000 755\n'
fi
EOF
  chmod +x "$TEST_ROOT/bin/stat"
}

teardown_test() {
  rm -rf "$TEST_ROOT"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  return 1
}

assert_eq() {
  [[ $1 == "$2" ]] || fail "expected <$2>, got <$1>"
}

assert_file_contains() {
  grep -Fq -- "$2" "$1" || fail "expected $1 to contain <$2>"
}

assert_json_field() {
  local json=$1 field=$2 expected=$3 actual
  actual=$(jq -r --arg field "$field" '.[$field] // "null"' <<<"$json")
  assert_eq "$actual" "$expected"
}

run_cli() {
  "$REPO_ROOT/bin/omihomo" "$@"
}
