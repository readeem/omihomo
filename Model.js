// Pure data helpers for the Omihomo panel. Everything here either parses a
// CLI or mihomo API payload, or formats a value for display; nothing touches
// QML types, so tests/model_test.mjs runs the same functions under node.

var RULE_TYPES = ["DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "PROCESS-NAME"]
var MODES = ["rule", "global", "direct"]

// Group types mihomo reports. Only Selector lets the user choose a config;
// the rest pick for themselves and are shown read-only.
var GROUP_TYPES = ["Selector", "URLTest", "Fallback", "LoadBalance", "Relay"]

function text(value) {
  return value === null || value === undefined ? "" : String(value)
}

function number(value, fallback) {
  var n = Number(value)
  return isFinite(n) ? n : (fallback === undefined ? 0 : fallback)
}

function parseJson(raw, fallback) {
  var body = text(raw).trim()
  if (body === "") return fallback
  try {
    return JSON.parse(body)
  } catch (e) {
    return fallback
  }
}

// ---------------------------------------------------------------- CLI

function defaultStatus() {
  return {
    state: "unknown",
    detail: "",
    activeSubscription: "",
    primaryGroup: "",
    tunEnabled: false,
    startedMs: 0
  }
}

// `omihomo status` is the one verb that always exits 0 and always returns the
// same object, so an unparseable payload means the CLI itself is broken.
function parseStatus(raw) {
  var data = parseJson(raw, null)
  if (!data || typeof data !== "object") return defaultStatus()
  return {
    state: text(data.state) || "unknown",
    detail: text(data.detail),
    activeSubscription: text(data.active_subscription),
    primaryGroup: text(data.primary_group),
    tunEnabled: data.tun_enabled === true,
    startedMs: parseUnitTimestamp(data.uptime)
  }
}

function stateLabel(state) {
  if (state === "on") return "On"
  if (state === "degraded") return "Degraded"
  if (state === "starting") return "Starting"
  if (state === "stopped") return "Off"
  if (state === "not-installed") return "Not installed"
  return "Unknown"
}

// Errors arrive on stderr as {"error":"...","code":N}; anything else is a
// crash or a usage message, and shows verbatim.
function cliError(stderr, exitCode) {
  var code = number(exitCode, 1)
  var data = parseJson(stderr, null)
  if (data && typeof data === "object" && data.error !== undefined) {
    return { message: text(data.error), code: number(data.code, code) }
  }
  var raw = text(stderr).replace(/\s+/g, " ").trim()
  return { message: raw !== "" ? raw : errorHint(code), code: code }
}

// The fixed exit-code table from docs/cli.md, as panel-facing copy.
function errorHint(code) {
  switch (number(code, 1)) {
  case 10: return "mihomo is not installed"
  case 11: return "mihomo is not running"
  case 12: return "mihomo controller is unreachable"
  case 13: return "No active subscription"
  case 20: return "mihomo rejected the YAML"
  case 21: return "Subscription fetch failed"
  case 0: return ""
  default: return "Command failed"
  }
}

function parseSubscriptions(raw) {
  var data = parseJson(raw, [])
  if (!data || typeof data.length !== "number") return []
  var out = []
  for (var i = 0; i < data.length; i++) {
    var entry = data[i]
    if (!entry || text(entry.name) === "") continue
    out.push({
      name: text(entry.name),
      url: text(entry.url),
      updatedAt: text(entry.updated_at),
      upload: entry.upload === null ? null : number(entry.upload, null),
      download: entry.download === null ? null : number(entry.download, null),
      total: entry.total === null ? null : number(entry.total, null),
      expire: entry.expire === null ? null : number(entry.expire, null),
      active: entry.active === true
    })
  }
  return out
}

// One dense line under a subscription name: what it has spent of its quota,
// when it expires, and how stale the cache is. Missing fields drop out.
function subscriptionDetail(sub, nowMs) {
  if (!sub) return ""
  var parts = []
  var used = number(sub.upload, 0) + number(sub.download, 0)
  if (sub.total !== null && sub.total !== undefined && number(sub.total, 0) > 0) {
    parts.push(formatBytes(used) + " of " + formatBytes(sub.total))
  } else if (used > 0) {
    parts.push(formatBytes(used) + " used")
  }
  if (sub.expire !== null && sub.expire !== undefined && number(sub.expire, 0) > 0) {
    parts.push("expires " + formatDate(number(sub.expire, 0) * 1000))
  }
  var age = relativeTime(sub.updatedAt, nowMs)
  if (age !== "") parts.push("updated " + age)
  return parts.join(" · ")
}

function parseRules(raw) {
  var data = parseJson(raw, [])
  if (!data || typeof data.length !== "number") return []
  var out = []
  for (var i = 0; i < data.length; i++) {
    var entry = data[i]
    if (!entry) continue
    var parts = ruleParts(entry.rule)
    out.push({
      index: number(entry.index, i + 1),
      kind: text(entry.kind) || "prepend",
      rule: text(entry.rule),
      type: parts.type,
      value: parts.value,
      target: parts.target
    })
  }
  return out
}

function ruleParts(rule) {
  var parts = text(rule).split(",")
  return {
    type: text(parts[0]).trim(),
    value: parts.length > 1 ? text(parts[1]).trim() : "",
    target: parts.length > 2 ? text(parts[2]).trim() : ""
  }
}

// Mirrors the CLI's per-type validation so the form can refuse a rule before
// spending a subprocess on it. `mihomo -t` at activation is still the real gate.
function validateRule(type, value, target) {
  if (text(target) === "") return "Pick a target"
  var v = text(value).trim()
  if (v === "") return "Value is required"
  if (type === "IP-CIDR") {
    return isCidr(v) ? "" : "Not a valid CIDR"
  }
  if (type === "DOMAIN-SUFFIX" || type === "DOMAIN-KEYWORD") {
    return /[\s/]/.test(v) ? "No spaces or slashes" : ""
  }
  if (type === "PROCESS-NAME") return ""
  return "Unsupported rule type"
}

function isCidr(value) {
  var v = text(value)
  var v4 = v.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/)
  if (v4) {
    for (var i = 1; i <= 4; i++) if (number(v4[i], 256) > 255) return false
    return number(v4[5], 33) <= 32
  }
  var v6 = v.match(/^([0-9A-Fa-f:]+)\/(\d{1,3})$/)
  if (!v6) return false
  return v6[1].indexOf(":") !== -1 && number(v6[2], 129) <= 128
}

// ---------------------------------------------------------------- mihomo API

function parseApiInfo(raw) {
  var data = parseJson(raw, null)
  if (!data || typeof data !== "object") return { address: "", secret: "" }
  return { address: text(data.address), secret: text(data.secret) }
}

function parseConfigs(raw) {
  var data = parseJson(raw, null)
  if (!data || typeof data !== "object") return { mode: "", mixedPort: 0, port: 0 }
  return {
    mode: text(data.mode).toLowerCase(),
    mixedPort: number(data["mixed-port"], 0),
    port: number(data.port, 0)
  }
}

// `GET /proxies` is a flat namespace of configs and groups keyed by name.
// Groups are the entries carrying an `all` list; everything else is a config.
function parseProxies(raw) {
  var data = parseJson(raw, null)
  var proxies = data && typeof data === "object" ? data.proxies : null
  var groups = []
  var configs = {}
  if (!proxies || typeof proxies !== "object") return { groups: groups, configs: configs }
  for (var name in proxies) {
    var entry = proxies[name]
    if (!entry || typeof entry !== "object") continue
    if (entry.all && typeof entry.all.length === "number") {
      groups.push({
        name: text(entry.name) || text(name),
        type: text(entry.type),
        now: text(entry.now),
        all: entry.all.slice(),
        selectable: text(entry.type) === "Selector"
      })
    } else {
      configs[text(name)] = entry
    }
  }
  groups.sort(function(a, b) { return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0) })
  return { groups: groups, configs: configs }
}

// The primary group is an override-layer setting; when it is unset or points
// at a group this subscription does not declare, fall back to the first
// selectable group, which is what the CLI defaults to.
function primaryGroupName(groups, configured) {
  var wanted = text(configured)
  var i
  for (i = 0; i < groups.length; i++) if (groups[i].name === wanted) return wanted
  for (i = 0; i < groups.length; i++) if (groups[i].selectable) return groups[i].name
  return groups.length > 0 ? groups[0].name : ""
}

function groupByName(groups, name) {
  for (var i = 0; i < groups.length; i++) if (groups[i].name === text(name)) return groups[i]
  return null
}

// Last delay mihomo recorded for a config, 0 when it has never been tested.
function historyDelay(entry) {
  if (!entry || !entry.history || typeof entry.history.length !== "number") return 0
  var last = entry.history[entry.history.length - 1]
  return last ? number(last.delay, 0) : 0
}

function parseDelay(raw) {
  var data = parseJson(raw, null)
  if (!data || typeof data !== "object") return null
  if (data.delay === undefined) return null
  return number(data.delay, 0)
}

function parseGroupDelay(raw) {
  var data = parseJson(raw, null)
  var out = {}
  if (!data || typeof data !== "object") return out
  for (var name in data) out[name] = number(data[name], 0)
  return out
}

// `/traffic` streams one JSON object per line; a partial line parses to null
// and is dropped rather than resetting the readout to zero.
function parseTraffic(line) {
  var data = parseJson(line, null)
  if (!data || typeof data !== "object") return null
  if (data.up === undefined && data.down === undefined) return null
  return { up: number(data.up, 0), down: number(data.down, 0) }
}

// One `curl -w '\n%{time_total}'` against cdn-cgi/trace serves both the egress
// IP and the round trip through the selected config.
function parseTrace(raw) {
  var body = text(raw)
  var ip = ""
  var match = body.match(/^ip=(.+)$/m)
  if (match) ip = text(match[1]).trim()
  var latency = 0
  var lines = body.split("\n")
  for (var i = lines.length - 1; i >= 0; i--) {
    var line = text(lines[i]).trim()
    if (line === "") continue
    if (/^\d+([.,]\d+)?$/.test(line)) latency = Math.round(parseFloat(line.replace(",", ".")) * 1000)
    break
  }
  return { ip: ip, latency: latency }
}

function parseConnections(raw, nowMs) {
  var data = parseJson(raw, null)
  var out = { downloadTotal: 0, uploadTotal: 0, items: [] }
  if (!data || typeof data !== "object") return out
  out.downloadTotal = number(data.downloadTotal, 0)
  out.uploadTotal = number(data.uploadTotal, 0)
  var list = data.connections
  if (!list || typeof list.length !== "number") return out
  for (var i = 0; i < list.length; i++) {
    var entry = list[i]
    if (!entry) continue
    var metadata = entry.metadata || {}
    var chains = entry.chains && typeof entry.chains.length === "number" ? entry.chains : []
    var started = Date.parse(text(entry.start))
    out.items.push({
      id: text(entry.id),
      host: connectionHost(metadata),
      network: text(metadata.network).toUpperCase(),
      process: baseName(metadata.processPath),
      chain: chains.length > 0 ? text(chains[0]) : "",
      rule: text(entry.rule) + (text(entry.rulePayload) !== "" ? "(" + text(entry.rulePayload) + ")" : ""),
      upload: number(entry.upload, 0),
      download: number(entry.download, 0),
      durationMs: isFinite(started) ? Math.max(0, number(nowMs, Date.now()) - started) : 0
    })
  }
  out.items.sort(function(a, b) { return a.durationMs - b.durationMs })
  return out
}

function connectionHost(metadata) {
  var host = text(metadata.host)
  if (host === "") host = text(metadata.destinationIP)
  var port = text(metadata.destinationPort)
  if (host === "") return port === "" ? "unknown" : ":" + port
  return port === "" ? host : host + ":" + port
}

function baseName(path) {
  var parts = text(path).split("/")
  return text(parts[parts.length - 1])
}

// `GET /rules` returns the merged list with no marker for ours. Our own rules
// are prepended, so peel matching entries off the head and show the rest as
// the subscription's — that is the dimmed, read-only view.
function parseApiRules(raw) {
  var data = parseJson(raw, null)
  var list = data && typeof data === "object" ? data.rules : null
  var out = []
  if (!list || typeof list.length !== "number") return out
  for (var i = 0; i < list.length; i++) {
    var entry = list[i]
    if (!entry) continue
    out.push({
      type: apiRuleType(entry.type),
      value: text(entry.payload),
      target: text(entry.proxy)
    })
  }
  return out
}

function stripOwnRules(apiRules, ownRules) {
  var own = []
  var i
  for (i = 0; i < ownRules.length; i++) {
    if (text(ownRules[i].kind) === "prepend") own.push(ownRules[i])
  }
  var head = 0
  for (i = 0; i < own.length && head < apiRules.length; i++) {
    var mine = own[i]
    var theirs = apiRules[head]
    if (text(theirs.value) === text(mine.value) && text(theirs.target) === text(mine.target)) head++
  }
  return apiRules.slice(head)
}

// mihomo reports rule types as CamelCase ("DomainSuffix"); the config syntax
// the user writes is upper-kebab ("DOMAIN-SUFFIX").
function apiRuleType(value) {
  var raw = text(value)
  if (raw === "") return ""
  if (/^IPCIDR6?$/i.test(raw)) return raw.toUpperCase().replace("IPCIDR", "IP-CIDR")
  if (/^GeoIP$/i.test(raw)) return "GEOIP"
  return raw.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toUpperCase()
}

// Parameters mihomo actually states for a config. Address, port, and
// credentials are deliberately absent: research ticket #4 found no reliable
// mapping from a runtime name back to its provider entry, so Omihomo renders
// only what it can resolve confidently.
function configParameters(entry) {
  var out = []
  if (!entry || typeof entry !== "object") return out
  if (text(entry.type) !== "") out.push({ label: "Type", value: text(entry.type) })
  if (entry.udp !== undefined) out.push({ label: "UDP", value: entry.udp === true ? "yes" : "no" })
  if (entry.xudp === true) out.push({ label: "XUDP", value: "yes" })
  if (entry.tfo === true) out.push({ label: "TFO", value: "yes" })
  if (entry.alive !== undefined) out.push({ label: "Alive", value: entry.alive === true ? "yes" : "no" })
  var delay = historyDelay(entry)
  if (delay > 0) out.push({ label: "Latency", value: formatDelay(delay) })
  return out
}

// ---------------------------------------------------------------- formatting

function formatBytes(bytes) {
  var n = number(bytes, 0)
  if (n < 1000) return Math.round(n) + " B"
  var units = ["kB", "MB", "GB", "TB", "PB"]
  var value = n
  var unit = -1
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000
    unit++
  }
  return (value >= 100 ? Math.round(value) : Math.round(value * 10) / 10) + " " + units[unit]
}

function formatRate(bytesPerSecond) {
  return formatBytes(bytesPerSecond) + "/s"
}

function formatDuration(ms) {
  var seconds = Math.floor(number(ms, 0) / 1000)
  if (seconds < 60) return seconds + "s"
  var minutes = Math.floor(seconds / 60)
  if (minutes < 60) return minutes + "m"
  var hours = Math.floor(minutes / 60)
  if (hours < 24) return hours + "h " + (minutes % 60) + "m"
  return Math.floor(hours / 24) + "d " + (hours % 24) + "h"
}

function formatDelay(ms) {
  var n = number(ms, 0)
  return n > 0 ? n + " ms" : "—"
}

var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

function formatDate(ms) {
  var n = number(ms, 0)
  if (n <= 0) return ""
  var date = new Date(n)
  return date.getDate() + " " + MONTHS[date.getMonth()]
}

function relativeTime(iso, nowMs) {
  var then = Date.parse(text(iso))
  if (!isFinite(then)) return ""
  var delta = number(nowMs, Date.now()) - then
  if (delta < 0) return "just now"
  var minutes = Math.floor(delta / 60000)
  if (minutes < 1) return "just now"
  if (minutes < 60) return minutes + "m ago"
  var hours = Math.floor(minutes / 60)
  if (hours < 24) return hours + "h ago"
  return Math.floor(hours / 24) + "d ago"
}

// systemd prints ActiveEnterTimestamp in local time with a weekday and a zone
// abbreviation JS cannot parse, so read the calendar fields out of it.
function parseUnitTimestamp(value) {
  var match = text(value).match(/(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})/)
  if (!match) return 0
  var date = new Date(number(match[1], 0), number(match[2], 1) - 1, number(match[3], 1),
    number(match[4], 0), number(match[5], 0), number(match[6], 0))
  var ms = date.getTime()
  return isFinite(ms) ? ms : 0
}

function nextMode(mode) {
  var index = MODES.indexOf(text(mode).toLowerCase())
  return MODES[(index < 0 ? 0 : index + 1) % MODES.length]
}

// Case-insensitive substring match, so the config filter behaves like every
// other filter in the shell.
function filterNames(names, query) {
  var needle = text(query).trim().toLowerCase()
  if (needle === "") return names.slice()
  var out = []
  for (var i = 0; i < names.length; i++) {
    if (text(names[i]).toLowerCase().indexOf(needle) !== -1) out.push(names[i])
  }
  return out
}

if (typeof module !== "undefined") {
  module.exports = {
    RULE_TYPES: RULE_TYPES,
    MODES: MODES,
    GROUP_TYPES: GROUP_TYPES,
    parseJson: parseJson,
    defaultStatus: defaultStatus,
    parseStatus: parseStatus,
    stateLabel: stateLabel,
    cliError: cliError,
    errorHint: errorHint,
    parseSubscriptions: parseSubscriptions,
    subscriptionDetail: subscriptionDetail,
    parseRules: parseRules,
    ruleParts: ruleParts,
    validateRule: validateRule,
    isCidr: isCidr,
    parseApiInfo: parseApiInfo,
    parseConfigs: parseConfigs,
    parseProxies: parseProxies,
    primaryGroupName: primaryGroupName,
    groupByName: groupByName,
    historyDelay: historyDelay,
    parseDelay: parseDelay,
    parseGroupDelay: parseGroupDelay,
    parseTraffic: parseTraffic,
    parseTrace: parseTrace,
    parseConnections: parseConnections,
    connectionHost: connectionHost,
    parseApiRules: parseApiRules,
    stripOwnRules: stripOwnRules,
    apiRuleType: apiRuleType,
    configParameters: configParameters,
    formatBytes: formatBytes,
    formatRate: formatRate,
    formatDuration: formatDuration,
    formatDelay: formatDelay,
    formatDate: formatDate,
    relativeTime: relativeTime,
    parseUnitTimestamp: parseUnitTimestamp,
    nextMode: nextMode,
    filterNames: filterNames
  }
}
