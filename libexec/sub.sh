#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
source "$OMIHOMO_ROOT/lib/common.sh"

subscription_exists() {
  local name=$1
  jq -e --arg name "$name" 'any(.[]; .name == $name)' "$OMIHOMO_SUBSCRIPTIONS_FILE" >/dev/null
}

# The URL is what the user actually chose, so it is what "already added" means.
subscription_url_exists() {
  local url=$1
  jq -e --arg url "$url" 'any(.[]; .url == $url)' "$OMIHOMO_SUBSCRIPTIONS_FILE" >/dev/null
}

subscription_userinfo_json() {
  local headers=$1 line value upload download total expire
  line=$(grep -i '^subscription-userinfo:' "$headers" | tail -n 1 | tr -d '\r' || true)
  value=${line#*: }
  upload=$(sed -n 's/.*\(^\|;[[:space:]]*\)upload=\([0-9]*\).*/\2/p' <<<"$value")
  download=$(sed -n 's/.*\(^\|;[[:space:]]*\)download=\([0-9]*\).*/\2/p' <<<"$value")
  total=$(sed -n 's/.*\(^\|;[[:space:]]*\)total=\([0-9]*\).*/\2/p' <<<"$value")
  expire=$(sed -n 's/.*\(^\|;[[:space:]]*\)expire=\([0-9]*\).*/\2/p' <<<"$value")
  jq -cn --arg upload "${upload:-}" --arg download "${download:-}" --arg total "${total:-}" --arg expire "${expire:-}" \
    '{upload: (if $upload == "" then null else ($upload | tonumber) end), download: (if $download == "" then null else ($download | tonumber) end), total: (if $total == "" then null else ($total | tonumber) end), expire: (if $expire == "" then null else ($expire | tonumber) end)}'
}

# `--compressed` is not an optimisation here. Some subscription servers answer
# with `content-encoding: gzip` whether or not the client asked for it, and
# without this curl writes the compressed bytes out verbatim, so every step
# downstream sees binary instead of a subscription.
fetch_subscription() {
  local url=$1 body=$2 headers=$3
  if ! "$OMIHOMO_CURL" -fsSL --compressed --connect-timeout 10 --max-time 60 \
    -A "$OMIHOMO_USER_AGENT" -D "$headers" -o "$body" "$url"; then
    omi_error "subscription fetch failed" 21
  fi
}

classify_subscription_body() {
  local body=$1
  if omi_yq -e '
    tag == "!!map" and (
      has("proxies") or
      has("proxy-providers") or
      has("proxy-groups") or
      has("rules") or
      has("rule-providers") or
      has("dns") or
      has("tun")
    )
  ' "$body" >/dev/null 2>&1; then
    printf 'full\n'
  else
    printf 'raw\n'
  fi
}

raw_provider_error_detail() {
  local log=$1 detail
  detail=$(awk '
    /level=error/ { detail=$0; capture=1; next }
    capture && /^[[:space:]]/ { detail=detail " " $0; next }
    capture { capture=0 }
    END { print detail }
  ' "$log")
  detail=${detail#*msg=\"}
  detail=${detail%\"}
  sed -E \
    -e 's#([[:alnum:]+.-]+://)[^[:space:]\"]+#\1[redacted]#g' \
    -e 's#((token|password|passwd|secret|uuid)=)[^,;[:space:]\"]+#\1[redacted]#Ig' \
    <<<"$detail" | cut -c1-300
}

# `mihomo -t` validates only the top-level config. An isolated core makes the
# file provider parse the fetched payload without contacting its URL again.
preflight_raw_provider() {
  local body=$1 directory provider config socket log response binary pid valid=0 attempt detail
  omi_require_core || return $?
  directory=$(mktemp -d "${OMIHOMO_DATA_DIR}/.preflight.XXXXXX")
  provider="$directory/provider.txt"
  config="$directory/config.yaml"
  socket="$directory/controller.sock"
  log="$directory/mihomo.log"
  response="$directory/provider.json"
  cp -- "$body" "$provider"

  if ! OMIHOMO_PREFLIGHT_PROVIDER=$provider OMIHOMO_PREFLIGHT_SOCKET=$socket omi_yq -n -P '
    ."external-controller-unix" = strenv(OMIHOMO_PREFLIGHT_SOCKET) |
    ."proxy-providers".subscription = {
      "type": "file",
      "path": strenv(OMIHOMO_PREFLIGHT_PROVIDER)
    } |
    ."proxy-groups" = [{
      "name": "Proxy",
      "type": "select",
      "use": ["subscription"]
    }] |
    .rules = ["MATCH,Proxy"]
  ' >"$config"; then
    rm -rf -- "$directory"
    omi_error "failed to prepare raw subscription validation" 20
    return 20
  fi

  binary=$(omi_mihomo_bin)
  "$binary" -d "$directory" -f "$config" >"$log" 2>&1 &
  pid=$!
  for attempt in {1..40}; do
    if "$OMIHOMO_CURL" -fsS --max-time 1 --unix-socket "$socket" \
      http://localhost/providers/proxies/subscription >"$response" 2>/dev/null; then
      if jq -e '.proxies | type == "array" and length > 0' "$response" >/dev/null 2>&1; then
        valid=1
        break
      fi
      grep -q 'level=error' "$log" && break
    fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.05
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
  fi
  wait "$pid" 2>/dev/null || true

  if ((valid == 1)); then
    rm -rf -- "$directory"
    return 0
  fi
  detail=$(raw_provider_error_detail "$log")
  rm -rf -- "$directory"
  if [[ -n $detail ]]; then
    omi_error "mihomo rejected the raw subscription: $detail" 20
  else
    omi_error "mihomo rejected the raw subscription" 20
  fi
}

write_raw_wrapper() {
  local provider_path=$1 destination=$2
  # The subscription scheduler owns downloads and their saved deadline. A
  # second HTTP provider timer would bypass both it and connection restoration.
  OMIHOMO_PROVIDER_PATH=$provider_path omi_yq -n -P '
    ."proxy-providers".subscription = {
      "type": "file",
      "path": strenv(OMIHOMO_PROVIDER_PATH)
    } |
    ."proxy-groups" = [{
      "name": "Proxy",
      "type": "select",
      "use": ["subscription"]
    }] |
    .rules = ["MATCH,Proxy"]
  ' >"$destination"
}

prepare_subscription_cache() {
  local url=$1 body=$2 prepared status digest
  PREPARED_PROVIDER=
  if [[ $(classify_subscription_body "$body") == full ]]; then
    omi_validate_yaml "$body" || return $?
    PREPARED_BODY=$body
    return 0
  fi

  preflight_raw_provider "$body" || return $?
  digest=$(printf '%s' "$url" | sha256sum)
  mkdir -p "$OMIHOMO_DATA_DIR/providers" &&
    PREPARED_PROVIDER=$(mktemp "${OMIHOMO_DATA_DIR}/providers/${digest%% *}.XXXXXX.yaml") &&
    cp -- "$body" "$PREPARED_PROVIDER" || {
      omi_error "failed to cache the raw subscription" 20
      return 20
    }
  prepared=$(mktemp "${OMIHOMO_DATA_DIR}/.subscription.XXXXXX")
  if ! write_raw_wrapper "./providers/${PREPARED_PROVIDER##*/}" "$prepared"; then
    rm -f "$prepared"
    omi_error "failed to prepare the raw subscription" 20
    return 20
  fi
  omi_validate_yaml "$prepared" || {
    status=$?
    rm -f "$prepared"
    return "$status"
  }
  rm -f "$body"
  PREPARED_BODY=$prepared
}

cached_raw_provider() {
  local cache=$1 url=$2 path digest
  [[ -f $cache ]] || return 0
  path=$(omi_yq -r '."proxy-providers".subscription.path // ""' "$cache")
  digest=$(printf '%s' "$url" | sha256sum)
  if [[ $path =~ ^\./providers/${digest%% *}(\.[[:alnum:]]{6})?\.yaml$ ]]; then
    printf '%s/%s\n' "$OMIHOMO_DATA_DIR" "$path"
  fi
}

subscription_header() {
  local headers=$1 field=$2 line
  line=$(grep -i "^${field}:" "$headers" | tail -n 1 | tr -d '\r' || true)
  [[ -n $line ]] || return 0
  printf '%s\n' "${line#*:}" | sed 's/^[[:blank:]]*//; s/[[:blank:]]*$//'
}

subscription_update_interval_hours() {
  local value
  value=$(subscription_header "$1" profile-update-interval)
  # Whole positive hours only. Keep conversion and multiplication representable
  # even for a malformed or excessively large provider header.
  jq -nr --arg value "$value" '$value |
    if test("^[0-9]+$") then
      (try tonumber catch 12) | if . > 0 and . <= 2147483647 then . else 12 end
    else 12 end'
}

# A name arrives from a server header, so it has to be made safe for a cache
# filename and a CLI argument before anything writes it: no path separators, no
# control characters, no leading dot, and a bounded length.
sanitize_name() {
  local name
  name=$(tr -d '[:cntrl:]' <<<"$1" | tr '/\\' '  ' | tr -s '[:space:]' ' ')
  name=${name%"${name##*[![:space:]]}"}
  while [[ $name == [.[:space:]]* ]]; do name=${name#?}; done
  name=${name:0:64}
  printf '%s\n' "${name%"${name##*[![:space:]]}"}"
}

# The subscription names itself. `profile-title` is the ecosystem's header for
# it, optionally base64; `content-disposition` carries the older filename form.
# Neither is guaranteed, so the URL's host is the last resort.
subscription_fetched_name() {
  local headers=$1 url=$2 title disposition
  title=$(subscription_header "$headers" profile-title)
  if [[ $title == base64:* ]]; then
    title=$(base64 -d <<<"${title#base64:}" 2>/dev/null || true)
  fi
  if [[ -z $(sanitize_name "$title") ]]; then
    disposition=$(subscription_header "$headers" content-disposition)
    title=$(sed -n 's/.*filename="\([^"]*\)".*/\1/p' <<<"$disposition")
    [[ -n $title ]] || title=$(sed -n 's/.*filename=\([^;]*\).*/\1/p' <<<"$disposition")
  fi
  title=$(sanitize_name "$title")
  if [[ -z $title ]]; then
    title=${url#*://}
    title=$(sanitize_name "${title%%/*}")
  fi
  printf '%s\n' "${title:-subscription}"
}

# Two subscriptions can advertise the same title, and the name is the handle
# every other command takes, so later arrivals get a numeric suffix.
unique_name() {
  local base=$1 candidate=$1 index=2
  while subscription_exists "$candidate"; do
    candidate="$base $((index++))"
  done
  printf '%s\n' "$candidate"
}

fetch_validated_subscription() {
  local url=$1 body headers status
  body=$(mktemp "${OMIHOMO_DATA_DIR}/.subscription.XXXXXX")
  headers=$(mktemp "${OMIHOMO_DATA_DIR}/.headers.XXXXXX")
  fetch_subscription "$url" "$body" "$headers" || {
    status=$?
    rm -f "$body" "$headers"
    return "$status"
  }
  prepare_subscription_cache "$url" "$body" || {
    status=$?
    rm -f "$body" "$headers" "${PREPARED_PROVIDER:-}"
    return "$status"
  }
  FETCH_BODY=$PREPARED_BODY
  FETCH_HEADERS=$headers
}

subscription_add() {
  local url=${1:-}
  omi_init_layout
  [[ $url =~ ^https?:// ]] || omi_error "subscription URL must use http or https" 1
  subscription_url_exists "$url" && omi_error "subscription already exists for this URL" 1
  local name body headers record temporary
  fetch_validated_subscription "$url" || return $?
  body=$FETCH_BODY
  headers=$FETCH_HEADERS
  name=$(unique_name "$(subscription_fetched_name "$headers" "$url")")
  record=$(jq -cn --arg name "$name" --arg url "$url" --arg updated "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson interval "$(subscription_update_interval_hours "$headers")" --argjson userinfo "$(subscription_userinfo_json "$headers")" \
    '{name: $name, url: $url, updated_at: $updated, update_interval_hours: $interval, userinfo: $userinfo}')
  temporary=$(mktemp "${OMIHOMO_DATA_DIR}/.subscriptions.XXXXXX")
  jq --argjson record "$record" '. + [$record]' "$OMIHOMO_SUBSCRIPTIONS_FILE" >"$temporary"
  omi_atomic_move "$body" "$OMIHOMO_CACHE_DIR/${name}.yaml"
  omi_atomic_move "$temporary" "$OMIHOMO_SUBSCRIPTIONS_FILE"
  rm -f "$headers"
  # Nothing active means nothing works: `core start` refuses without an active
  # subscription, and the panel has no groups to show. Adding the first one is
  # the user asking to use it, so it becomes active here. A later add does not
  # steal the slot — switching is what `sub activate` is for.
  [[ -n $(omi_active_name) ]] || activate_cached_subscription "$name"
}

subscription_list() {
  if [[ ! -f $OMIHOMO_SUBSCRIPTIONS_FILE ]]; then
    printf '[]\n'
    return 0
  fi
  local active
  active=$(omi_active_name)
  jq --arg active "$active" '[.[] | {name, url, updated_at, upload: .userinfo.upload, download: .userinfo.download, total: .userinfo.total, expire: .userinfo.expire, active: (.name == $active)}]' "$OMIHOMO_SUBSCRIPTIONS_FILE"
}

# Capture immediately before reloading, after the potentially slow download.
# Mode and TUN normally live in the override; selectors are live controller
# state, and a reload can reset them even with store-selected enabled.
subscription_connection_snapshot() {
  local config proxies
  config=$(omi_api_curl /configs --max-time 5) || return 12
  proxies=$(omi_api_curl /proxies --max-time 5) || return 12
  jq -cen --argjson config "$config" --argjson proxies "$proxies" '
    select(($config.mode == "rule" or $config.mode == "global" or $config.mode == "direct")
      and ($config.tun.enable | type == "boolean") and ($proxies.proxies | type == "object")) |
    {mode: $config.mode, tun: $config.tun.enable,
     selections: [$proxies.proxies | to_entries[] | select(.value.type == "Selector") |
       {group: .key, name: .value.now}]}
  '
}

subscription_restore_selections() {
  local snapshot=$1 proxies selections selection group name encoded payload
  proxies=$(omi_api_curl /proxies --max-time 5) || return 12
  # A server removed by the provider cannot be restored. Leave mihomo's valid
  # fallback in that group, while restoring every selection still available.
  selections=$(jq -cr --argjson proxies "$proxies" '.selections[] |
    select(. as $selection | $proxies.proxies[$selection.group] |
      .type == "Selector" and any(.all[]?; . == $selection.name)) |
    [.group, .name] | @base64' <<<"$snapshot") || return 12
  while IFS= read -r selection; do
    [[ -n $selection ]] || continue
    group=$(base64 -d <<<"$selection" | jq -r '.[0]')
    name=$(base64 -d <<<"$selection" | jq -r '.[1]')
    encoded=$(jq -rn --arg group "$group" '$group | @uri')
    payload=$(jq -cn --arg name "$name" '{name: $name}')
    omi_api_curl "/proxies/${encoded}" --max-time 5 -X PUT \
      -H 'Content-Type: application/json' --data "$payload" >/dev/null || return 12
  done <<<"$selections"
}

subscription_reload_runtime() {
  local runtime=$1 snapshot=$2 status=0
  omi_reload_runtime "$runtime" || status=$?
  if ((status == 0)); then
    subscription_restore_selections "$snapshot" && return 0
    status=12
    # Disk still contains the previous runtime until the whole update succeeds.
    omi_load_runtime "$OMIHOMO_RUNTIME_FILE" "$runtime" || true
  fi
  subscription_restore_selections "$snapshot" || true
  omi_error "subscription reload failed; attempted to restore the previous connection" "$status"
}

subscription_update() {
  local name=${1:-}
  omi_init_layout --no-runtime-sync
  omi_require_core
  subscription_exists "$name" || omi_error "subscription not found: $name" 1
  local url body headers record temporary active cache runtime= status previous_provider snapshot
  url=$(jq -r --arg name "$name" '.[] | select(.name == $name) | .url' "$OMIHOMO_SUBSCRIPTIONS_FILE")
  cache="$OMIHOMO_CACHE_DIR/${name}.yaml"
  previous_provider=$(cached_raw_provider "$cache" "$url")
  fetch_validated_subscription "$url" || return $?
  body=$FETCH_BODY
  headers=$FETCH_HEADERS
  record=$(jq -cn --arg updated "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson interval "$(subscription_update_interval_hours "$headers")" --argjson userinfo "$(subscription_userinfo_json "$headers")" \
    '{updated_at: $updated, update_interval_hours: $interval, userinfo: $userinfo}')
  temporary=$(mktemp "${OMIHOMO_DATA_DIR}/.subscriptions.XXXXXX")
  jq --arg name "$name" --argjson record "$record" 'map(if .name == $name then . * $record else . end)' "$OMIHOMO_SUBSCRIPTIONS_FILE" >"$temporary"
  active=$(omi_active_name)
  if [[ $active == "$name" ]]; then
    runtime=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
    omi_prepare_runtime "$body" "$OMIHOMO_OVERRIDE_FILE" "$runtime" || {
      status=$?
      rm -f "$body" "$headers" "$temporary" "$runtime" "${PREPARED_PROVIDER:-}"
      return "$status"
    }
    if omi_unit_active; then
      snapshot=$(subscription_connection_snapshot) || {
        status=$?
        rm -f "$body" "$headers" "$temporary" "$runtime" "${PREPARED_PROVIDER:-}"
        omi_error "could not capture the current connection before updating" "$status"
        return "$status"
      }
      OMIHOMO_CONNECTION_SNAPSHOT=$snapshot omi_yq -i '
        .mode = (strenv(OMIHOMO_CONNECTION_SNAPSHOT) | from_json | .mode) |
        .tun.enable = (strenv(OMIHOMO_CONNECTION_SNAPSHOT) | from_json | .tun)
      ' "$runtime"
      subscription_reload_runtime "$runtime" "$snapshot" || {
        status=$?
        rm -f "$body" "$headers" "$temporary" "$runtime" "${PREPARED_PROVIDER:-}"
        return "$status"
      }
    fi
  fi
  omi_atomic_move "$body" "$cache"
  omi_atomic_move "$temporary" "$OMIHOMO_SUBSCRIPTIONS_FILE"
  if [[ -n $runtime ]]; then
    omi_atomic_move "$runtime" "$OMIHOMO_RUNTIME_FILE"
  fi
  rm -f "$headers" "$previous_provider"
}

# Each subscription is checked under the same lock as manual changes. A
# successful manual refresh resets the provider's deadline as well. Failed
# downloads keep updated_at unchanged and are retried on the next timer tick.
subscription_update_if_due() {
  local name=$1 updated now interval
  [[ -f $OMIHOMO_SUBSCRIPTIONS_FILE ]] || return 0
  subscription_exists "$name" || return 0
  updated=$(jq -r --arg name "$name" '.[] | select(.name == $name) |
    (.updated_at | try fromdateiso8601 catch 0)' "$OMIHOMO_SUBSCRIPTIONS_FILE")
  # Records imported before interval metadata existed use the same 12-hour
  # default as a response with no valid Profile-Update-Interval header.
  interval=$(jq -r --arg name "$name" '.[] | select(.name == $name) |
    (.update_interval_hours // 12) |
    if type == "number" and . > 0 and . == floor and . <= 2147483647 then . else 12 end' "$OMIHOMO_SUBSCRIPTIONS_FILE")
  now=$(date +%s)
  ((now - updated >= interval * 3600)) || return 0
  subscription_update "$name"
}

subscription_update_due() {
  [[ -f $OMIHOMO_SUBSCRIPTIONS_FILE ]] && omi_core_installed || return 0
  local names name status=0
  names=$(jq -r '.[].name' "$OMIHOMO_SUBSCRIPTIONS_FILE")
  while IFS= read -r name; do
    [[ -n $name ]] || continue
    # A separate process keeps errexit effective inside subscription_update
    # while allowing one failing subscription to leave the others runnable.
    "$OMIHOMO_ROOT/libexec/sub.sh" update-if-due "$name" || status=1
  done <<<"$names"
  return "$status"
}

subscription_remove() {
  local name=${1:-}
  omi_init_layout
  subscription_exists "$name" || omi_error "subscription not found: $name" 1
  local temporary active
  active=$(omi_active_name)
  if [[ $active == "$name" ]] && omi_unit_active; then
    omi_error "cannot remove the active subscription while mihomo is running" 11
  fi
  temporary=$(mktemp "${OMIHOMO_DATA_DIR}/.subscriptions.XXXXXX")
  jq --arg name "$name" 'map(select(.name != $name))' "$OMIHOMO_SUBSCRIPTIONS_FILE" >"$temporary"
  omi_atomic_move "$temporary" "$OMIHOMO_SUBSCRIPTIONS_FILE"
  rm -f "$OMIHOMO_CACHE_DIR/${name}.yaml"
  if [[ $active == "$name" ]]; then
    rm -f "$OMIHOMO_ACTIVE_FILE" "$OMIHOMO_RUNTIME_FILE"
  fi
}

# Point the runtime and the active marker at a subscription that is already
# cached. `subscription_add` reuses this for the first subscription, so it takes
# the name on trust: its caller has established that the subscription exists.
activate_cached_subscription() {
  local name=$1 cache runtime active_tmp status
  cache="$OMIHOMO_CACHE_DIR/${name}.yaml"
  [[ -f $cache ]] || omi_error "subscription cache is missing: $name" 1
  runtime=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
  omi_prepare_runtime "$cache" "$OMIHOMO_OVERRIDE_FILE" "$runtime" || {
    status=$?
    rm -f "$runtime"
    return "$status"
  }
  active_tmp=$(mktemp "${OMIHOMO_DATA_DIR}/.active.XXXXXX")
  printf '%s\n' "$name" >"$active_tmp"
  if omi_unit_active; then
    omi_reload_runtime "$runtime" || {
      status=$?
      rm -f "$runtime" "$active_tmp"
      return "$status"
    }
  fi
  omi_atomic_move "$runtime" "$OMIHOMO_RUNTIME_FILE"
  omi_atomic_move "$active_tmp" "$OMIHOMO_ACTIVE_FILE"
}

subscription_activate() {
  local name=${1:-}
  omi_init_layout
  omi_require_core
  subscription_exists "$name" || omi_error "subscription not found: $name" 1
  activate_cached_subscription "$name"
}

case ${1:-} in
  add) omi_with_lock subscription_add "${2:-}" ;;
  list) subscription_list ;;
  remove) omi_with_lock subscription_remove "${2:-}" ;;
  update) omi_with_lock subscription_update "${2:-}" ;;
  update-due) subscription_update_due ;;
  update-if-due) omi_with_lock subscription_update_if_due "${2:-}" ;;
  activate) omi_with_lock subscription_activate "${2:-}" ;;
  *) omi_error "unknown subscription command" 1 ;;
esac
