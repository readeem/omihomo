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
  [[ $(jq -e 'has("ip") and has("latency") and has("download") and has("upload") and has("config") and has("uptime") and has("permissions_ok")' <<<"$output") == true ]] || fail "status shape is missing fields"
}

test_subscription_add_and_list_are_flat_json() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
  local output
  output=$(run_cli sub list)
  [[ $(jq 'length' <<<"$output") == 1 ]] || fail "expected one subscription"
  assert_json_field "$(jq '.[0]' <<<"$output")" name work
  assert_json_field "$(jq '.[0]' <<<"$output")" upload 10
  [[ $(jq -e '.[0] | has("userinfo") | not' <<<"$output") == true ]] || fail "list must stay flat"
}

# The subscription server picks the format from the User-Agent: an unrecognised
# client is answered with a base64 share-link list that `mihomo -t` rejects.
test_subscription_fetch_asks_as_a_clash_client() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
  assert_eq "$(head -n 1 "$TEST_ROOT/curl-agent.log")" clash.meta
}

test_subscription_takes_its_name_from_the_server() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE="base64:$(printf 'Omega Hangout' | base64)"

  run_cli sub add https://example.test/subscription/work
  assert_json_field "$(run_cli sub list | jq '.[0]')" name "Omega Hangout"
  [[ -f "$XDG_DATA_HOME/omihomo/cache/Omega Hangout.yaml" ]] || fail "cache is not named after the subscription"
}

# A header is the server's to write, so a title that would escape the cache
# directory or break a CLI argument has to be neutralised before it is stored.
test_unsafe_and_missing_titles_still_produce_a_usable_name() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE='../../etc/passwd'

  run_cli sub add https://example.test/subscription/work
  assert_json_field "$(run_cli sub list | jq '.[0]')" name "etc passwd"

  unset OMIHOMO_TEST_TITLE
  export OMIHOMO_TEST_NO_TITLE=yes
  run_cli sub add https://example.test/subscription/backup
  assert_json_field "$(run_cli sub list | jq '.[1]')" name example.test
}

test_repeated_titles_are_suffixed_and_repeated_urls_are_refused() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=shared

  run_cli sub add https://example.test/subscription/work
  run_cli sub add https://example.test/subscription/backup
  local output
  output=$(run_cli sub list)
  assert_eq "$(jq -r '[.[].name] | join(",")' <<<"$output")" "shared,shared 2"

  local status
  set +e
  run_cli sub add https://example.test/subscription/work 2>/dev/null
  status=$?
  set -e
  assert_eq "$status" 1
  assert_eq "$(run_cli sub list | jq 'length')" 2
}

test_raw_subscription_is_cached_as_a_provider_wrapper() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=raw
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/raw-plain.txt"
  local url='https://example.test/subscription/raw?token=a%2Bb&tag=x'

  run_cli sub add "$url"

  local cache="$XDG_DATA_HOME/omihomo/cache/raw.yaml"
  assert_eq "$(yq -r '."proxy-providers".subscription.type' "$cache")" http
  assert_eq "$(yq -r '."proxy-providers".subscription.url' "$cache")" "$url"
  assert_eq "$(yq -r '."proxy-providers".subscription.path' "$cache")" './providers/d314e84c465c28453ac705094c504c6004c0bfc32741962e9939760a7f3484ec.yaml'
  assert_eq "$(yq -r '."proxy-groups"[0].name' "$cache")" Proxy
  assert_eq "$(yq -r '.rules[0]' "$cache")" MATCH,Proxy
  assert_json_field "$(run_cli sub list | jq '.[0]')" upload 10
  assert_eq "$(wc -l <"$TEST_ROOT/curl-agent.log")" 1
}

test_base64_subscription_is_cached_as_a_provider_wrapper() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=encoded
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/raw-base64.txt"

  run_cli sub add https://example.test/subscription/encoded

  local cache="$XDG_DATA_HOME/omihomo/cache/encoded.yaml"
  assert_eq "$(yq -r '."proxy-providers".subscription.type' "$cache")" http
  assert_eq "$(yq -r '."proxy-groups"[0].use[0]' "$cache")" subscription
}

test_full_config_subscription_is_not_wrapped() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=full
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/full-config.yaml"

  run_cli sub add https://example.test/subscription/full

  local cache="$XDG_DATA_HOME/omihomo/cache/full.yaml"
  cmp -s "$REPO_ROOT/tests/fixtures/full-config.yaml" "$cache" || fail "full config changed during import"
  assert_eq "$(yq -r '."proxy-groups"[0].name' "$cache")" 'Local select'
  assert_eq "$(yq -r 'has("proxy-providers")' "$cache")" false
}

test_provider_yaml_subscription_is_not_wrapped() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=provider
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/provider.yaml"

  run_cli sub add https://example.test/subscription/provider

  local cache="$XDG_DATA_HOME/omihomo/cache/provider.yaml"
  cmp -s "$REPO_ROOT/tests/fixtures/provider.yaml" "$cache" || fail "provider YAML was wrapped"
}

test_garbage_subscription_leaves_no_partial_state() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=garbage
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/garbage.txt"
  local stderr status
  stderr=$(mktemp)

  set +e
  run_cli sub add https://example.test/subscription/garbage 2>"$stderr"
  status=$?
  set -e

  assert_eq "$status" 20
  assert_eq "$(run_cli sub list | jq 'length')" 0
  assert_eq "$(find "$XDG_DATA_HOME/omihomo/cache" -type f | wc -l)" 0
  if find "$XDG_DATA_HOME/omihomo" -maxdepth 1 -name '.*.??????' | grep -q .; then
    fail "subscription import left temporary files"
  fi
  assert_file_contains "$stderr" 'mihomo rejected the raw subscription: convert v2ray subscribe error: format invalid'
  if grep -Fq password "$stderr"; then
    fail "raw provider error exposed credentials"
  fi
}

test_invalid_full_config_has_a_distinct_validation_error() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='proxies: INVALID'
  local stderr status
  stderr=$(mktemp)

  set +e
  run_cli sub add https://example.test/subscription/invalid-full 2>"$stderr"
  status=$?
  set -e

  assert_eq "$status" 20
  assert_file_contains "$stderr" 'mihomo rejected the YAML'
  assert_eq "$(run_cli sub list | jq 'length')" 0
}

test_raw_subscriptions_with_the_same_title_use_independent_provider_paths() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_TITLE=shared
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/raw-plain.txt"

  run_cli sub add https://example.test/subscription/one
  run_cli sub add https://example.test/subscription/two

  local first="$XDG_DATA_HOME/omihomo/cache/shared.yaml"
  local second="$XDG_DATA_HOME/omihomo/cache/shared 2.yaml"
  [[ $(yq -r '."proxy-providers".subscription.path' "$first") != \
    "$(yq -r '."proxy-providers".subscription.path' "$second")" ]] ||
    fail "same-title subscriptions share a provider cache path"
}

test_invalid_subscription_update_keeps_previous_cache() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
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
  run_cli sub add https://example.test/subscription/work 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 21
  assert_file_contains "$stderr" '"code":21'
}

test_activation_merges_override_and_reloads_active_core() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
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

test_runtime_routes_global_through_the_primary_group() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_SUBSCRIPTION_BODY=$'proxies:\n  - name: Tokyo\n    type: direct\nproxy-groups:\n  - name: VPN\n    type: select\n    proxies: [Tokyo]\n  - name: Backup\n    type: select\n    proxies: [Tokyo]'

  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  local runtime="$XDG_DATA_HOME/omihomo/runtime.yaml"
  assert_eq "$(yq -r '."proxy-groups"[0].name' "$runtime")" GLOBAL
  assert_eq "$(yq -r '."proxy-groups"[0].proxies[0]' "$runtime")" VPN

  run_cli set group Backup
  assert_eq "$(yq -r '."proxy-groups"[0].proxies[0]' "$runtime")" Backup

  run_cli set mode global
  assert_eq "$(yq -r '.mode' "$runtime")" global
}

test_global_mode_requires_a_subscription_group() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli set mode global 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 1
  assert_file_contains "$stderr" 'global mode needs a subscription group'
}

test_global_cannot_be_set_as_primary() {
  setup_test
  trap teardown_test RETURN
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli set group GLOBAL 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 1
  assert_file_contains "$stderr" 'GLOBAL is managed by Omihomo'
}

test_active_update_reloads_without_restarting_the_unit() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='proxies: []'
  run_cli sub update work
  assert_file_contains "$TEST_ROOT/curl-put.log" '/configs?force=true'
}

test_failed_active_raw_update_keeps_previous_cache_and_runtime() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  local cache="$XDG_DATA_HOME/omihomo/cache/work.yaml"
  local runtime="$XDG_DATA_HOME/omihomo/runtime.yaml"
  local cache_before runtime_before subscriptions_before stderr status
  cache_before=$(<"$cache")
  runtime_before=$(<"$runtime")
  subscriptions_before=$(<"$XDG_DATA_HOME/omihomo/subscriptions.json")
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_API_UNREACHABLE=yes
  export OMIHOMO_TEST_SUBSCRIPTION_FIXTURE="$REPO_ROOT/tests/fixtures/raw-plain.txt"
  stderr=$(mktemp)

  set +e
  run_cli sub update work 2>"$stderr"
  status=$?
  set -e

  assert_eq "$status" 12
  assert_eq "$(<"$cache")" "$cache_before"
  assert_eq "$(<"$runtime")" "$runtime_before"
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/subscriptions.json")" "$subscriptions_before"
}

test_failed_active_activation_keeps_previous_subscription() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add https://example.test/subscription/work
  run_cli sub add https://example.test/subscription/backup
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
  run_cli sub add https://example.test/subscription/work
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

  run_cli sub add https://example.test/subscription/work
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

  run_cli sub add https://example.test/subscription/work
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

test_status_reports_whether_the_core_can_run_tun() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  assert_eq "$(run_cli status | jq -r '.permissions_ok')" false
  export OMIHOMO_TEST_PERMISSIONS=yes
  assert_eq "$(run_cli status | jq -r '.permissions_ok')" true
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

# mihomo names the interface after `tun.device`, defaulting to `Meta`, so the
# device probe has to read the config rather than assume a name.
test_status_reads_the_tun_device_name_from_the_runtime_config() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
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
  cat >"$XDG_DATA_HOME/omihomo/runtime.yaml" <<'EOF'
tun:
  enable: true
  device: lo
EOF
  local output
  output=$(run_cli status)
  assert_json_field "$output" state on
}

test_status_reports_autostart_state() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  local output
  output=$(run_cli status)
  assert_eq "$(jq -r '.autostart_enabled' <<<"$output")" false

  export OMIHOMO_TEST_UNIT_ENABLED=yes
  output=$(run_cli status)
  assert_eq "$(jq -r '.autostart_enabled' <<<"$output")" true
}

# Autostart is the one path that can enable the unit before it has ever been
# started, so it has to write the unit file itself.
test_autostart_writes_the_unit_before_enabling_it() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"

  run_cli core autostart on
  [[ -f $XDG_CONFIG_HOME/systemd/user/omihomo.service ]] || fail "autostart did not write the unit"
  assert_file_contains "$TEST_ROOT/systemctl.log" "enable omihomo.service"
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

test_turning_tun_on_starts_an_inactive_unit_without_privilege() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_PERMISSIONS=yes

  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  run_cli set tun on

  assert_eq "$(yq -r '.config.tun.enable' "$XDG_DATA_HOME/omihomo/override.yaml")" true
  assert_file_contains "$TEST_ROOT/systemctl.log" "start omihomo.service"
  [[ ! -e $TEST_ROOT/pkexec.log ]] || fail "TUN enable invoked pkexec"
  [[ ! -e $TEST_ROOT/sudo.log ]] || fail "TUN enable invoked sudo"
}

test_turning_tun_on_reloads_an_active_unit_without_privilege() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_PERMISSIONS=yes
  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  export OMIHOMO_TEST_UNIT_ACTIVE=yes

  run_cli set tun on

  assert_file_contains "$TEST_ROOT/curl-put.log" "/configs?force=true"
  [[ ! -e $TEST_ROOT/pkexec.log ]] || fail "TUN reload invoked pkexec"
  [[ ! -e $TEST_ROOT/sudo.log ]] || fail "TUN reload invoked sudo"
}

test_turning_tun_on_requires_an_active_subscription() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_PERMISSIONS=yes
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli set tun on 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 13
  assert_file_contains "$stderr" '"code":13'
}

test_turning_tun_on_without_root_permissions_does_not_prompt() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add https://example.test/subscription/work
  run_cli sub activate work
  local stderr status
  stderr=$(mktemp)

  set +e
  run_cli set tun on 2>"$stderr"
  status=$?
  set -e

  assert_eq "$status" 14
  assert_file_contains "$stderr" '"code":14'
  [[ ! -e $TEST_ROOT/pkexec.log ]] || fail "missing permissions invoked pkexec"
  [[ ! -e $TEST_ROOT/sudo.log ]] || fail "missing permissions invoked sudo"
}

test_install_has_one_privileged_setup_boundary() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_PERMISSIONS=yes

  script -qec "$REPO_ROOT/bin/omihomo core install" /dev/null >/dev/null

  assert_eq "$(wc -l <"$TEST_ROOT/sudo.log")" 1
  assert_file_contains "$TEST_ROOT/sudo.log" "root.sh prepare"
  assert_file_contains "$TEST_ROOT/yay.log" "--sudoloop"
}

test_active_override_change_reports_unreachable_controller() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add https://example.test/subscription/work
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

# The panel parses stderr as JSON; a human reading a terminal gets a plain line.
test_pretty_errors_are_plain_text() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN=/nonexistent
  local stderr status
  stderr=$(mktemp)
  set +e
  run_cli --pretty core start 2>"$stderr"
  status=$?
  set -e
  assert_eq "$status" 10
  assert_file_contains "$stderr" 'omihomo: mihomo is not installed (exit 10)'
}

# Uninstall has to take every artifact with it: unit, permissions, launcher
# symlink, package, and state.
test_uninstall_removes_every_artifact() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_PKGS="mihomo-bin"
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add https://example.com/subscription >/dev/null
  mkdir -p "$HOME/.local/bin"
  ln -sfn "$REPO_ROOT/bin/omihomo" "$HOME/.local/bin/omihomo"
  local unit="$XDG_CONFIG_HOME/systemd/user/omihomo.service"
  mkdir -p "$(dirname "$unit")"
  printf '[Service]\n' >"$unit"

  run_cli core uninstall

  [[ ! -e $unit ]] || fail "unit file survived uninstall"
  [[ ! -e $HOME/.local/bin/omihomo ]] || fail "launcher symlink survived uninstall"
  [[ ! -d $XDG_DATA_HOME/omihomo ]] || fail "state directory survived uninstall"
  assert_file_contains "$TEST_ROOT/yay.log" "-Rns --noconfirm mihomo-bin"
  assert_file_contains "$TEST_ROOT/pkexec.log" "uninstall"
}

# --keep-data is the same removal with the subscriptions left in place.
test_uninstall_can_keep_state() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub add https://example.com/subscription >/dev/null

  run_cli core uninstall --keep-data

  [[ -f $XDG_DATA_HOME/omihomo/subscriptions.json ]] || fail "--keep-data dropped the state"
  [[ ! -f $TEST_ROOT/yay.log ]] || fail "yay ran for a package that is not installed"
}

tests=(
  test_status_reports_not_installed
  test_subscription_add_and_list_are_flat_json
  test_subscription_fetch_asks_as_a_clash_client
  test_subscription_takes_its_name_from_the_server
  test_unsafe_and_missing_titles_still_produce_a_usable_name
  test_repeated_titles_are_suffixed_and_repeated_urls_are_refused
  test_raw_subscription_is_cached_as_a_provider_wrapper
  test_base64_subscription_is_cached_as_a_provider_wrapper
  test_full_config_subscription_is_not_wrapped
  test_provider_yaml_subscription_is_not_wrapped
  test_garbage_subscription_leaves_no_partial_state
  test_invalid_full_config_has_a_distinct_validation_error
  test_raw_subscriptions_with_the_same_title_use_independent_provider_paths
  test_invalid_subscription_update_keeps_previous_cache
  test_subscription_fetch_failure_uses_exit_code_21
  test_activation_merges_override_and_reloads_active_core
  test_runtime_routes_global_through_the_primary_group
  test_global_mode_requires_a_subscription_group
  test_global_cannot_be_set_as_primary
  test_active_update_reloads_without_restarting_the_unit
  test_failed_active_raw_update_keeps_previous_cache_and_runtime
  test_failed_active_activation_keeps_previous_subscription
  test_running_core_rejects_removing_active_subscription
  test_rule_filter_and_append_are_applied_to_runtime
  test_rule_validation_and_indexed_removal
  test_pretty_is_applied_by_dispatcher
  test_status_exit_code_is_always_zero
  test_status_reports_stopped_for_installed_core
  test_status_reports_whether_the_core_can_run_tun
  test_status_reports_degraded_when_tun_device_is_missing
  test_status_reads_the_tun_device_name_from_the_runtime_config
  test_status_reports_autostart_state
  test_autostart_writes_the_unit_before_enabling_it
  test_error_code_10_is_used_when_core_is_missing
  test_pretty_errors_are_plain_text
  test_uninstall_removes_every_artifact
  test_uninstall_can_keep_state
  test_core_start_requires_an_active_subscription
  test_turning_tun_on_starts_an_inactive_unit_without_privilege
  test_turning_tun_on_reloads_an_active_unit_without_privilege
  test_turning_tun_on_requires_an_active_subscription
  test_turning_tun_on_without_root_permissions_does_not_prompt
  test_install_has_one_privileged_setup_boundary
  test_active_override_change_reports_unreachable_controller
)

for test_name in "${tests[@]}"; do
  printf 'TEST %s\n' "$test_name"
  "$test_name"
done
printf 'PASS %d tests\n' "${#tests[@]}"
