#!/usr/bin/env bash

set -euo pipefail

source "$(dirname "$0")/test_helper.sh"

test_status_reports_not_installed() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN=/nonexistent

  local output
  output=$(run_cli status)
  assert_json_field "$output" state not-installed
  assert_json_field "$output" active_subscription null
  [[ $(jq -e 'has("ip") and has("latency") and has("download") and has("upload") and has("config") and has("uptime")' <<<"$output") == true ]] || fail "status shape is missing #5 fields"
}

test_subscription_add_and_list_are_flat_json() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add work https://example.test/subscription/work
  local output
  output=$(run_cli sub list)
  [[ $(jq 'length' <<<"$output") == 1 ]] || fail "expected one subscription"
  assert_json_field "$(jq '.[0]' <<<"$output")" name work
  assert_json_field "$(jq '.[0]' <<<"$output")" upload 10
  [[ $(jq -e '.[0] | has("userinfo") | not' <<<"$output") == true ]] || fail "list must stay flat"
}

test_invalid_subscription_update_keeps_previous_cache() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add work https://example.test/subscription/work
  local cache="$XDG_DATA_HOME/omihomo/cache/work.yaml"
  local before
  before=$(<"$cache")
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='INVALID'
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli sub update work 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 20
  assert_eq "$(<"$cache")" "$before"
  assert_file_contains "$stderr" '"code":20'
}

test_subscription_fetch_failure_uses_exit_code_21() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_FETCH_FAIL=yes
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli sub add work https://example.test/subscription/work 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 21
  assert_file_contains "$stderr" '"code":21'
}

test_activation_merges_override_and_reloads_active_core() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add work https://example.test/subscription/work
  cat >"$XDG_DATA_HOME/omihomo/override.yaml" <<'EOF'
config:
  external-controller: 127.0.0.1:9090
  secret: test-secret
  profile:
    store-selected: true
omihomo:
  primary-group: Auto
rules:
  prepend:
    - DOMAIN-SUFFIX,example.com,DIRECT
  append: []
  filter: []
EOF
  run_cli sub activate work
  [[ $(cat "$XDG_DATA_HOME/omihomo/active") == work ]] || fail "active subscription not written"
  assert_file_contains "$XDG_DATA_HOME/omihomo/runtime.yaml" 'external-controller: 127.0.0.1:9090'
  assert_file_contains "$XDG_DATA_HOME/omihomo/runtime.yaml" 'DOMAIN-SUFFIX,example.com,DIRECT'
  if grep -q '^omihomo:' "$XDG_DATA_HOME/omihomo/runtime.yaml"; then
    fail "runtime YAML must not contain Omihomo metadata"
  fi
}

test_active_update_reloads_without_restarting_the_unit() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add work https://example.test/subscription/work
  run_cli sub activate work
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='proxies: []'
  run_cli sub update work
  assert_file_contains "$TEST_ROOT/curl-put.log" '/configs?force=true'
}

test_failed_active_activation_keeps_previous_subscription() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add work https://example.test/subscription/work
  run_cli sub add backup https://example.test/subscription/backup
  run_cli sub activate work
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_API_UNREACHABLE=yes
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli sub activate backup 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 12
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/active")" work
  assert_file_contains "$stderr" '"code":12'
}

test_running_core_rejects_removing_active_subscription() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add work https://example.test/subscription/work
  run_cli sub activate work
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli sub remove work 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 11
  assert_file_contains "$stderr" '"code":11'
  [[ -f "$XDG_DATA_HOME/omihomo/cache/work.yaml" ]] || fail "active cache was removed"
}

test_rule_filter_and_append_are_applied_to_runtime() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_SUBSCRIPTION_BODY=$'rules:\n  - DOMAIN-SUFFIX,old.example,DIRECT\n  - DOMAIN-SUFFIX,keep.example,DIRECT'

  run_cli sub add work https://example.test/subscription/work
  run_cli rule raw 'DOMAIN-SUFFIX,first.example,DIRECT'
  run_cli rule raw append 'DOMAIN-SUFFIX,last.example,DIRECT'
  run_cli rule raw filter 'DOMAIN-SUFFIX,old.example,DIRECT'
  run_cli sub activate work
  local runtime="$XDG_DATA_HOME/omihomo/runtime.yaml"
  assert_file_contains "$runtime" 'DOMAIN-SUFFIX,first.example,DIRECT'
  assert_file_contains "$runtime" 'DOMAIN-SUFFIX,keep.example,DIRECT'
  assert_file_contains "$runtime" 'DOMAIN-SUFFIX,last.example,DIRECT'
  if grep -q 'DOMAIN-SUFFIX,old.example,DIRECT' "$runtime"; then
    fail "filtered subscription rule survived merge"
  fi
}

test_rule_validation_and_indexed_removal() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli rule add DOMAIN-SUFFIX example.com DIRECT
  run_cli rule raw 'PROCESS-NAME,Firefox,REJECT'
  local output
  output=$(run_cli rule list)
  [[ $(jq 'length' <<<"$output") == 2 ]] || fail "expected two rules"
  set +e
  run_cli rule add IP-CIDR not-a-cidr DIRECT >/dev/null 2>"$TEST_ROOT/error"
  local status=$?
  set -e
  assert_eq "$status" 1
  assert_file_contains "$TEST_ROOT/error" 'invalid IP-CIDR'
  run_cli rule remove 1
  [[ $(jq 'length' <<<"$(run_cli rule list)") == 1 ]] || fail "rule was not removed"
}

test_pretty_is_applied_by_dispatcher() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add work https://example.test/subscription/work
  local output
  output=$(run_cli --pretty sub list)
  assert_file_contains <(printf '%s\n' "$output") 'name'
  [[ $output != '['* ]] || fail "pretty output should not be JSON array"
}

test_status_exit_code_is_always_zero() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  local output
  output=$(run_cli status)
  assert_json_field "$output" state starting
}

test_status_reports_stopped_for_installed_core() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  local output
  output=$(run_cli status)
  assert_json_field "$output" state stopped
}

test_status_reports_degraded_when_tun_device_is_missing() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TUN_DEVICE=omihomo-test-device-that-does-not-exist
  mkdir -p "$XDG_DATA_HOME/omihomo"
  cat >"$XDG_DATA_HOME/omihomo/override.yaml" <<'EOF'
config:
  external-controller: 127.0.0.1:9090
  secret: test-secret
  tun:
    enable: true
omihomo:
  primary-group: Auto
rules:
  prepend: []
  append: []
  filter: []
EOF
  local output
  output=$(run_cli status)
  assert_json_field "$output" state degraded
}

test_error_code_10_is_used_when_core_is_missing() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN=/nonexistent
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli sub update missing 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 10
  assert_file_contains "$stderr" '"code":10'
}

test_core_start_requires_an_active_subscription() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli core start 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 13
  assert_file_contains "$stderr" '"code":13'
}

test_turning_tun_on_requires_an_active_unit() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli set tun on 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 11
  assert_file_contains "$stderr" '"code":11'
}

test_active_override_change_reports_unreachable_controller() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add work https://example.test/subscription/work
  run_cli sub activate work
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_API_UNREACHABLE=yes
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli set mode direct 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 12
  assert_file_contains "$stderr" '"code":12'
}

tests=(
  test_status_reports_not_installed
  test_subscription_add_and_list_are_flat_json
  test_invalid_subscription_update_keeps_previous_cache
  test_subscription_fetch_failure_uses_exit_code_21
  test_activation_merges_override_and_reloads_active_core
  test_active_update_reloads_without_restarting_the_unit
  test_failed_active_activation_keeps_previous_subscription
  test_running_core_rejects_removing_active_subscription
  test_rule_filter_and_append_are_applied_to_runtime
  test_rule_validation_and_indexed_removal
  test_pretty_is_applied_by_dispatcher
  test_status_exit_code_is_always_zero
  test_status_reports_stopped_for_installed_core
  test_status_reports_degraded_when_tun_device_is_missing
  test_error_code_10_is_used_when_core_is_missing
  test_core_start_requires_an_active_subscription
  test_turning_tun_on_requires_an_active_unit
  test_active_override_change_reports_unreachable_controller
)

for test_name in "${tests[@]}"; do
  printf 'TEST %s\n' "$test_name"
  "$test_name"
done
printf 'PASS %d tests\n' "${#tests[@]}"
