#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/test_helper.sh"

setup_refresh_test() {
  unset OMIHOMO_TEST_FETCH_FAIL_URL
  setup_test
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_NOW=1735732800 # 2025-01-01 12:00 UTC
  cat >"$TEST_ROOT/bin/date" <<'EOF'
#!/usr/bin/env bash
if [[ $* == '+%s' ]]; then
  printf '%s\n' "$OMIHOMO_TEST_NOW"
else
  /usr/bin/date -u -d "@$OMIHOMO_TEST_NOW" +%Y-%m-%dT%H:%M:%SZ
fi
EOF
  chmod +x "$TEST_ROOT/bin/date"
  run_cli sub add https://example.test/subscription/work
}

age_subscription() {
  local name=${1:-work} timestamp=${2:-2025-01-01T06:00:00Z} file="$XDG_DATA_HOME/omihomo/subscriptions.json"
  jq --arg name "$name" --arg updated "$timestamp" \
    'map(if .name == $name then .updated_at = $updated else . end)' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
}

fetch_count() { wc -l <"$TEST_ROOT/curl-agent.log"; }

setup_connection_test() {
  setup_refresh_test
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='proxies:
  - {name: First, type: direct}
  - {name: "Chosen / Москва", type: direct}
proxy-groups:
  - name: "Group / Выбор"
    type: select
    proxies: [First, "Chosen / Москва"]
rules: ["MATCH,Group / Выбор"]'
  run_cli sub update work
  run_cli set mode global
  yq -i '.config.tun.enable = true' "$XDG_DATA_HOME/omihomo/override.yaml"
  run_cli sub activate work
  cp "$XDG_DATA_HOME/omihomo/runtime.yaml" "$TEST_ROOT/live.yaml"
  printf '{"Group / Выбор":{"type":"Selector","all":["First","Chosen / Москва"],"now":"Chosen / Москва"}}\n' >"$TEST_ROOT/proxies.json"
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  age_subscription
}

test_elapsed_time_survives_sessions_and_refreshes_at_six_hours() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription work 2025-01-01T07:00:00Z
  run_cli sub update-due
  run_cli sub update-due
  assert_eq "$(fetch_count)" 1
  export OMIHOMO_TEST_NOW=1735736400 # 13:00 UTC; six hours have elapsed
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  assert_eq "$(run_cli sub list | jq -r '.[0].updated_at')" 2025-01-01T13:00:00Z
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
}

test_manual_refresh_resets_the_deadline() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  run_cli sub update work
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
}

test_failed_download_keeps_state_and_retries() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  local before runtime cache status
  before=$(run_cli sub list)
  runtime=$(<"$XDG_DATA_HOME/omihomo/runtime.yaml")
  cache=$(<"$XDG_DATA_HOME/omihomo/cache/work.yaml")
  export OMIHOMO_TEST_FETCH_FAIL=yes
  set +e
  run_cli sub update-due 2>"$TEST_ROOT/error"
  status=$?
  set -e
  assert_eq "$status" 1
  assert_eq "$(run_cli sub list)" "$before"
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/runtime.yaml")" "$runtime"
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/cache/work.yaml")" "$cache"
  export OMIHOMO_TEST_FETCH_FAIL=no
  run_cli sub update-due
  assert_eq "$(run_cli sub list | jq -r '.[0].updated_at')" 2025-01-01T12:00:00Z
}

test_refresh_does_not_start_a_stopped_core() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  local override
  override=$(<"$XDG_DATA_HOME/omihomo/override.yaml")
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/override.yaml")" "$override"
  [[ ! -e $TEST_ROOT/systemctl.log && ! -e $TEST_ROOT/curl-put.log ]] || fail "refresh changed the core lifecycle"
}

test_refresh_restores_mode_tun_and_group_selection() {
  setup_connection_test
  trap teardown_test RETURN
  run_cli sub update-due
  assert_eq "$(yq -r '.mode' "$TEST_ROOT/live.yaml")" global
  assert_eq "$(yq -r '.tun.enable' "$TEST_ROOT/live.yaml")" true
  assert_eq "$(jq -r '.["Group / Выбор"].now' "$TEST_ROOT/proxies.json")" 'Chosen / Москва'
  [[ ! -e $TEST_ROOT/systemctl.log ]] || fail "refresh restarted the core"
}

test_refresh_keeps_tun_disabled_and_direct_mode() {
  setup_connection_test
  trap teardown_test RETURN
  yq -i '.config.mode = "direct" | .config.tun.enable = false' "$XDG_DATA_HOME/omihomo/override.yaml"
  yq -i '.mode = "direct" | .tun.enable = false' "$XDG_DATA_HOME/omihomo/runtime.yaml"
  yq -i '.mode = "direct" | .tun.enable = false' "$TEST_ROOT/live.yaml"
  run_cli sub update-due
  assert_eq "$(yq -r '.mode' "$TEST_ROOT/live.yaml")" direct
  assert_eq "$(yq -r '.tun.enable' "$TEST_ROOT/live.yaml")" false
}

test_removed_config_uses_a_valid_fallback() {
  setup_connection_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='proxies: [{name: First, type: direct}]
proxy-groups: [{name: "Group / Выбор", type: select, proxies: [First]}]
rules: ["MATCH,Group / Выбор"]'
  run_cli sub update-due
  assert_eq "$(jq -r '.["Group / Выбор"].now' "$TEST_ROOT/proxies.json")" First
}

test_failed_selection_restore_rolls_back() {
  setup_connection_test
  trap teardown_test RETURN
  local runtime before status
  runtime=$(<"$XDG_DATA_HOME/omihomo/runtime.yaml")
  before=$(run_cli sub list)
  # Load 1 succeeds; the selection request is rejected once; rollback succeeds.
  export OMIHOMO_TEST_PUT_FAIL=2
  set +e
  run_cli sub update-due 2>"$TEST_ROOT/error"
  status=$?
  set -e
  assert_eq "$status" 1
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/runtime.yaml")" "$runtime"
  assert_eq "$(run_cli sub list)" "$before"
  assert_eq "$(jq -r '.["Group / Выбор"].now' "$TEST_ROOT/proxies.json")" 'Chosen / Москва'
  assert_eq "$(yq -r '.tun.enable' "$TEST_ROOT/live.yaml")" true
}

test_concurrent_checks_only_refresh_once() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  run_cli sub update-due &
  local first=$!
  run_cli sub update-due &
  local second=$!
  wait "$first"
  wait "$second"
  assert_eq "$(fetch_count)" 2
}

test_each_subscription_has_its_own_deadline() {
  setup_refresh_test
  trap teardown_test RETURN
  run_cli sub add https://example.test/subscription/second
  age_subscription second
  run_cli sub update-due
  assert_eq "$(fetch_count)" 3
  assert_eq "$(run_cli sub list | jq -r '.[] | select(.name == "second") | .updated_at')" 2025-01-01T12:00:00Z
  assert_eq "$(cat "$XDG_DATA_HOME/omihomo/active")" work
}

test_a_failed_subscription_does_not_block_other_due_updates() {
  setup_refresh_test
  trap teardown_test RETURN
  run_cli sub add https://example.test/subscription/second
  age_subscription
  age_subscription second
  export OMIHOMO_TEST_FETCH_FAIL_URL=https://example.test/subscription/work
  local status
  set +e
  run_cli sub update-due 2>"$TEST_ROOT/error"
  status=$?
  set -e
  assert_eq "$status" 1
  assert_eq "$(run_cli sub list | jq -r '.[] | select(.name == "work") | .updated_at')" 2025-01-01T06:00:00Z
  assert_eq "$(run_cli sub list | jq -r '.[] | select(.name == "second") | .updated_at')" 2025-01-01T12:00:00Z
}

test_empty_install_does_not_create_state() {
  setup_test
  trap teardown_test RETURN
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  run_cli sub update-due
  [[ ! -d $XDG_DATA_HOME/omihomo ]] || fail "timer created unrequested state"
}

test_missing_core_does_not_attempt_a_refresh() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  export OMIHOMO_MIHOMO_BIN=/nonexistent
  run_cli sub update-due
  assert_eq "$(fetch_count)" 1
}

test_sync_installs_timer_for_existing_users_once() {
  setup_refresh_test
  trap teardown_test RETURN
  mkdir -p "$HOME/.local/bin"
  ln -s "$REPO_ROOT/bin/omihomo" "$HOME/.local/bin/omihomo"
  run_cli core sync
  local directory="$XDG_CONFIG_HOME/systemd/user"
  assert_file_contains "$directory/omihomo-subscription-update.timer" 'Persistent=true'
  assert_file_contains "$directory/omihomo-subscription-update.service" 'sub update-due'
  assert_file_contains "$TEST_ROOT/systemctl.log" 'enable --now omihomo-subscription-update.timer'
  local operations
  operations=$(<"$TEST_ROOT/systemctl.log")
  run_cli core sync
  assert_eq "$(<"$TEST_ROOT/systemctl.log")" "$operations"
}

tests=(
  test_elapsed_time_survives_sessions_and_refreshes_at_six_hours
  test_manual_refresh_resets_the_deadline
  test_failed_download_keeps_state_and_retries
  test_refresh_does_not_start_a_stopped_core
  test_refresh_restores_mode_tun_and_group_selection
  test_refresh_keeps_tun_disabled_and_direct_mode
  test_removed_config_uses_a_valid_fallback
  test_failed_selection_restore_rolls_back
  test_concurrent_checks_only_refresh_once
  test_each_subscription_has_its_own_deadline
  test_a_failed_subscription_does_not_block_other_due_updates
  test_empty_install_does_not_create_state
  test_missing_core_does_not_attempt_a_refresh
  test_sync_installs_timer_for_existing_users_once
)
for test_name in "${tests[@]}"; do
  printf 'TEST %s\n' "$test_name"
  "$test_name"
done
printf 'PASS %d automatic refresh tests\n' "${#tests[@]}"
