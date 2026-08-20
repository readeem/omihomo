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

fetch_subscription() {
  local url=$1 body=$2 headers=$3
  if ! "$OMIHOMO_CURL" -fsSL -A "$OMIHOMO_USER_AGENT" -D "$headers" -o "$body" "$url"; then
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
  local url=$1 destination=$2 digest provider_path
  digest=$(printf '%s' "$url" | sha256sum)
  digest=${digest%% *}
  provider_path="./providers/${digest}.yaml"
  OMIHOMO_SUBSCRIPTION_URL=$url OMIHOMO_PROVIDER_PATH=$provider_path omi_yq -n -P '
    ."proxy-providers".subscription = {
      "type": "http",
      "url": strenv(OMIHOMO_SUBSCRIPTION_URL),
      "path": strenv(OMIHOMO_PROVIDER_PATH),
      "interval": 3600
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
  local url=$1 body=$2 prepared status
  if [[ $(classify_subscription_body "$body") == full ]]; then
    omi_validate_yaml "$body" || return $?
    PREPARED_BODY=$body
    return 0
  fi

  preflight_raw_provider "$body" || return $?
  prepared=$(mktemp "${OMIHOMO_DATA_DIR}/.subscription.XXXXXX")
  if ! write_raw_wrapper "$url" "$prepared"; then
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

subscription_header() {
  local headers=$1 field=$2 line
  line=$(grep -i "^${field}:" "$headers" | tail -n 1 | tr -d '\r' || true)
  [[ -n $line ]] || return 0
  printf '%s\n' "${line#*: }"
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
    rm -f "$body" "$headers"
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
  record=$(jq -cn --arg name "$name" --arg url "$url" --arg updated "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson userinfo "$(subscription_userinfo_json "$headers")" \
    '{name: $name, url: $url, updated_at: $updated, userinfo: $userinfo}')
  temporary=$(mktemp "${OMIHOMO_DATA_DIR}/.subscriptions.XXXXXX")
  jq --argjson record "$record" '. + [$record]' "$OMIHOMO_SUBSCRIPTIONS_FILE" >"$temporary"
  omi_atomic_move "$body" "$OMIHOMO_CACHE_DIR/${name}.yaml"
  omi_atomic_move "$temporary" "$OMIHOMO_SUBSCRIPTIONS_FILE"
  rm -f "$headers"
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

subscription_update() {
  local name=${1:-}
  omi_init_layout
  omi_require_core
  subscription_exists "$name" || omi_error "subscription not found: $name" 1
  local url body headers record temporary active cache runtime= status
  url=$(jq -r --arg name "$name" '.[] | select(.name == $name) | .url' "$OMIHOMO_SUBSCRIPTIONS_FILE")
  fetch_validated_subscription "$url" || return $?
  body=$FETCH_BODY
  headers=$FETCH_HEADERS
  record=$(jq -cn --arg updated "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson userinfo "$(subscription_userinfo_json "$headers")" \
    '{updated_at: $updated, userinfo: $userinfo}')
  temporary=$(mktemp "${OMIHOMO_DATA_DIR}/.subscriptions.XXXXXX")
  jq --arg name "$name" --argjson record "$record" 'map(if .name == $name then . * $record else . end)' "$OMIHOMO_SUBSCRIPTIONS_FILE" >"$temporary"
  active=$(omi_active_name)
  if [[ $active == "$name" ]]; then
    runtime=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
    omi_prepare_runtime "$body" "$OMIHOMO_OVERRIDE_FILE" "$runtime" || {
      status=$?
      rm -f "$body" "$headers" "$temporary" "$runtime"
      return "$status"
    }
    if omi_unit_active; then
      omi_reload_runtime "$runtime" || {
        status=$?
        rm -f "$body" "$headers" "$temporary" "$runtime"
        return "$status"
      }
    fi
  fi
  cache="$OMIHOMO_CACHE_DIR/${name}.yaml"
  omi_atomic_move "$body" "$cache"
  omi_atomic_move "$temporary" "$OMIHOMO_SUBSCRIPTIONS_FILE"
  if [[ -n $runtime ]]; then
    omi_atomic_move "$runtime" "$OMIHOMO_RUNTIME_FILE"
  fi
  rm -f "$headers"
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

subscription_activate() {
  local name=${1:-}
  omi_init_layout
  omi_require_core
  subscription_exists "$name" || omi_error "subscription not found: $name" 1
  local cache runtime
  cache="$OMIHOMO_CACHE_DIR/${name}.yaml"
  [[ -f $cache ]] || omi_error "subscription cache is missing: $name" 1
  runtime=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
  omi_prepare_runtime "$cache" "$OMIHOMO_OVERRIDE_FILE" "$runtime"
  local active_tmp
  active_tmp=$(mktemp "${OMIHOMO_DATA_DIR}/.active.XXXXXX")
  printf '%s\n' "$name" >"$active_tmp"
  if omi_unit_active; then
    omi_reload_runtime "$runtime" || {
      local status=$?
      rm -f "$runtime" "$active_tmp"
      return "$status"
    }
  fi
  omi_atomic_move "$runtime" "$OMIHOMO_RUNTIME_FILE"
  omi_atomic_move "$active_tmp" "$OMIHOMO_ACTIVE_FILE"
}

case ${1:-} in
  add) omi_with_lock subscription_add "${2:-}" ;;
  list) subscription_list ;;
  remove) omi_with_lock subscription_remove "${2:-}" ;;
  update) omi_with_lock subscription_update "${2:-}" ;;
  activate) omi_with_lock subscription_activate "${2:-}" ;;
  *) omi_error "unknown subscription command" 1 ;;
esac
