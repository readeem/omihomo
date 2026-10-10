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
OMIHOMO_STAT=${OMIHOMO_STAT:-stat}
OMIHOMO_NET_DIR=${OMIHOMO_NET_DIR:-/sys/class/net}
OMIHOMO_RELOAD_TIMEOUT=${OMIHOMO_RELOAD_TIMEOUT:-20}
# Subscription servers content-negotiate on User-Agent. A Clash-family agent
# usually gets a full config, which Omihomo can preserve without wrapping it.
OMIHOMO_USER_AGENT=${OMIHOMO_USER_AGENT:-clash.meta}
# The loopback proxy tailscaled dials when the Tailscale integration is on. It
# is Omihomo's own listener rather than the subscription's `mixed-port`, so the
# drop-in written for tailscaled never depends on which subscription is active.
OMIHOMO_TAILSCALE_PORT=${OMIHOMO_TAILSCALE_PORT:-7899}
OMIHOMO_TAILSCALE_LISTENER=omihomo-tailscale
# Tailscale's interface and MagicDNS resolver. Without the first, TUN routes
# what Tailscale already handles; without the second, `dns-hijack: any:53`
# swallows the `*.ts.net` lookups only Tailscale's resolver can answer.
OMIHOMO_TAILSCALE_INTERFACE=tailscale0
OMIHOMO_TAILSCALE_DNS=100.100.100.100
OMIHOMO_TAILSCALE_DOMAIN='+.ts.net'
# Sending the tailnet through the proxy means TUN has to see it before
# Tailscale's peer routes do. Tailscale's ip rules sit at 5210-5250 (its own
# packets, which must still leave directly) and 5270 (table 52, the peers), and
# sing-tun takes the index plus up to ten, so 5260 slots TUN in between.
OMIHOMO_TAILNET_RANGE=100.64.0.0/10
OMIHOMO_TAILNET_TUN_RULE_INDEX=5260
# The TUN adapter's interface name. mihomo defaults it to `Meta`, which every
# other mihomo-based client also defaults to, so a machine that runs one of
# those alongside Omihomo has a `Meta` that belongs to somebody else. The
# device probe in `omihomo status` would read that as its own working tunnel,
# so Omihomo names its adapter after itself instead.
OMIHOMO_TUN_DEVICE_NAME=omihomo
# TUN has two working shapes, and `auto-redirect` decides which. On it hands TCP
# to the kernel through an nftables table sing-tun creates, which is the faster
# path. Off, the gVisor stack tunnels entirely in userspace and touches no
# firewall rules, so it works on any machine, which is why it is the default.
#
# The stack is not a free choice alongside it. `mixed` uses the system TCP
# stack, which only ever sees TCP because the redirect puts it there, so the
# pair `auto-redirect: false` with `mixed` is the one combination that comes up
# looking healthy and silently carries no TCP at all. The two fields only ever
# move together, which is what `set tun-redirect` exists to guarantee.
OMIHOMO_TUN_REDIRECT_STACK=mixed
OMIHOMO_TUN_COMPATIBLE_STACK=gvisor
OMIHOMO_MIXED_PORT=${OMIHOMO_MIXED_PORT:-7890}
OMIHOMO_DEFAULT_NAMESERVERS=(https://1.1.1.1/dns-query https://1.0.0.1/dns-query)

omi_init_layout() {
  mkdir -p "$OMIHOMO_DATA_DIR" "$OMIHOMO_CACHE_DIR" "$(dirname "$OMIHOMO_UNIT_FILE")"
  if [[ ! -f $OMIHOMO_SUBSCRIPTIONS_FILE ]]; then
    printf '[]\n' >"$OMIHOMO_SUBSCRIPTIONS_FILE"
  fi
  if [[ ! -f $OMIHOMO_OVERRIDE_FILE ]]; then
    omi_write_default_override "$OMIHOMO_OVERRIDE_FILE"
  else
    omi_backfill_override "$OMIHOMO_OVERRIDE_FILE"
  fi
  # An update prepares and validates its final runtime in one transaction.
  # Syncing the old cache first could reset live selections before capture.
  [[ ${1:-} == --no-runtime-sync ]] || omi_sync_runtime
}

omi_sync_runtime() {
  local active cache runtime
  active=$(omi_active_name)
  cache="$OMIHOMO_CACHE_DIR/${active}.yaml"
  [[ -n $active && -f $cache ]] && omi_yq_available || return 0
  runtime=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
  if (omi_merge_runtime "$cache" "$OMIHOMO_OVERRIDE_FILE" "$runtime") 2>/dev/null &&
    ! cmp -s "$runtime" "$OMIHOMO_RUNTIME_FILE" &&
    (omi_validate_yaml "$runtime") >/dev/null 2>&1 &&
    (omi_reload_runtime "$runtime") >/dev/null 2>&1; then
    omi_atomic_move "$runtime" "$OMIHOMO_RUNTIME_FILE"
  else
    rm -f "$runtime"
  fi
}

# Ranges that must never leave through the tunnel: loopback, the RFC 1918 and
# CGNAT private space, link-local, the documentation and multicast blocks, and
# their IPv6 equivalents. Without them `auto-route` swallows the LAN, so the
# router's web UI, printers, and local DNS stop answering the moment TUN comes
# up.
#
# Koala Clash ships the multicast and reserved space as 224.0.0.0/3 and
# ff00::/8, but both run to the last address, which sing-tun cannot put in an
# nftables interval set: it writes an end equal to the start, the kernel answers
# "file exists", and TUN acceleration never starts. So these stop just short.
OMIHOMO_TUN_ROUTE_EXCLUDE=(
  0.0.0.0/8
  10.0.0.0/8
  100.64.0.0/10
  127.0.0.0/8
  169.254.0.0/16
  172.16.0.0/12
  192.0.0.0/24
  192.0.2.0/24
  192.88.99.0/24
  192.168.0.0/16
  198.51.100.0/24
  203.0.113.0/24
  224.0.0.0/4
  240.0.0.0/5
  ::/127
  fc00::/7
  fe80::/10
  ff00::/9
)

# The list as a YAML block sequence, indented by the given number of spaces.
# yq's `env()` parses the same text, so the default override and the backfill
# below share one source for it.
omi_tun_route_exclude_yaml() {
  local indent entry
  printf -v indent '%*s' "${1:-0}" ''
  for entry in "${OMIHOMO_TUN_ROUTE_EXCLUDE[@]}"; do
    printf '%s- %s\n' "$indent" "$entry"
  done
}

# The Tailscale coexistence defaults, in the same shape the default override
# writes them. Both are block YAML so `env()` and the heredoc read one source.
omi_tailscale_exclude_interface_yaml() {
  local indent
  printf -v indent '%*s' "${1:-0}" ''
  printf '%s- %s\n' "$indent" "$OMIHOMO_TAILSCALE_INTERFACE"
}

omi_default_nameservers_yaml() {
  local entry indent
  printf -v indent '%*s' "${2:-0}" ''
  for entry in "${OMIHOMO_DEFAULT_NAMESERVERS[@]}"; do
    printf '%s- %s#%s\n' "$indent" "$entry" "${1:-DIRECT}"
  done
}

# NetworkManager decides the machine is online by fetching its connectivity URI
# over a socket bound to the physical interface, outside the tunnel. A fake-ip
# answer from `dns-hijack` only routes inside the tunnel, so the probe times out
# and the desktop reports limited connectivity. Its host has to resolve for real.
omi_connectivity_check_host() {
  local uri
  command -v NetworkManager >/dev/null 2>&1 || return 0
  uri=$(NetworkManager --print-config 2>/dev/null |
    awk -F= '/^\[/ { section = ($0 == "[connectivity]") } section && $1 == "uri" { print $2; exit }')
  uri=${uri#*://}
  printf '%s\n' "${uri%%[/:]*}"
}

omi_tailscale_nameserver_policy_yaml() {
  local indent
  printf -v indent '%*s' "${1:-0}" ''
  printf '%s"%s": %s\n' "$indent" "$OMIHOMO_TAILSCALE_DOMAIN" "$OMIHOMO_TAILSCALE_DNS"
}

# True when the override already carries a key, so a default is only ever
# written once. This is what leaves a list the user deliberately emptied alone.
omi_override_has() {
  local file=$1 parent=$2 key=$3
  [[ $(omi_yq -r "($parent // {}) | has(\"$key\")" "$file" 2>/dev/null) == true ]]
}

omi_backfill_override() {
  local file=$1 candidate key expression=""
  omi_yq_available || return 0
  omi_override_has "$file" .config.tun device ||
    expression+='.config.tun.device = strenv(OMIHOMO_TUN_DEVICE_NAME) | '
  omi_override_has "$file" .config.tun route-exclude-address ||
    expression+='.config.tun."route-exclude-address" = env(OMIHOMO_TUN_ROUTE_EXCLUDE_YAML) | '
  [[ $(omi_yq -r '.config.tun."route-exclude-address" // [] | any_c(. == "224.0.0.0/3" or . == "ff00::/8")' "$file" 2>/dev/null) != true ]] ||
    expression+='.config.tun."route-exclude-address" |= (map(sub("^224\.0\.0\.0/3$"; "224.0.0.0/4,240.0.0.0/5") | sub("^ff00::/8$"; "ff00::/9") | split(",")) | flatten) | '
  omi_override_has "$file" .config.tun exclude-interface ||
    expression+='.config.tun."exclude-interface" = env(OMIHOMO_TAILSCALE_EXCLUDE_INTERFACE_YAML) | '
  omi_override_has "$file" .config.dns nameserver-policy ||
    expression+='.config.dns."nameserver-policy" = env(OMIHOMO_TAILSCALE_NAMESERVER_POLICY_YAML) | '
  omi_override_has "$file" .config.dns enhanced-mode ||
    expression+='.config.dns."enhanced-mode" = "redir-host" | '
  omi_override_has "$file" .config.dns nameserver ||
    expression+='.config.dns.nameserver = env(OMIHOMO_PROXY_NAMESERVERS_YAML) | '
  omi_override_has "$file" .config.dns fallback ||
    expression+='.config.dns.fallback = [] | '
  for key in default-nameserver proxy-server-nameserver; do
    omi_override_has "$file" .config.dns "$key" ||
      expression+=".config.dns.\"$key\" = env(OMIHOMO_DEFAULT_NAMESERVERS_YAML) | "
  done
  [[ -n $expression ]] || return 0
  candidate=$(mktemp "${OMIHOMO_DATA_DIR}/.override.XXXXXX")
  if ! OMIHOMO_TUN_ROUTE_EXCLUDE_YAML=$(omi_tun_route_exclude_yaml) \
    OMIHOMO_TAILSCALE_EXCLUDE_INTERFACE_YAML=$(omi_tailscale_exclude_interface_yaml) \
    OMIHOMO_TAILSCALE_NAMESERVER_POLICY_YAML=$(omi_tailscale_nameserver_policy_yaml) \
    OMIHOMO_PROXY_NAMESERVERS_YAML=$(omi_default_nameservers_yaml GLOBAL) \
    OMIHOMO_DEFAULT_NAMESERVERS_YAML=$(omi_default_nameservers_yaml) \
    OMIHOMO_TUN_DEVICE_NAME=$OMIHOMO_TUN_DEVICE_NAME \
    omi_yq eval "${expression%' | '}" "$file" >"$candidate" 2>/dev/null; then
    rm -f "$candidate"
    return 0
  fi
  omi_atomic_move "$candidate" "$file"
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
    device: $OMIHOMO_TUN_DEVICE_NAME
    auto-route: true
    auto-redirect: false
    stack: $OMIHOMO_TUN_COMPATIBLE_STACK
    dns-hijack:
      - any:53
    route-exclude-address:
$(omi_tun_route_exclude_yaml 6)
    exclude-interface:
$(omi_tailscale_exclude_interface_yaml 6)
  dns:
    enhanced-mode: redir-host
    fallback: []
    nameserver:
$(omi_default_nameservers_yaml GLOBAL 6)
    default-nameserver:
$(omi_default_nameservers_yaml DIRECT 6)
    proxy-server-nameserver:
$(omi_default_nameservers_yaml DIRECT 6)
    nameserver-policy:
$(omi_tailscale_nameserver_policy_yaml 6)
omihomo:
  primary-group: ""
  tailscale: false
  tailnet-proxy: false
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

omi_tailscaled_bin() {
  if [[ -n ${OMIHOMO_TAILSCALED_BIN:-} ]]; then
    printf '%s\n' "$OMIHOMO_TAILSCALED_BIN"
    return 0
  fi
  command -v tailscaled 2>/dev/null || true
}

omi_tailscale_present() {
  local binary
  binary=$(omi_tailscaled_bin)
  [[ -n $binary && -x $binary ]]
}

# tailscaled is a system unit, so its environment is root-owned: the drop-in
# goes in through the same privileged helper that installs the core.
omi_apply_tailscale_dropin() {
  local state=$1
  omi_privileged "$OMIHOMO_ROOT/libexec/root.sh" tailscale "$state" "$OMIHOMO_TAILSCALE_PORT" ||
    omi_error "updating the tailscaled proxy configuration failed" 1
}

# TUN needs the core to run as root (ADR-0006): mihomo shells out to resolvectl
# for systemd-resolved, and only a uid 0 caller skips polkit. That is true when
# the binary is owned by root and carries both the setuid and setgid bits.
omi_tun_permissions_ok() {
  local binary owner group mode
  binary=$(omi_mihomo_bin)
  [[ -n $binary && -x $binary ]] || return 1
  read -r owner group mode < <("$OMIHOMO_STAT" -c '%u %g %a' "$binary" 2>/dev/null) || return 1
  [[ $owner == 0 && $group == 0 ]] || return 1
  # stat's %a is octal, so a leading 6 is setuid (4) plus setgid (2).
  [[ $mode == 6??? ]]
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

omi_write_subscription_timer() {
  local directory
  directory=$(dirname "$OMIHOMO_UNIT_FILE")
  mkdir -p "$directory"
  cat >"$directory/omihomo-subscription-update.service" <<EOF
[Unit]
Description=Refresh Omihomo subscriptions when their saved deadlines elapse

[Service]
Type=oneshot
ExecStart=$HOME/.local/bin/omihomo sub update-due
# Downloads and controller calls have their own bounds. A fixed batch limit
# could repeatedly stop long lists before their healthy final subscriptions.
TimeoutStartSec=infinity
EOF
  cat >"$directory/omihomo-subscription-update.timer" <<'EOF'
[Unit]
Description=Check Omihomo subscription refresh deadlines every minute

[Timer]
OnCalendar=*-*-* *:*:00
AccuracySec=1s
Persistent=true

[Install]
WantedBy=timers.target
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

# Calls the controller at PATH, passing the secret on stdin to keep it out of argv.
omi_api_curl() {
  local path=$1 address
  address=$(omi_api_address)
  "$OMIHOMO_CURL" -fsS -H @- "${@:2}" "http://${address}${path}" <<<"Authorization: Bearer $(omi_api_secret)"
}

omi_api_reachable() {
  omi_unit_active || return 1
  omi_api_curl /version --max-time 2 >/dev/null
}

# Omihomo names its adapter after itself, but the runtime is the config mihomo
# is actually running, and an override from before that default still leaves the
# name to mihomo, which calls it `Meta`.
omi_tun_device_name() {
  local runtime=${1:-$OMIHOMO_RUNTIME_FILE} device=${OMIHOMO_TUN_DEVICE:-}
  if [[ -z $device && -f $runtime ]] && omi_yq_available; then
    device=$(omi_yq -r '.tun.device // ""' "$runtime")
  fi
  printf '%s\n' "${device:-Meta}"
}

omi_tun_device_present() {
  [[ -e $OMIHOMO_NET_DIR/$1 ]]
}

# The kernel takes the adapter down a moment after mihomo closes it, and mihomo
# does not wait for that: a rebuild started inside the window fails outright.
omi_await_tun_release() {
  local device=$1 attempt
  for attempt in {1..40}; do
    omi_tun_device_present "$device" || return 0
    sleep 0.05
  done
  return 1
}

omi_put_runtime() {
  local path=$1 payload
  payload=$(jq -cn --arg path "$path" '{path: $path, payload: ""}')
  omi_api_curl '/configs?force=true' --max-time "$OMIHOMO_RELOAD_TIMEOUT" -X PUT \
    -H 'Content-Type: application/json' --data "$payload" >/dev/null
}

# mihomo answers a load whose adapter then failed to start with 200, and the
# old adapter can outlive it in sysfs, so only the controller can say whether
# TUN is actually running.
omi_tun_running() {
  [[ $(omi_api_curl /configs --max-time 2 | jq -r '.tun.enable // false') == true ]]
}

omi_tun_enabled() {
  [[ $(omi_yq -r '.tun.enable // false' "$1") == true ]]
}

# Everything mihomo builds its adapter from. It keeps a live adapter untouched
# through any reload that leaves these alone.
omi_tun_signature() {
  omi_yq -o=json -I=0 '{"tun": .tun, "fake-ip-range": .dns."fake-ip-range"}' "$1"
}

# mihomo cannot rebuild a TUN adapter over one that is still up. A reload that
# changes a live adapter answers 200, logs "configure tun interface: device or
# resource busy", and leaves the machine with no tunnel, which is the panel's
# degraded state, and why switching TUN off and on by hand fixes it.
#
# So only a load that changes a live adapter tears it down, with a first pass
# that has TUN disabled: the manual repair, done in the order that never leaves
# a device behind. Every other load, a rule change included, is one pass that
# mihomo applies without touching the adapter. `previous` is the config the
# core holds now. Only a load that ends with a reachable controller and, when
# asked for, a running TUN counts as done.
omi_load_runtime() {
  local path=$1 previous=$2 staged status=0
  if [[ -f $previous ]] && omi_tun_enabled "$previous" && omi_tun_enabled "$path" &&
    [[ $(omi_tun_signature "$path") != "$(omi_tun_signature "$previous")" ]]; then
    staged=$(mktemp "${OMIHOMO_DATA_DIR}/.runtime.XXXXXX")
    omi_yq '.tun.enable = false' "$path" >"$staged" &&
      omi_put_runtime "$staged" &&
      omi_await_tun_release "$(omi_tun_device_name "$previous")" || status=1
    rm -f "$staged"
    ((status == 0)) || return 1
  fi
  omi_put_runtime "$path" && omi_api_reachable && { ! omi_tun_enabled "$path" || omi_tun_running; }
}

# A load that fails halfway can leave the core on a config nobody asked for,
# typically the TUN-less first pass. The runtime on disk is still the last one
# that worked, so a failed load puts that back before reporting the failure.
omi_reload_runtime() {
  local runtime_path=${1:-$OMIHOMO_RUNTIME_FILE}
  omi_unit_active || return 0
  if ! omi_api_reachable; then
    omi_error "mihomo controller is unreachable" 12
    return 12
  fi
  omi_load_runtime "$runtime_path" "$OMIHOMO_RUNTIME_FILE" && return 0
  if [[ $runtime_path != "$OMIHOMO_RUNTIME_FILE" && -f $OMIHOMO_RUNTIME_FILE ]] &&
    omi_load_runtime "$OMIHOMO_RUNTIME_FILE" "$runtime_path"; then
    omi_error "mihomo rejected the runtime config" 12
    return 12
  fi
  omi_error "mihomo failed to reload and the previous config could not be restored" 12
  return 12
}

# Resolve the same primary group the panel shows. GLOBAL is mihomo's system
# group, so only subscription groups can become Omihomo's primary.
omi_resolve_primary_group() {
  local source=$1 override=$2 configured
  configured=$(omi_yq -r '.omihomo."primary-group" // ""' "$override")
  OMIHOMO_CONFIGURED_PRIMARY=$configured omi_yq -r '
    [."proxy-groups"[]? | select(.name != "GLOBAL")] as $groups |
    (strenv(OMIHOMO_CONFIGURED_PRIMARY)) as $wanted |
    (($groups | map(select(.name == $wanted)) | .[0].name) //
     ($groups | map(select(.type == "select")) | .[0].name) //
     ($groups[0].name // ""))
  ' "$source"
}

omi_merge_runtime() {
  local source=$1 override=$2 destination=$3 primary
  primary=$(omi_resolve_primary_group "$source" "$override")
  OMIHOMO_PRIMARY_GROUP=$primary \
  OMIHOMO_TAILSCALE_LISTENER=$OMIHOMO_TAILSCALE_LISTENER \
  OMIHOMO_TAILSCALE_PORT=$OMIHOMO_TAILSCALE_PORT \
  OMIHOMO_TAILSCALE_DNS=$OMIHOMO_TAILSCALE_DNS \
  OMIHOMO_TAILNET_RANGE=$OMIHOMO_TAILNET_RANGE \
  OMIHOMO_TAILNET_TUN_RULE_INDEX=$OMIHOMO_TAILNET_TUN_RULE_INDEX \
  OMIHOMO_MIXED_PORT=$OMIHOMO_MIXED_PORT \
  OMIHOMO_DEFAULT_NAMESERVERS_YAML=$(omi_default_nameservers_yaml) \
  OMIHOMO_PROXY_NAMESERVERS_YAML=$(omi_default_nameservers_yaml GLOBAL) \
  OMIHOMO_CONNECTIVITY_HOST=$(omi_connectivity_check_host) \
    omi_yq eval-all -P '
    select(fileIndex == 0) as $base |
    select(fileIndex == 1) as $override |
    ($override.config // {}) as $config |
    ($override.rules.prepend // []) as $prepend |
    ($override.rules.append // []) as $append |
    ($override.rules.filter // []) as $filter |
    ($override.omihomo.tailscale) as $tailscale |
    ($override.omihomo."tailnet-proxy") as $tailnet |
    ($base | {
      "mixed-port": (strenv(OMIHOMO_MIXED_PORT) | to_number),
      "dns": {
        "enable": true,
        "default-nameserver": env(OMIHOMO_DEFAULT_NAMESERVERS_YAML),
        "proxy-server-nameserver": env(OMIHOMO_DEFAULT_NAMESERVERS_YAML),
        "nameserver": env(OMIHOMO_PROXY_NAMESERVERS_YAML)
      }
    }) as $defaults |
    ($defaults * $base * $config) as $merged |
    (($merged.rules // []) | map(select(. as $rule | ($filter | contains([$rule]) | not)))) as $base_rules |
    ($base_rules + $append) as $rest |
    (($merged."proxy-groups" // []) | map(select(.name != "GLOBAL"))) as $groups |
    strenv(OMIHOMO_CONNECTIVITY_HOST) as $connectivity |
    $merged |
    .dns."fake-ip-filter" = (((.dns."fake-ip-filter" // []) | map(select(. != $connectivity))) +
      ([$connectivity] | map(select(. != "")))) |
    del(.dns."fake-ip-filter" | select(length == 0)) |
    .rules = ($prepend + ([
        "IP-CIDR," + strenv(OMIHOMO_TAILNET_RANGE) + ",GLOBAL,no-resolve",
        "DOMAIN-SUFFIX,ts.net,GLOBAL"
      ] | map(select(($tailnet == true) and (strenv(OMIHOMO_PRIMARY_GROUP) != "")))
      ) + ([
        "IP-CIDR,100.64.0.0/10,DIRECT,no-resolve",
        "IP-CIDR,fd7a:115c:a1e0::/48,DIRECT,no-resolve",
        "DOMAIN-SUFFIX,tailscale.com,GLOBAL",
        "DOMAIN-SUFFIX,tailscale.io,GLOBAL"
      ] | map(select(($tailscale == true) and (strenv(OMIHOMO_PRIMARY_GROUP) != "")))
      ) + $rest) |
    .listeners = (((.listeners // []) | map(select(.name != strenv(OMIHOMO_TAILSCALE_LISTENER)))) + ([{
        "name": strenv(OMIHOMO_TAILSCALE_LISTENER),
        "type": "mixed",
        "listen": "127.0.0.1",
        "port": (strenv(OMIHOMO_TAILSCALE_PORT) | to_number),
        "udp": false,
        "users": []
      }] | map(select($tailscale == true)))) |
    del(.listeners | select(length == 0)) |
    with(.tun | select($tailnet == true);
      ."route-exclude-address" = (((."route-exclude-address" // []) |
        map(select(. != strenv(OMIHOMO_TAILNET_RANGE)))) + [strenv(OMIHOMO_TAILSCALE_DNS) + "/32"]) |
      ."iproute2-rule-index" = (strenv(OMIHOMO_TAILNET_TUN_RULE_INDEX) | to_number)) |
    ."proxy-groups" = ((
      [{"name": "GLOBAL", "type": "select", "proxies": [strenv(OMIHOMO_PRIMARY_GROUP)]}] |
        map(select(strenv(OMIHOMO_PRIMARY_GROUP) != ""))
      ) + $groups)
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
    if [[ ! -f $cache ]]; then
      rm -f "$candidate"
      omi_error "active subscription cache is missing" 13
      return 13
    fi
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
