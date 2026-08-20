#!/usr/bin/env bash

set -euo pipefail

OMIHOMO_ROOT=${OMIHOMO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
source "$OMIHOMO_ROOT/lib/common.sh"

validate_domain_value() {
  local value=$1
  [[ -n $value && $value != *' '* && $value != */* ]]
}

validate_cidr() {
  local value=$1 address prefix octet
  if [[ $value =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$ ]]; then
    address=${value%/*}
    IFS=. read -r -a octets <<<"$address"
    for octet in "${octets[@]}"; do
      ((octet <= 255)) || return 1
    done
    return 0
  fi
  [[ $value =~ ^[0-9A-Fa-f:]+/([0-9]|[1-9][0-9]|1[0-1][0-9]|12[0-8])$ ]]
}

validate_rule() {
  local type=$1 value=$2 target=$3
  [[ -n $target ]] || omi_error "rule target is required" 1
  case $type in
    DOMAIN-SUFFIX|DOMAIN-KEYWORD) validate_domain_value "$value" || omi_error "invalid $type value" 1 ;;
    IP-CIDR) validate_cidr "$value" || omi_error "invalid IP-CIDR value" 1 ;;
    PROCESS-NAME) [[ -n $value ]] || omi_error "invalid PROCESS-NAME value" 1 ;;
    *) omi_error "unsupported rule type: $type" 1 ;;
  esac
}

override_candidate() {
  local expression=$1 candidate
  candidate=$(mktemp "${OMIHOMO_DATA_DIR}/.override.XXXXXX")
  if ! omi_yq eval "$expression" "$OMIHOMO_OVERRIDE_FILE" >"$candidate"; then
    rm -f "$candidate"
    omi_error "failed to update override.yaml" 1
  fi
  omi_commit_override "$candidate"
}

rule_add() {
  local type=${1:-} value=${2:-} target=${3:-}
  omi_init_layout
  validate_rule "$type" "$value" "$target"
  local rule="$type,$value,$target"
  OMIHOMO_RULE="$rule" override_candidate '.rules.prepend = ((.rules.prepend // []) + [strenv(OMIHOMO_RULE)])'
}

rule_raw() {
  omi_init_layout
  local placement=prepend rule
  if [[ ${1:-} == prepend || ${1:-} == append || ${1:-} == filter ]]; then
    placement=$1
    shift
  fi
  rule=${1:-}
  [[ -n $rule ]] || omi_error "raw rule string is required" 1
  OMIHOMO_RULE="$rule" override_candidate ".rules.$placement = ((.rules.$placement // []) + [strenv(OMIHOMO_RULE)])"
}

rule_list() {
  if [[ ! -f $OMIHOMO_OVERRIDE_FILE ]]; then
    printf '[]\n'
    return 0
  fi
  local rules
  rules=$(omi_yq -o=json '.rules // {}' "$OMIHOMO_OVERRIDE_FILE")
  jq -c '
    ([.prepend // [] | to_entries[] | {kind: "prepend", local_index: (.key + 1), rule: .value}] +
     [.append // [] | to_entries[] | {kind: "append", local_index: (.key + 1), rule: .value}] +
     [.filter // [] | to_entries[] | {kind: "filter", local_index: (.key + 1), rule: .value}])
    | to_entries | map(.value + {index: (.key + 1)})
  ' <<<"$rules"
}

rule_remove() {
  local index=${1:-} kind local_index zero_index
  omi_init_layout
  [[ $index =~ ^[1-9][0-9]*$ ]] || omi_error "rule index must be a positive integer" 1
  kind=$(rule_list | jq -r --argjson index "$index" '.[] | select(.index == $index) | .kind')
  [[ -n $kind ]] || omi_error "rule index out of range" 1
  local candidate expression
  local_index=$(rule_list | jq -r --argjson index "$index" '.[] | select(.index == $index) | .local_index')
  zero_index=$((local_index - 1))
  expression=".rules.$kind |= del(.[$zero_index])"
  override_candidate "$expression"
}

set_mode() {
  local mode=${1:-}
  [[ $mode == rule || $mode == global || $mode == direct ]] || omi_error "mode must be rule, global, or direct" 1
  omi_init_layout
  if [[ $mode == global ]]; then
    local active cache primary
    active=$(omi_active_name)
    [[ -n $active ]] || omi_error "global mode needs an active subscription" 13
    cache="$OMIHOMO_CACHE_DIR/${active}.yaml"
    [[ -f $cache ]] || omi_error "active subscription cache is missing" 13
    primary=$(omi_resolve_primary_group "$cache" "$OMIHOMO_OVERRIDE_FILE")
    [[ -n $primary ]] || omi_error "global mode needs a subscription group" 1
  fi
  override_candidate ".config.mode = \"$mode\""
}

set_tun() {
  local state=${1:-} enabled start_core=0
  [[ $state == on || $state == off ]] || omi_error "tun expects on or off" 1
  if [[ $state == on ]]; then
    omi_require_core
    omi_tun_capabilities_ok || omi_error "mihomo TUN capabilities are missing; run core repair" 14
    [[ -n $(omi_active_name) ]] || omi_error "no active subscription" 13
    if omi_unit_active; then
      omi_api_reachable || omi_error "mihomo controller is unreachable" 12
    else
      omi_require_command nft
      start_core=1
    fi
  fi
  enabled=false
  [[ $state == on ]] && enabled=true
  omi_init_layout
  override_candidate ".config.tun.enable = $enabled"
  if ((start_core == 1)); then
    omi_start_core
  fi
}

set_group() {
  local group=${1:-}
  [[ -n $group ]] || omi_error "group name is required" 1
  [[ $group != GLOBAL ]] || omi_error "GLOBAL is managed by Omihomo" 1
  omi_init_layout
  OMIHOMO_GROUP="$group" override_candidate '.omihomo."primary-group" = strenv(OMIHOMO_GROUP)'
}

case ${1:-} in
  rule)
    case ${2:-} in
      add) omi_with_lock rule_add "${3:-}" "${4:-}" "${5:-}" ;;
      list) rule_list ;;
      remove) omi_with_lock rule_remove "${3:-}" ;;
      raw) shift 2; omi_with_lock rule_raw "$@" ;;
      *) omi_error "unknown rule command" 1 ;;
    esac
    ;;
  set)
    case ${2:-} in
      mode) omi_with_lock set_mode "${3:-}" ;;
      tun) omi_with_lock set_tun "${3:-}" ;;
      group) omi_with_lock set_group "${3:-}" ;;
      *) omi_error "unknown set command" 1 ;;
    esac
    ;;
  *) omi_error "unknown override command" 1 ;;
esac
