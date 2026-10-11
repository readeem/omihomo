// Pure data helpers for the Omihomo panel. Everything here either parses a
// CLI or mihomo API payload, or formats a value for display; nothing touches
// QML types, so tests/model_test.mjs runs the same functions under node.

var RULE_TYPES = ["DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "PROCESS-NAME"]
var MODES = ["rule", "global", "direct"]

// Group types mihomo reports. Only Selector lets the user choose a config;
// the rest pick for themselves and are shown read-only.
var GROUP_TYPES = ["Selector", "URLTest", "Fallback", "LoadBalance", "Relay"]

function isSystemGroup(group) {
  return !!group && (group.system === true || text(group.name) === "GLOBAL")
}

function text(value) {
  return value === null || value === undefined ? "" : String(value)
}

function number(value, fallback) {
  var n = Number(value)
  return isFinite(n) ? n : (fallback === undefined ? 0 : fallback)
}

// A dictionary keyed by remote names, so a name like `__proto__` stays data.
function dict() {
  return Object.create(null)
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
    tunRedirect: false,
    autostartEnabled: false,
    permissionsOk: false,
    hardeningPending: false,
    tailscaleEnabled: false,
    tailscalePresent: false,
    tailnetProxy: false,
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
    tunRedirect: data.tun_redirect === true,
    autostartEnabled: data.autostart_enabled === true,
    permissionsOk: data.permissions_ok === true,
    hardeningPending: data.hardening_pending === true,
    tailscaleEnabled: data.tailscale_enabled === true,
    tailscalePresent: data.tailscale_present === true,
    tailnetProxy: data.tailnet_proxy === true,
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

// What a subscription has spent of its quota and when it expires, for the
// line under its name. `fraction` is -1 when the provider states no quota.
function subscriptionUsage(sub) {
  var out = { fraction: -1, text: "" }
  if (!sub) return out
  var parts = []
  var used = number(sub.upload, 0) + number(sub.download, 0)
  var total = number(sub.total, 0)
  if (sub.total !== null && sub.total !== undefined && total > 0) {
    out.fraction = Math.max(0, Math.min(1, used / total))
    parts.push(formatBytes(used) + " of " + formatBytes(total))
  } else if (used > 0) {
    parts.push(formatBytes(used) + " used")
  }
  if (sub.expire !== null && sub.expire !== undefined && number(sub.expire, 0) > 0) {
    parts.push("expires " + formatDate(number(sub.expire, 0) * 1000))
  }
  out.text = parts.length > 0 ? parts.join(" · ") : "no quota reported"
  return out
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
  if ((v + target).indexOf(",") !== -1) return "No commas"
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
  if (!data || typeof data !== "object") data = {}
  var tun = data.tun || {}
  return {
    mode: text(data.mode).toLowerCase(),
    mixedPort: number(data["mixed-port"], 0),
    port: number(data.port, 0),
    routing: {
      tunEnabled: tun.enable === true,
      tunAutoRoute: tun["auto-route"] === true,
      routeExcludeAddresses: Array.isArray(tun["route-exclude-address"])
        ? tun["route-exclude-address"].filter(function(cidr) { return typeof cidr === "string" }) : []
    }
  }
}

// `GET /proxies` exposes groups and direct proxy entries. Provider-backed
// configs may only appear in a group's `all` list, so callers must not assume
// every visible config also has a top-level API entry.
function parseProxies(raw) {
  var data = parseJson(raw, null)
  var proxies = data && typeof data === "object" ? data.proxies : null
  var groups = []
  var configs = dict()
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
        selectable: text(entry.type) === "Selector",
        system: (text(entry.name) || text(name)) === "GLOBAL"
      })
    } else {
      configs[text(name)] = entry
    }
  }
  groups.sort(function(a, b) { return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0) })
  return { groups: groups, configs: configs }
}

// Everything a rule can send traffic to, as dropdown options: the built-ins,
// then the groups, then every config the groups list, once each. A name with a
// comma cannot be written into a rule, so it is left out.
function ruleTargets(groups) {
  var out = [{ value: "DIRECT", label: "DIRECT", description: "built-in" },
    { value: "REJECT", label: "REJECT", description: "built-in" }]
  var seen = new Set(["DIRECT", "REJECT"])
  var i
  function add(name, description) {
    if (seen.has(name) || name.indexOf(",") !== -1) return
    seen.add(name)
    out.push({ value: name, label: name, description: description })
  }
  for (i = 0; i < groups.length; i++) add(text(groups[i].name), "group")
  for (i = 0; i < groups.length; i++) {
    var all = groups[i].all || []
    for (var j = 0; j < all.length; j++) add(text(all[j]), "config")
  }
  return out
}

function userGroups(groups) {
  var visible = []
  for (var i = 0; i < groups.length; i++) {
    if (!isSystemGroup(groups[i])) visible.push(groups[i])
  }
  return visible
}

// GLOBAL is runtime plumbing, never a primary group. A stale explicit setting
// falls through to the same subscription-owned default as an empty setting.
function primaryGroupName(groups, configured) {
  var wanted = text(configured)
  var i
  for (i = 0; i < groups.length; i++) {
    if (groups[i].name === wanted && !isSystemGroup(groups[i])) return wanted
  }
  for (i = 0; i < groups.length; i++) {
    if (groups[i].selectable && !isSystemGroup(groups[i])) return groups[i].name
  }
  for (i = 0; i < groups.length; i++) {
    if (!isSystemGroup(groups[i])) return groups[i].name
  }
  return ""
}

function groupByName(groups, name) {
  for (var i = 0; i < groups.length; i++) if (groups[i].name === text(name)) return groups[i]
  return null
}

function groupForConfig(groups, name) {
  var wanted = text(name)
  for (var i = 0; i < groups.length; i++) {
    var all = groups[i] && groups[i].all
    if (!all || typeof all.length !== "number") continue
    for (var j = 0; j < all.length; j++) {
      if (text(all[j]) === wanted) return text(groups[i].name)
    }
  }
  return ""
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
  var out = dict()
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

// `GET /connections` only ever states what is open right now, so a panel that
// renders it directly loses every connection the moment it finishes and
// repaints its whole list on each poll. The three functions below turn that
// snapshot into a log instead: `parseConnections` reads one snapshot,
// `mergeConnectionLog` folds it into what came before, and `stackConnections`
// collapses the result into the rows the view draws.
function parseConnections(raw, nowMs) {
  var data = parseJson(raw, null)
  var now = number(nowMs, Date.now())
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
      process: baseName(metadata.processPath) || text(metadata.process)
        || (metadata.type === "Inner" ? "mihomo" : ""),
      inboundType: text(metadata.type),
      sourceIP: text(metadata.sourceIP),
      sourcePort: number(metadata.sourcePort, 0),
      destinationIP: text(metadata.destinationIP),
      destinationPort: number(metadata.destinationPort, 0),
      chain: chains.length > 0 ? text(chains[0]) : "",
      rule: text(entry.rule) + (text(entry.rulePayload) !== "" ? "(" + text(entry.rulePayload) + ")" : ""),
      upload: number(entry.upload, 0),
      download: number(entry.download, 0),
      startMs: isFinite(started) ? started : now,
      open: true,
      lastSeenMs: now,
      closedAtMs: 0
    })
  }
  return out
}

/** Fills missing TUN process names from unique sockets on a local address. */
function enrichConnections(items, table) {
  var addresses = table && Array.isArray(table.addresses) ? table.addresses : []
  var sockets = table && Array.isArray(table.sockets) ? table.sockets : []
  return items.map(function(entry) {
    if (entry.process !== "" || entry.inboundType !== "Tun" || entry.sourcePort <= 0) return entry
    var source = socketAddress(entry.sourceIP)
    if (addresses.map(socketAddress).indexOf(source) < 0) return entry
    var matches = sockets.filter(function(socket) {
      if (text(socket.network).toUpperCase() !== entry.network || socket.localPort !== entry.sourcePort) return false
      var local = socketAddress(socket.localIP)
      var wildcard = local === "*" || (local === "0.0.0.0" && ipv4Number(source) !== null)
        || (local === "::" && source.indexOf(":") >= 0)
      if (local !== source && !wildcard) return false
      if (entry.network === "TCP" && socket.peerPort === 0) return false
      return socket.peerPort === 0 || (socket.peerPort === entry.destinationPort
        && socketAddress(socket.peerIP) === socketAddress(entry.destinationIP))
    })
    if (matches.length !== 1 || text(matches[0].process) === "") return entry
    var enriched = {}
    for (var key in entry) enriched[key] = entry[key]
    enriched.process = text(matches[0].process)
    return enriched
  })
}

function socketAddress(value) {
  return text(value).replace(/^\[|\]$/g, "").replace(/^::ffff:/i, "").toLowerCase()
}

// Fold one snapshot into the log. An id that is still there keeps its original
// start and takes the fresh figures; an id that was open and is no longer in
// the snapshot is closed, keeping the last figures it reported. It closes at
// `lastSeenMs` rather than now, because a poll can only prove a connection was
// already gone by the time it looked, and between two polls — or across a
// stretch with the panel shut — that gap is the whole story.
function mergeConnectionLog(log, items, nowMs, cap) {
  var now = number(nowMs, Date.now())
  var previous = log && typeof log.length === "number" ? log : []
  var fresh = items && typeof items.length === "number" ? items : []
  var incoming = dict()
  var i
  for (i = 0; i < fresh.length; i++) incoming[fresh[i].id] = fresh[i]

  var out = []
  var kept = dict()
  for (i = 0; i < previous.length; i++) {
    var entry = previous[i]
    var update = incoming[entry.id]
    kept[entry.id] = true
    if (update) {
      update.startMs = entry.startMs
      if (update.process === "" && update.network === entry.network
        && update.sourceIP === entry.sourceIP && update.sourcePort === entry.sourcePort
        && update.destinationIP === entry.destinationIP && update.destinationPort === entry.destinationPort)
        update.process = entry.process
      out.push(update)
    } else if (entry.open) {
      out.push(closedEntry(entry, now))
    } else {
      out.push(entry)
    }
  }
  for (i = 0; i < fresh.length; i++) {
    if (!kept[fresh[i].id]) out.push(fresh[i])
  }
  return trimClosedConnections(out, number(cap, 200))
}

function closedEntry(entry, nowMs) {
  var closed = {}
  for (var field in entry) closed[field] = entry[field]
  closed.open = false
  closed.closedAtMs = entry.lastSeenMs > 0 ? entry.lastSeenMs : nowMs
  return closed
}

// Closed entries are the half of the log that only grows, so cap them: keep
// the most recently closed and drop the rest. Open ones are never dropped.
function trimClosedConnections(log, limit) {
  var closed = []
  var i
  for (i = 0; i < log.length; i++) if (!log[i].open) closed.push(log[i])
  if (closed.length <= limit) return log
  closed.sort(function(a, b) { return b.closedAtMs - a.closedAtMs })
  var keep = dict()
  for (i = 0; i < limit; i++) keep[closed[i].id] = true
  var out = []
  for (i = 0; i < log.length; i++) if (log[i].open || keep[log[i].id]) out.push(log[i])
  return out
}

// One row per process, destination, and state: the four sockets Firefox has
// open to github.com are one row reading x4, and the ones it has finished with
// are a second, dimmed row rather than the same row quietly changing meaning.
// Open stacks sort above closed ones, newest first within each, so a poll only
// ever updates a row in place or puts a new one at the top.
function stackConnections(log) {
  var list = log && typeof log.length === "number" ? log : []
  var byKey = dict()
  var stacks = []
  for (var i = 0; i < list.length; i++) {
    var entry = list[i]
    var key = connectionStackKey(entry)
    var stack = byKey[key]
    if (!stack) {
      stack = {
        key: key,
        host: entry.host,
        process: entry.process,
        network: entry.network,
        chain: entry.chain,
        rule: entry.rule,
        open: entry.open,
        count: 0,
        upload: 0,
        download: 0,
        startMs: entry.startMs,
        closedAtMs: entry.closedAtMs,
        ids: []
      }
      byKey[key] = stack
      stacks.push(stack)
    }
    stack.count++
    stack.upload += entry.upload
    stack.download += entry.download
    stack.ids.push(entry.id)
    // A stack is as old as its oldest member and as recent as its last close.
    if (entry.startMs < stack.startMs) stack.startMs = entry.startMs
    if (entry.closedAtMs > stack.closedAtMs) stack.closedAtMs = entry.closedAtMs
    if (stack.chain === "") stack.chain = entry.chain
    if (stack.rule === "") stack.rule = entry.rule
    if (stack.network === "") stack.network = entry.network
  }
  stacks.sort(function(a, b) {
    if (a.open !== b.open) return a.open ? -1 : 1
    return a.open ? b.startMs - a.startMs : b.closedAtMs - a.closedAtMs
  })
  return stacks
}

function connectionStackKey(entry) {
  return text(entry.process) + "\u0000" + text(entry.host) + "\u0000" + (entry.open ? "open" : "closed")
}

function openConnectionCount(log) {
  var list = log && typeof log.length === "number" ? log : []
  var open = 0
  for (var i = 0; i < list.length; i++) if (list[i].open) open++
  return open
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

// `GET /rules` returns the merged list with no marker for ours: the user's
// prepended rules, then the ones Omihomo injects, then the subscription's.
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

// Peels our prepended rules and then Omihomo's injected ones off the head of
// the merged list. A rule the runtime does not carry yet is skipped rather
// than swallowing a subscription rule.
function splitApiRules(apiRules, ownRules, injected) {
  var head = 0
  var i
  function peel(rule, typed) {
    var theirs = apiRules[head]
    if (!theirs || text(theirs.value) !== text(rule.value) || text(theirs.target) !== text(rule.target)) return
    if (!typed || text(theirs.type).replace("IP-CIDR6", "IP-CIDR") === text(rule.type)) head++
  }
  for (i = 0; i < ownRules.length; i++) {
    if (text(ownRules[i].kind) === "prepend") peel(ownRules[i], false)
  }
  var own = head
  for (i = 0; i < injected.length; i++) peel(ruleParts(injected[i]), true)
  return { injected: apiRules.slice(own, head), subscription: apiRules.slice(head) }
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

// ---------------------------------------------------------------- rule tester

var PRIVATE_CIDRS = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8",
  "169.254.0.0/16", "100.64.0.0/10"]

// A pasted URL, host:port, or bare host, reduced to the host mihomo matches on.
function ruleQueryHost(query) {
  return text(query).trim().toLowerCase()
    .replace(/^[a-z][a-z0-9+.-]*:\/\//, "")
    .replace(/^[^@/]*@/, "")
    .replace(/[/?#].*$/, "")
    .replace(/:\d+$/, "")
    .replace(/\.$/, "")
}

function ipv4Number(value) {
  var m = text(value).match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (!m) return null
  var n = 0
  for (var i = 1; i <= 4; i++) {
    var octet = number(m[i], 256)
    if (octet > 255) return null
    n = n * 256 + octet
  }
  return n
}

function inIpv4Cidr(ip, cidr) {
  var parts = text(cidr).split("/")
  var base = ipv4Number(parts[0])
  var bits = parts.length > 1 ? number(parts[1], -1) : 32
  if (base === null || bits < 0 || bits > 32 || bits !== Math.floor(bits)) return false
  var size = Math.pow(2, 32 - bits)
  return Math.floor(ip / size) === Math.floor(base / size)
}

// One rule against one host: "hit", "miss", or "unknown" when the answer
// depends on something a host string cannot tell — a rule set, a geo database,
// the process, or a DNS lookup that only the core would make.
function ruleVerdict(rule, host) {
  var type = text(rule && rule.type).toUpperCase()
  var value = text(rule && rule.value).toLowerCase()
  var ip = ipv4Number(host)
  switch (type) {
  case "MATCH":
  case "FINAL":
    return "hit"
  case "DOMAIN":
    return ip === null ? (host === value ? "hit" : "miss") : "miss"
  case "DOMAIN-SUFFIX":
    if (ip !== null) return "miss"
    return host === value || host.slice(-(value.length + 1)) === "." + value ? "hit" : "miss"
  case "DOMAIN-KEYWORD":
    return ip === null && host.indexOf(value) !== -1 ? "hit" : "miss"
  case "IP-CIDR":
  case "IP-CIDR6":
    if (ip === null) return "unknown"
    return type === "IP-CIDR" && inIpv4Cidr(ip, value) ? "hit" : "miss"
  case "GEOIP":
    if (ip === null) return "unknown"
    if (value === "private" || value === "lan") {
      for (var i = 0; i < PRIVATE_CIDRS.length; i++) if (inIpv4Cidr(ip, PRIVATE_CIDRS[i])) return "hit"
      return "miss"
    }
    return "unknown"
  }
  return "unknown"
}

/** Checks active IPv4 TUN exclusions, then reports the first locally evaluable rule hit. */
function testRules(rules, query, routing) {
  var host = ruleQueryHost(query)
  var out = { host: host, hit: -1, verdicts: [], unknown: 0, certain: false,
    bypassed: false, bypassCidr: "" }
  if (host === "") return out
  var ip = ipv4Number(host)
  if (ip !== null && routing && routing.tunEnabled && routing.tunAutoRoute) {
    var excludes = routing.routeExcludeAddresses || []
    for (var j = 0; j < excludes.length; j++) {
      if (inIpv4Cidr(ip, excludes[j])) {
        out.bypassed = true
        out.bypassCidr = excludes[j]
        out.certain = true
        return out
      }
    }
  }
  var list = rules && typeof rules.length === "number" ? rules : []
  for (var i = 0; i < list.length; i++) {
    var verdict = ruleVerdict(list[i], host)
    out.verdicts.push(verdict)
    if (verdict === "hit") {
      out.hit = i
      out.certain = out.unknown === 0
      return out
    }
    if (verdict === "unknown") out.unknown++
  }
  return out
}

// How many rules send traffic to each target, largest first.
function ruleTargetTally(rules) {
  var list = rules && typeof rules.length === "number" ? rules : []
  var counts = dict()
  var order = []
  for (var i = 0; i < list.length; i++) {
    var target = text(list[i].target)
    if (target === "") continue
    if (counts[target] === undefined) {
      counts[target] = 0
      order.push(target)
    }
    counts[target]++
  }
  var out = []
  for (var j = 0; j < order.length; j++) out.push({ target: order[j], count: counts[order[j]] })
  out.sort(function(a, b) { return b.count - a.count })
  return out
}

// Open traffic as process → config flows, for the connections diagram. Past
// `maxSources` processes or `maxTargets` configs the smallest fold into one
// "other" node, so the diagram always fits its box.
function connectionFlows(stacks, maxSources, maxTargets) {
  var list = stacks && typeof stacks.length === "number" ? stacks : []
  var bySource = dict()
  var byTarget = dict()
  var links = dict()
  var i
  for (i = 0; i < list.length; i++) {
    var stack = list[i]
    if (!stack.open) continue
    var bytes = number(stack.download, 0) + number(stack.upload, 0)
    var source = text(stack.process) || "unknown"
    var target = text(stack.chain) || "DIRECT"
    bySource[source] = (bySource[source] || 0) + bytes
    byTarget[target] = (byTarget[target] || 0) + bytes
    var key = source + "\u0000" + target
    links[key] = (links[key] || 0) + bytes
  }
  var sources = topNodes(bySource, maxSources)
  var targets = topNodes(byTarget, maxTargets)
  var merged = dict()
  for (var key2 in links) {
    var parts = key2.split("\u0000")
    var s = sources.names[parts[0]] ? parts[0] : "other"
    var t = targets.names[parts[1]] ? parts[1] : "other"
    merged[s + "\u0000" + t] = (merged[s + "\u0000" + t] || 0) + links[key2]
  }
  var out = []
  for (var key3 in merged) {
    var pair = key3.split("\u0000")
    out.push({ source: pair[0], target: pair[1], bytes: merged[key3] })
  }
  out.sort(function(a, b) { return b.bytes - a.bytes })
  return { sources: sources.nodes, targets: targets.nodes, links: out }
}

function topNodes(totals, max) {
  var nodes = []
  for (var name in totals) nodes.push({ name: name, bytes: totals[name] })
  nodes.sort(function(a, b) { return b.bytes - a.bytes })
  var limit = Math.max(1, number(max, 6))
  var names = dict()
  if (nodes.length > limit) {
    var rest = 0
    for (var i = limit - 1; i < nodes.length; i++) rest += nodes[i].bytes
    nodes = nodes.slice(0, limit - 1)
    nodes.push({ name: "other", bytes: rest })
  }
  for (var j = 0; j < nodes.length; j++) names[nodes[j].name] = true
  return { nodes: nodes, names: names }
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

// The fastest and slowest measured delays; untested configs are left out.
function delaySpan(delays) {
  var span = { low: 0, high: 0 }
  for (var i = 0; i < delays.length; i++) {
    var n = number(delays[i], 0)
    if (n <= 0) continue
    if (span.low === 0 || n < span.low) span.low = n
    if (n > span.high) span.high = n
  }
  return span
}

// Where a delay sits within `span`, from 0 for the fastest to 1 for the slowest.
function delayHeat(ms, span) {
  var n = number(ms, 0)
  if (n <= 0 || span.high <= span.low) return 0
  return Math.max(0, Math.min(1, (n - span.low) / (span.high - span.low)))
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
  return relativeSince(then, nowMs)
}

function relativeSince(thenMs, nowMs) {
  var delta = number(nowMs, Date.now()) - number(thenMs, 0)
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

var LEADING_EMOJI = /^((?:\uD83C[\uDDE6-\uDDFF]){2}|(?:\uD83C[\uDF00-\uDFFF]|\uD83D[\uDC00-\uDEFF]|\uD83E[\uDD00-\uDEFF]|[\u2600-\u27BF])\uFE0F?)\s+/

// Splits a leading flag or emoji, and the space after it, off a name.
function leadingEmoji(name) {
  var s = text(name)
  var m = LEADING_EMOJI.exec(s)
  return m ? { mark: m[1], rest: s.slice(m[0].length) } : { mark: "", rest: s }
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
    subscriptionUsage: subscriptionUsage,
    parseRules: parseRules,
    ruleParts: ruleParts,
    validateRule: validateRule,
    isCidr: isCidr,
    parseApiInfo: parseApiInfo,
    parseConfigs: parseConfigs,
    parseProxies: parseProxies,
    isSystemGroup: isSystemGroup,
    userGroups: userGroups,
    ruleTargets: ruleTargets,
    primaryGroupName: primaryGroupName,
    groupByName: groupByName,
    groupForConfig: groupForConfig,
    historyDelay: historyDelay,
    parseDelay: parseDelay,
    parseGroupDelay: parseGroupDelay,
    parseTraffic: parseTraffic,
    parseTrace: parseTrace,
    parseConnections: parseConnections,
    enrichConnections: enrichConnections,
    mergeConnectionLog: mergeConnectionLog,
    stackConnections: stackConnections,
    openConnectionCount: openConnectionCount,
    connectionHost: connectionHost,
    parseApiRules: parseApiRules,
    splitApiRules: splitApiRules,
    apiRuleType: apiRuleType,
    ruleQueryHost: ruleQueryHost,
    ruleVerdict: ruleVerdict,
    testRules: testRules,
    ruleTargetTally: ruleTargetTally,
    connectionFlows: connectionFlows,
    formatBytes: formatBytes,
    formatRate: formatRate,
    formatDuration: formatDuration,
    delaySpan: delaySpan,
    delayHeat: delayHeat,
    formatDelay: formatDelay,
    formatDate: formatDate,
    relativeTime: relativeTime,
    relativeSince: relativeSince,
    parseUnitTimestamp: parseUnitTimestamp,
    nextMode: nextMode,
    filterNames: filterNames,
    leadingEmoji: leadingEmoji
  }
}
