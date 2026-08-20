#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
DATA_HOME=${XDG_DATA_HOME:-${HOME}/.local/share}
CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME}/.config}
OMIHOMO_DATA_DIR=${OMIHOMO_DATA_DIR:-${DATA_HOME}/omihomo}
OMIHOMO_CACHE_DIR=${OMIHOMO_DATA_DIR}/cache
OMIHOMO_SUBSCRIPTIONS_FILE=${OMIHOMO_DATA_DIR}/subscriptions.json
OMIHOMO_OVERRIDE_FILE=${OMIHOMO_DATA_DIR}/override.yaml
OMIHOMO_RUNTIME_FILE=${OMIHOMO_DATA_DIR}/runtime.yaml
OMIHOMO_ACTIVE_FILE=${OMIHOMO_DATA_DIR}/active
OMIHOMO_LOCK_FILE=${OMIHOMO_DATA_DIR}/.lock
OMIHOMO_UNIT=${OMIHOMO_UNIT:-omihomo.service}
OMIHOMO_UNIT_FILE=${OMIHOMO_UNIT_FILE:-${CONFIG_HOME}/systemd/user/${OMIHOMO_UNIT}}
OMIHOMO_YQ=${OMIHOMO_YQ:-yq}
OMIHOMO_CURL=${OMIHOMO_CURL:-curl}
OMIHOMO_SYSTEMCTL=${OMIHOMO_SYSTEMCTL:-systemctl}
OMIHOMO_GETCAP=${OMIHOMO_GETCAP:-getcap}
# Subscription servers content-negotiate on User-Agent. A client they do not
# recognise gets a base64 list of share links, which `mihomo -t` then rejects;
# a Clash-family agent gets the mihomo YAML we actually want.
OMIHOMO_USER_AGENT=${OMIHOMO_USER_AGENT:-clash.meta}

omi_init_layout() {
  mkdir -p "$OMIHOMO_DATA_DIR" "$OMIHOMO_CACHE_DIR" "$(dirname "$OMIHOMO_UNIT_FILE")"
  if [[ ! -f $OMIHOMO_SUBSCRIPTIONS_FILE ]]; then
    printf '[]\n' >"$OMIHOMO_SUBSCRIPTIONS_FILE"
  fi
  if [[ ! -f $OMIHOMO_OVERRIDE_FILE ]]; then
    omi_write_default_override "$OMIHOMO_OVERRIDE_FILE"
  fi
}

omi_write_default_override() {
  local destination=$1 secret
  if command -v openssl >/dev/null 2>&1; then
    secret=$(openssl rand -hex 16)
  else
    secret=$(date +%s%N | sha256sum | cut -c1-32)
  fi
  cat >"$destination" <<EOF
config:
  external-controller: 127.0.0.1:9090
  secret: "$secret"
  mode: rule
  profile:
    store-selected: true
  tun:
    enable: false
    auto-route: true
    auto-redirect: true
    stack: mixed
    dns-hijack:
      - any:53
omihomo:
  primary-group: ""
rules:
  prepend: []
  append: []
  filter: []
EOF
}

omi_require_command() {
  command -v "$1" >/dev/null 2>&1 || omi_error "required command not found: $1" 1
}

# Every YAML read and write goes through here, because "yq" is two different
# programs. We need mikefarah's Go yq v4 (Arch package go-yq); the `yq` package
# is kislyuk's jq wrapper, which takes different arguments and answers with a
# usage dump. Resolve to whichever binary on this machine is the real one.
omi_yq() {
  if ! omi_yq_available; then
    omi_error "mikefarah yq v4 not found: install the go-yq package (the yq package is a different program)" 1
    return 1
  fi
  "$OMIHOMO_YQ_RESOLVED" "$@"
}

omi_yq_available() {
  [[ -z ${OMIHOMO_YQ_RESOLVED:-} ]] || return 0
  local candidate
  for candidate in "$OMIHOMO_YQ" yq-go go-yq; do
    command -v "$candidate" >/dev/null 2>&1 || continue
    if "$candidate" --version 2>/dev/null | grep -qi mikefarah; then
      OMIHOMO_YQ_RESOLVED=$candidate
      return 0
    fi
  done
  return 1
}

# Installation and repair each enter one privileged helper. A terminal uses
# sudo; a panel action has no terminal for password input, so it uses pkexec.
omi_privileged() {
  if [[ -t 0 ]] && command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    pkexec "$@"
  fi
}

# Progress for a human. The panel parses stdout, so it only prints on a terminal.
omi_note() {
  [[ ${OMIHOMO_PRETTY:-0} == 1 || -t 1 ]] || return 0
  printf 'omihomo: %s\n' "$1"
}

omi_mihomo_bin() {
  if [[ -n ${OMIHOMO_MIHOMO_BIN:-} ]]; then
    printf '%s\n' "$OMIHOMO_MIHOMO_BIN"
    return 0
  fi
  command -v mihomo 2>/dev/null || true
}

omi_core_installed() {
  local binary
  binary=$(omi_mihomo_bin)
  [[ -n $binary && -x $binary ]]
}

omi_require_core() {
  omi_core_installed || omi_error "mihomo is not installed" 10
}

omi_tun_capabilities_ok() {
  local binary output value capabilities
  binary=$(omi_mihomo_bin)
  [[ -n $binary && -x $binary ]] || return 1
  output=$("$OMIHOMO_GETCAP" "$binary" 2>/dev/null) || return 1
  value=${output#* }
  [[ $value == *=ep ]] || return 1
  capabilities=${value%=ep}
  [[ ,$capabilities, == *,cap_net_admin,* ]] &&
    [[ ,$capabilities, == *,cap_net_raw,* ]] &&
    [[ ,$capabilities, == *,cap_net_bind_service,* ]]
}

# Errors are machine-readable by default, because the panel parses stderr as
# JSON. When a human is reading (stderr is a terminal, or --pretty was passed)
# the same message goes out as a plain line instead.
omi_error() {
  local message=$1 code=${2:-1}
  # Only the first error speaks. It is the specific one, and the panel parses
  # stderr as a single JSON object; wrappers further up just carry the code.
  if [[ ${OMIHOMO_ERROR_EMITTED:-0} == 1 ]]; then
    return "$code"
  fi
  OMIHOMO_ERROR_EMITTED=1
  if [[ ${OMIHOMO_PRETTY:-0} == 1 || -t 2 ]]; then
    # Code 1 is the generic failure, so it tells a human nothing worth printing.
    if ((code == 1)); then
      printf 'omihomo: %s\n' "$message" >&2
    else
      printf 'omihomo: %s (exit %s)\n' "$message" "$code" >&2
    fi
  else
    printf '{"error":%s,"code":%s}\n' "$(jq -Rn --arg message "$message" '$message')" "$code" >&2
  fi
  return "$code"
}

omi_atomic_move() {
  local source=$1 destination=$2
  mkdir -p "$(dirname "$destination")"
  mv -f -- "$source" "$destination"
}

omi_with_lock() {
  mkdir -p "$OMIHOMO_DATA_DIR"
  exec 9>"$OMIHOMO_LOCK_FILE"
  flock -x 9
  "$@"
}

omi_systemctl() {
  if ! "$OMIHOMO_SYSTEMCTL" "$@" >/dev/null 2>&1; then
    omi_error "systemd operation failed" 1
  fi
}

omi_write_unit() {
  mkdir -p "$(dirname "$OMIHOMO_UNIT_FILE")"
  cat >"$OMIHOMO_UNIT_FILE" <<EOF
[Unit]
Description=Omihomo mihomo proxy core
After=network-online.target

[Service]
ExecStart=$(omi_mihomo_bin) -d $OMIHOMO_DATA_DIR -f $OMIHOMO_RUNTIME_FILE
Restart=on-failure

[Install]
WantedBy=default.target
EOF
}

omi_start_core() {
  omi_require_core
  omi_require_command nft
  [[ -n $(omi_active_name) ]] || omi_error "no active subscription" 13
  [[ -f $OMIHOMO_RUNTIME_FILE ]] || omi_error "active runtime config is missing" 13
  [[ -f $OMIHOMO_UNIT_FILE ]] || omi_write_unit
  omi_systemctl --user daemon-reload
  omi_systemctl --user start "$OMIHOMO_UNIT"
}

omi_active_name() {
  [[ -s $OMIHOMO_ACTIVE_FILE ]] || return 0
  head -n 1 "$OMIHOMO_ACTIVE_FILE"
}

omi_unit_active() {
  "$OMIHOMO_SYSTEMCTL" --user is-active --quiet "$OMIHOMO_UNIT" >/dev/null 2>&1
}

# True when the unit is wired into the session's default target, which is what
# the panel calls autostart.
omi_unit_enabled() {
  "$OMIHOMO_SYSTEMCTL" --user is-enabled --quiet "$OMIHOMO_UNIT" >/dev/null 2>&1
}

omi_api_address() {
  [[ -f $OMIHOMO_OVERRIDE_FILE ]] || return 0
  omi_yq -r '.config."external-controller" // "127.0.0.1:9090"' "$OMIHOMO_OVERRIDE_FILE"
}

omi_api_secret() {
  [[ -f $OMIHOMO_OVERRIDE_FILE ]] || return 0
  omi_yq -r '.config.secret // ""' "$OMIHOMO_OVERRIDE_FILE"
}

omi_api_reachable() {
  omi_unit_active || return 1
  local address secret
  address=$(omi_api_address)
  secret=$(omi_api_secret)
  "$OMIHOMO_CURL" -fsS --max-time 2 -H "Authorization: Bearer $secret" "http://${address}/version" >/dev/null
}

omi_reload_runtime() {
  local runtime_path=${1:-$OMIHOMO_RUNTIME_FILE}
  omi_unit_active || return 0
  if ! omi_api_reachable; then
    omi_error "mihomo controller is unreachable" 12
    return 12
  fi
  local address secret payload
  address=$(omi_api_address)
  secret=$(omi_api_secret)
  payload=$(jq -cn --arg path "$runtime_path" '{path: $path, payload: ""}')
  if ! "$OMIHOMO_CURL" -fsS -X PUT -H "Authorization: Bearer $secret" -H 'Content-Type: application/json' \
    --data "$payload" "http://${address}/configs?force=true" >/dev/null; then
    omi_error "mihomo rejected the runtime config" 12
    return 12
  fi
}

omi_merge_runtime() {
  local source=$1 override=$2 destination=$3
  omi_yq eval-all -P '
    select(fileIndex == 0) as $base |
    select(fileIndex == 1) as $override |
    ($override.config // {}) as $config |
    ($override.rules.prepend // []) as $prepend |
    ($override.rules.append // []) as $append |
    ($override.rules.filter // []) as $filter |
    ($base * $config) as $merged |
    ($merged | .rules = ($prepend + ((.rules // []) | map(. as $rule | select(($filter | contains([$rule]) | not))) + $append)))
  ' "$source" "$override" >"$destination"
}

omi_validate_yaml() {
  local file=$1 binary
  omi_require_core || return $?
  binary=$(omi_mihomo_bin)
  if ! "$binary" -t -f "$file" >/dev/null 2>&1; then
    omi_error "mihomo rejected the YAML" 20
    return 20
  fi
}

omi_prepare_runtime() {
  local source=$1 override=$2 destination=$3
  if ! omi_merge_runtime "$source" "$override" "$destination"; then
    rm -f "$destination"
    omi_error "failed to merge the runtime config" 1
    return 1
  fi
  omi_validate_yaml "$destination" || {
    local status=$?
    rm -f "$destination"
    return "$status"
  }
}

omi_commit_override() {
  local candidate=$1 active cache runtime
  active=$(omi_active_name)
  if [[ -n $active ]]; then
    cache="$OMIHOMO_CACHE_DIR/${active}.yaml"
    [[ -f $cache ]] || { rm -f "$candidate"; omi_error "active subscription cache is missing" 13; }
    runtime=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
    omi_prepare_runtime "$cache" "$candidate" "$runtime" || {
      local status=$?
      rm -f "$candidate"
      return "$status"
    }
    if omi_unit_active; then
      omi_reload_runtime "$runtime" || {
        local status=$?
        rm -f "$candidate" "$runtime"
        return "$status"
      }
    fi
    omi_atomic_move "$candidate" "$OMIHOMO_OVERRIDE_FILE"
    omi_atomic_move "$runtime" "$OMIHOMO_RUNTIME_FILE"
  else
    omi_atomic_move "$candidate" "$OMIHOMO_OVERRIDE_FILE"
  fi
}

omi_pretty() {
  local input json_type
  input=$(cat)
  json_type=$(jq -r 'type' <<<"$input")
  case $json_type in
  object)
    jq -r '
    def scalar: . == null or type == "string" or type == "number" or type == "boolean";
    if all(.[]; scalar) then
      to_entries as $entries |
      ($entries | map(.key | length) | max // 0) as $width |
      $entries[] | "\(.key + (" " * ($width - (.key | length)))): \(.value // "")"
    else . end
    ' <<<"$input"
    ;;
  array)
    jq -r '
    def scalar: . == null or type == "string" or type == "number" or type == "boolean";
    if all(.[]; type == "object" and all(.[]; scalar)) then
      . as $rows |
      (if ($rows | length) == 0 then [] else ($rows | map(keys_unsorted) | add | unique) end) as $keys |
      ($keys | @tsv),
      ($rows[] | . as $row | [$keys[] | ($row[.] // "") | tostring] | @tsv)
    else . end
    ' <<<"$input" | column -t -s $'\t'
    ;;
  *) jq . <<<"$input" ;;
  esac
}
