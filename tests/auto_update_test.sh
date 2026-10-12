#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/test_helper.sh"

setup_refresh_test() {
  unset OMIHOMO_TEST_FETCH_FAIL_URL OMIHOMO_TEST_FETCH_FAIL_PREFIX OMIHOMO_TEST_FETCH_FAIL_DELAY
  setup_test
  export OMIHOMO_TEST_INTERVAL_HEADER='Profile-Update-Interval: 6'
  export OMIHOMO_MIHOMO_BIN="$TEST_ROOT/bin/mihomo"
  export OMIHOMO_TEST_NOW=1735732800 # 2025-01-01 12:00 UTC
  advance_test_clock 0
  run_cli sub add <<<https://example.test/subscription/work
}

age_subscription() {
  local name=${1:-work} timestamp=${2:-2025-01-01T06:00:00Z} file="$XDG_DATA_HOME/omihomo/subscriptions.json"
  jq --arg name "$name" --arg updated "$timestamp" \
    'map(if .name == $name then .updated_at = $updated else . end)' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
  local url digest request updated interval
  url=$(jq -r --arg name "$name" '.[] | select(.name == $name) | .url' "$file")
  digest=$(printf '%s' "$url" | sha256sum)
  request="$XDG_DATA_HOME/omihomo/requests/${digest%% *}.json"
  if [[ -f $request ]]; then
    updated=$(/usr/bin/date -u -d "$timestamp" +%s)
    interval=$(jq -r --arg name "$name" '.[] | select(.name == $name) | .update_interval_hours // 12' "$file")
    jq --argjson updated "$updated" --argjson interval "$interval" '
      .attempted_at = $updated | .automatic_after = ($updated + $interval * 3600) |
      .manual_after = ($updated + 60)' "$request" >"$request.tmp"
    mv "$request.tmp" "$request"
  fi
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
  advance_test_clock 60
  run_cli sub update work
  run_cli set mode global
  yq -i '.config.tun.enable = true' "$XDG_DATA_HOME/omihomo/override.yaml"
  run_cli sub activate work
  cp "$XDG_DATA_HOME/omihomo/runtime.yaml" "$TEST_ROOT/live.yaml"
  printf '{"Group / Выбор":{"type":"Selector","all":["First","Chosen / Москва"],"now":"Chosen / Москва"}}\n' >"$TEST_ROOT/proxies.json"
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_NOW=1735732800
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
  advance_test_clock 60
  run_cli sub update work
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
}

test_startup_only_refreshes_due_subscriptions() {
  setup_refresh_test
  trap teardown_test RETURN
  run_cli sub add <<<https://example.test/subscription/second
  export OMIHOMO_TEST_UNIT_ENABLED=yes OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_NOW=1735732860
  run_cli sub update-on-start
  assert_eq "$(fetch_count)" 2
  age_subscription
  run_cli sub update-on-start
  assert_eq "$(fetch_count)" 3
  run_cli sub update-on-start
  assert_eq "$(fetch_count)" 3
  [[ ! -e $TEST_ROOT/systemctl.log ]] || fail "startup refresh changed the core lifecycle"
}

test_startup_skips_refresh_when_autostart_is_disabled_or_core_is_stopped() {
  setup_refresh_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_UNIT_ACTIVE=yes
  run_cli sub update-on-start
  export OMIHOMO_TEST_UNIT_ACTIVE=no OMIHOMO_TEST_UNIT_ENABLED=yes
  run_cli sub update-on-start
  assert_eq "$(fetch_count)" 1
}

# Exit 12 is the one failure the startup unit restarts on.
test_startup_asks_to_be_retried_when_the_core_never_becomes_ready() {
  setup_refresh_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_UNIT_ENABLED=yes OMIHOMO_TEST_UNIT_ACTIVE=yes OMIHOMO_TEST_API_UNREACHABLE=yes
  export OMIHOMO_STARTUP_READY_TIMEOUT=1
  local status=0
  run_cli sub update-on-start 2>/dev/null || status=$?
  assert_eq "$status" 12
  assert_eq "$(fetch_count)" 1
}

test_startup_failure_keeps_cache_and_refreshes_other_subscriptions() {
  setup_refresh_test
  trap teardown_test RETURN
  run_cli sub add <<<https://example.test/subscription/second
  local cache runtime status
  cache=$(<"$XDG_DATA_HOME/omihomo/cache/work.yaml")
  runtime=$(<"$XDG_DATA_HOME/omihomo/runtime.yaml")
  export OMIHOMO_TEST_UNIT_ENABLED=yes OMIHOMO_TEST_UNIT_ACTIVE=yes
  export OMIHOMO_TEST_NOW=1735732860
  export OMIHOMO_TEST_FETCH_FAIL_URL=https://example.test/subscription/work
  age_subscription
  age_subscription second
  set +e
  run_cli sub update-on-start 2>"$TEST_ROOT/error"
  status=$?
  set -e
  assert_eq "$status" 1
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/cache/work.yaml")" "$cache"
  assert_eq "$(<"$XDG_DATA_HOME/omihomo/runtime.yaml")" "$runtime"
  assert_eq "$(run_cli sub list | jq -r '.[] | select(.name == "work") | .updated_at')" 2025-01-01T06:00:00Z
  assert_eq "$(run_cli sub list | jq -r '.[] | select(.name == "second") | .updated_at')" 2025-01-01T12:01:00Z
}

test_startup_refresh_restores_connection_state() {
  setup_connection_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_UNIT_ENABLED=yes
  run_cli sub update-on-start
  assert_eq "$(yq -r '.mode' "$TEST_ROOT/live.yaml")" global
  assert_eq "$(yq -r '.tun.enable' "$TEST_ROOT/live.yaml")" true
  assert_eq "$(jq -r '.["Group / Выбор"].now' "$TEST_ROOT/proxies.json")" 'Chosen / Москва'
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
  assert_eq "$(run_cli sub list)" "$before"
  advance_test_clock 21600
  run_cli sub update-due
  assert_eq "$(run_cli sub list | jq -r '.[0].updated_at')" 2025-01-01T18:00:00Z
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
  run_cli sub add <<<https://example.test/subscription/second
  age_subscription second
  run_cli sub update-due
  assert_eq "$(fetch_count)" 3
  assert_eq "$(run_cli sub list | jq -r '.[] | select(.name == "second") | .updated_at')" 2025-01-01T12:00:00Z
  assert_eq "$(cat "$XDG_DATA_HOME/omihomo/active")" work
}

test_a_failed_subscription_does_not_block_other_due_updates() {
  setup_refresh_test
  trap teardown_test RETURN
  run_cli sub add <<<https://example.test/subscription/second
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
  printf '[Service]\nExecStart=/bin/true\n' >"$XDG_CONFIG_HOME/systemd/user/omihomo.service"
  run_cli core sync
  local directory="$XDG_CONFIG_HOME/systemd/user"
  assert_file_contains "$directory/omihomo-subscription-update.timer" 'Persistent=true'
  assert_file_contains "$directory/omihomo-subscription-update.service" 'sub update-due'
  assert_file_contains "$directory/omihomo.service" 'Wants=omihomo-subscription-startup.service'
  assert_file_contains "$directory/omihomo-subscription-startup.service" 'After=omihomo.service'
  assert_file_contains "$directory/omihomo-subscription-startup.service" 'sub update-on-start'
  assert_file_contains "$TEST_ROOT/systemctl.log" 'enable --now omihomo-subscription-update.timer'
  local operations
  operations=$(<"$TEST_ROOT/systemctl.log")
  run_cli core sync
  assert_eq "$(<"$TEST_ROOT/systemctl.log")" "$operations"
}

test_import_persists_the_provider_interval() {
  setup_refresh_test
  trap teardown_test RETURN
  assert_eq "$(jq -r '.[0].update_interval_hours' "$XDG_DATA_HOME/omihomo/subscriptions.json")" 6
  export OMIHOMO_TEST_INTERVAL_HEADER=$'pRoFiLe-UpDaTe-InTeRvAl:\t 24  '
  run_cli sub add <<<https://example.test/subscription/second
  assert_eq "$(jq -r '.[1].update_interval_hours' "$XDG_DATA_HOME/omihomo/subscriptions.json")" 24
  age_subscription second 2024-12-31T13:00:00Z # 23 hours old
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  export OMIHOMO_TEST_NOW=1735736400 # 13:00 UTC; 24 hours elapsed
  run_cli sub update-due
  assert_eq "$(fetch_count)" 3
}

test_absent_and_invalid_intervals_default_to_twelve_hours() {
  setup_refresh_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_INTERVAL_HEADER=''
  run_cli sub add <<<https://example.test/subscription/second
  assert_eq "$(jq -r '.[1].update_interval_hours' "$XDG_DATA_HOME/omihomo/subscriptions.json")" 12
  local header
  for header in '' 'Profile-Update-Interval:' 'Profile-Update-Interval: 0' 'Profile-Update-Interval: -6' \
    'Profile-Update-Interval: 1.5' 'Profile-Update-Interval: invalid' \
    'Profile-Update-Interval: 12e0' 'Profile-Update-Interval: 999999999999999999999'; do
    export OMIHOMO_TEST_INTERVAL_HEADER="$header"
    advance_test_clock 60
  run_cli sub update work
    assert_eq "$(jq -r '.[0].update_interval_hours' "$XDG_DATA_HOME/omihomo/subscriptions.json")" 12
  done
  age_subscription work 2025-01-01T01:00:00Z # 11 hours old
  run_cli sub update-due
  local before
  before=$(fetch_count)
  export OMIHOMO_TEST_NOW=1735736400 # 13:00 UTC; 12 hours elapsed
  run_cli sub update-due
  assert_eq "$(fetch_count)" "$((before + 1))"
}

test_legacy_subscriptions_use_twelve_hours_and_catch_overdue_checks() {
  setup_refresh_test
  trap teardown_test RETURN
  local file="$XDG_DATA_HOME/omihomo/subscriptions.json"
  jq 'map(del(.update_interval_hours))' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
  age_subscription work 2025-01-01T01:00:00Z
  run_cli sub update-due
  assert_eq "$(fetch_count)" 1
  export OMIHOMO_TEST_NOW=1735779600 # 2025-01-02 01:00 UTC; overdue after offline time
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  assert_eq "$(jq -r '.[0].updated_at' "$file")" 2025-01-02T01:00:00Z
}

test_successful_refresh_commits_interval_changes_and_failed_reload_keeps_them() {
  setup_connection_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_INTERVAL_HEADER='Profile-Update-Interval: 24'
  export OMIHOMO_TEST_PUT_FAIL='1'
  local file="$XDG_DATA_HOME/omihomo/subscriptions.json" before status
  before=$(<"$file")
  set +e
  run_cli sub update work 2>"$TEST_ROOT/error"
  status=$?
  set -e
  assert_eq "$status" 12
  assert_eq "$(<"$file")" "$before"
  unset OMIHOMO_TEST_PUT_FAIL
  advance_test_clock 86400
  run_cli sub update work
  assert_eq "$(jq -r '.[0].update_interval_hours' "$file")" 24
  local downloads
  downloads=$(fetch_count)
  advance_test_clock 86399
  run_cli sub update-due
  assert_eq "$(fetch_count)" "$downloads"
  advance_test_clock 1
  run_cli sub update-due
  assert_eq "$(fetch_count)" "$((downloads + 1))"
}

test_slow_failures_do_not_starve_the_healthy_tail() {
  setup_refresh_test
  trap teardown_test RETURN
  local file="$XDG_DATA_HOME/omihomo/subscriptions.json"
  # Ten slow failures precede the healthy active subscription, in both runs.
  jq '.[0] as $healthy | [range(10) | {
    name: ("slow-" + tostring), url: ("https://example.test/subscription/slow-" + tostring),
    updated_at: "2025-01-01T06:00:00Z", update_interval_hours: 6, userinfo: {}
  }] + [$healthy]' "$file" >"$file.tmp"
  mv "$file.tmp" "$file"
  mkdir -p "$HOME/.local/bin"
  ln -s "$REPO_ROOT/bin/omihomo" "$HOME/.local/bin/omihomo"
  run_cli core sync
  local service="$XDG_CONFIG_HOME/systemd/user/omihomo-subscription-update.service" batch_budget round status
  batch_budget=$(sed -n 's/^TimeoutStartSec=//p' "$service")
  export OMIHOMO_TEST_FETCH_FAIL_PREFIX=https://example.test/subscription/slow-
  # Scale 60-second downloads and the old 10-minute limit by 1/1000.
  export OMIHOMO_TEST_FETCH_FAIL_DELAY=0.06
  for round in 1 2; do
    export OMIHOMO_TEST_NOW=$((1735732800 + (round - 1) * 21600))
    age_subscription work
    set +e
    if [[ $batch_budget == infinity || -z $batch_budget ]]; then
      timeout 10s "$REPO_ROOT/bin/omihomo" sub update-due 2>"$TEST_ROOT/error"
    else
      timeout 0.6s "$REPO_ROOT/bin/omihomo" sub update-due 2>"$TEST_ROOT/error"
    fi
    status=$?
    set -e
    assert_eq "$status" 1
    assert_eq "$(jq -r '.[] | select(.name == "work") | .updated_at' "$file")" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  done
  assert_eq "$(grep -c '^clash.meta$' "$TEST_ROOT/curl-agent.log")" 23
}

test_failures_wait_for_the_interval_across_all_request_paths() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  export OMIHOMO_TEST_FETCH_FAIL=yes
  local before status
  before=$(fetch_count)
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "download should fail"
  advance_test_clock 60
  run_cli sub update-due
  export OMIHOMO_TEST_UNIT_ENABLED=yes OMIHOMO_TEST_UNIT_ACTIVE=yes
  run_cli sub update-on-start
  status=0
  run_cli sub update work 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 22
  assert_eq "$(fetch_count)" "$((before + 1))"
  unset OMIHOMO_TEST_FETCH_FAIL
  advance_test_clock 21540
  run_cli sub update-due
  assert_eq "$(fetch_count)" "$((before + 2))"
}

test_retry_after_survives_removal_and_reimport() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  export OMIHOMO_TEST_HTTP_STATUS=429 OMIHOMO_TEST_RETRY_AFTER=86400
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "429 should fail"
  run_cli sub remove work
  advance_test_clock 43200
  local before status=0
  before=$(fetch_count)
  run_cli sub add <<<https://example.test/subscription/work 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 22
  assert_eq "$(fetch_count)" "$before"
  unset OMIHOMO_TEST_HTTP_STATUS OMIHOMO_TEST_RETRY_AFTER
  advance_test_clock 43200
  run_cli sub add <<<https://example.test/subscription/work
  assert_eq "$(fetch_count)" "$((before + 1))"
}

test_http_date_retry_after_is_respected() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  export OMIHOMO_TEST_HTTP_STATUS=503 OMIHOMO_TEST_RETRY_AFTER='Thu, 02 Jan 2025 12:00:00 GMT'
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "503 should fail"
  unset OMIHOMO_TEST_HTTP_STATUS OMIHOMO_TEST_RETRY_AFTER
  advance_test_clock 86399
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  advance_test_clock 1
  run_cli sub update-due
  assert_eq "$(fetch_count)" 3
}

test_numeric_retry_after_starts_when_the_response_arrives() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  mv "$TEST_ROOT/bin/date" "$TEST_ROOT/bin/fixed-date"
  cat >"$TEST_ROOT/bin/date" <<'EOF'
#!/usr/bin/env bash
if [[ $* == '+%s' && -f $TEST_ROOT/response-arrived ]]; then
  printf '%s\n' "$((OMIHOMO_TEST_NOW + 45))"
else
  exec "$TEST_ROOT/bin/fixed-date" "$@"
fi
EOF
  mv "$TEST_ROOT/bin/curl" "$TEST_ROOT/bin/fixture-curl"
  cat >"$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
status=0
"$TEST_ROOT/bin/fixture-curl" "$@" || status=$?
touch "$TEST_ROOT/response-arrived"
exit "$status"
EOF
  chmod +x "$TEST_ROOT/bin/date" "$TEST_ROOT/bin/curl"
  export OMIHOMO_TEST_HTTP_STATUS=429 OMIHOMO_TEST_RETRY_AFTER=86400
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "429 should fail"
  unset OMIHOMO_TEST_HTTP_STATUS OMIHOMO_TEST_RETRY_AFTER
  export OMIHOMO_TEST_NOW=$((1735732800 + 86400 - 1))
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  export OMIHOMO_TEST_NOW=$((1735732800 + 86400))
  run_cli sub update-due
  assert_eq "$(fetch_count)" 3
}

test_authorization_failures_pause_until_explicitly_resumed() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  export OMIHOMO_TEST_HTTP_STATUS=403
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "403 should fail"
  advance_test_clock 604800
  run_cli sub update-due
  local status=0
  run_cli sub update work 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 22
  assert_eq "$(fetch_count)" 2
  unset OMIHOMO_TEST_HTTP_STATUS
  run_cli sub resume work
  run_cli sub update work
  assert_eq "$(fetch_count)" 3
}

test_local_failures_do_not_redownload_on_the_next_tick() {
  setup_refresh_test
  trap teardown_test RETURN
  age_subscription
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='INVALID: true'
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "invalid YAML should fail"
  advance_test_clock 60
  run_cli sub update-due
  assert_eq "$(fetch_count)" 2
  export OMIHOMO_TEST_SUBSCRIPTION_BODY='proxies: []'
  export OMIHOMO_TEST_UNIT_ACTIVE=yes OMIHOMO_TEST_API_UNREACHABLE=yes
  advance_test_clock 21540
  run_cli sub update-due 2>"$TEST_ROOT/error" && fail "controller snapshot should fail"
  advance_test_clock 60
  run_cli sub update-due
  assert_eq "$(fetch_count)" 3
}

test_manual_requests_have_a_persistent_minimum_spacing() {
  setup_refresh_test
  trap teardown_test RETURN
  local status=0
  run_cli sub update work 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 22
  assert_eq "$(fetch_count)" 1
  advance_test_clock 60
  run_cli sub update work 2>"$TEST_ROOT/first-error" &
  local first=$!
  run_cli sub update work 2>"$TEST_ROOT/error" &
  local second=$!
  local first_status=0 second_status=0
  wait "$first" || first_status=$?
  wait "$second" || second_status=$?
  assert_eq "$((first_status + second_status))" 22
  assert_eq "$(fetch_count)" 2
}

test_failed_first_import_can_be_explicitly_resumed_without_bypassing_spacing() {
  setup_refresh_test
  trap teardown_test RETURN
  export OMIHOMO_TEST_HTTP_STATUS=401
  run_cli sub add <<<https://example.test/subscription/new 2>"$TEST_ROOT/error" && fail "401 should fail"
  run_cli sub resume-url <<<https://example.test/subscription/new
  unset OMIHOMO_TEST_HTTP_STATUS
  local status=0
  run_cli sub add <<<https://example.test/subscription/new 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 22
  assert_eq "$(fetch_count)" 2
  advance_test_clock 60
  run_cli sub add <<<https://example.test/subscription/new
  assert_eq "$(fetch_count)" 3
}

test_request_state_is_private_and_corruption_fails_closed() {
  setup_refresh_test
  trap teardown_test RETURN
  local request before status=0
  request=$(printf '%s\n' "$XDG_DATA_HOME"/omihomo/requests/*.json)
  assert_eq "$(/usr/bin/stat -c %a "${request%/*}")" 700
  assert_eq "$(/usr/bin/stat -c %a "$request")" 600
  ! grep -q 'https://' "$request" || fail "request state contains a URL"
  chmod 755 "${request%/*}"
  chmod 644 "$request"
  run_cli sub update work 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 22
  assert_eq "$(/usr/bin/stat -c %a "${request%/*}")" 700
  assert_eq "$(/usr/bin/stat -c %a "$request")" 600
  printf '{}\n' >"$request"
  advance_test_clock 86400
  before=$(fetch_count)
  status=0
  run_cli sub update-due 2>"$TEST_ROOT/error" || status=$?
  assert_eq "$status" 1
  run_cli sub update work 2>"$TEST_ROOT/error" || true
  assert_eq "$(fetch_count)" "$before"
}

tests=(
  test_failures_wait_for_the_interval_across_all_request_paths
  test_retry_after_survives_removal_and_reimport
  test_http_date_retry_after_is_respected
  test_numeric_retry_after_starts_when_the_response_arrives
  test_authorization_failures_pause_until_explicitly_resumed
  test_local_failures_do_not_redownload_on_the_next_tick
  test_manual_requests_have_a_persistent_minimum_spacing
  test_failed_first_import_can_be_explicitly_resumed_without_bypassing_spacing
  test_request_state_is_private_and_corruption_fails_closed
  test_startup_only_refreshes_due_subscriptions
  test_startup_skips_refresh_when_autostart_is_disabled_or_core_is_stopped
  test_startup_asks_to_be_retried_when_the_core_never_becomes_ready
  test_startup_failure_keeps_cache_and_refreshes_other_subscriptions
  test_startup_refresh_restores_connection_state
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
  test_import_persists_the_provider_interval
  test_absent_and_invalid_intervals_default_to_twelve_hours
  test_legacy_subscriptions_use_twelve_hours_and_catch_overdue_checks
  test_successful_refresh_commits_interval_changes_and_failed_reload_keeps_them
  test_slow_failures_do_not_starve_the_healthy_tail
)
if (($#)); then tests=("$@"); fi
for test_name in "${tests[@]}"; do
  printf 'TEST %s\n' "$test_name"
  "$test_name"
done
printf 'PASS %d automatic refresh tests\n' "${#tests[@]}"
