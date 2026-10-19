// Pure data helpers for the Omihomo panel. Everything here either reads a
// message from the daemon's socket, or formats a value for display; nothing
// touches QML types, so tests/model_test.js runs the same functions under node.

var RULE_TYPES = ["DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "PROCESS-NAME"]
var MODES = ["rule", "global", "direct"]

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

// ---------------------------------------------------------------- daemon
//
// Everything below reads one decoded message from the daemon's socket (see
// docs/daemon.md) into the shapes the panel renders. A field the daemon left
// out or sent with the wrong type reads as empty rather than throwing.

function defaultStatus() {
  return {
    state: "unknown",
    detail: "",
    activeSubscription: "",
    primaryGroup: "",
    mode: "",
    tunEnabled: false,
    tunRedirect: false,
    tailscaleEnabled: false,
    tailscalePresent: false,
    tailnetProxy: false,
    startedMs: 0,
    tunBypass: []
  }
}

function readStatus(data) {
  if (!data || typeof data !== "object") return defaultStatus()
  return {
    state: text(data.state) || "unknown",
    detail: text(data.detail),
    activeSubscription: text(data.active_subscription),
    primaryGroup: text(data.primary_group),
    mode: text(data.mode).toLowerCase(),
    tunEnabled: data.tun === true,
    tunRedirect: data.tun_redirect === true,
    tailscaleEnabled: data.tailscale === true,
    tailscalePresent: data.tailscale_present === true,
    tailnetProxy: data.tailnet_proxy === true,
    startedMs: number(data.started_at, 0),
    tunBypass: Array.isArray(data.tun_bypass)
      ? data.tun_bypass.filter(function(cidr) { return typeof cidr === "string" }) : []
  }
}

// What the rule tester needs to know about TUN: whether it routes this
// machine's traffic at all, and the address space it routes around.
function statusRouting(status) {
  var running = !!status && (status.state === "on" || status.state === "degraded")
  return {
    tunEnabled: running && status.tunEnabled === true,
    tunAutoRoute: true,
    routeExcludeAddresses: status && status.tunBypass ? status.tunBypass.slice() : []
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

// A failed call's error, as panel copy. The daemon's messages are written for
// people; the code is the fallback when one arrives without a message.
function daemonError(error) {
  if (!error || typeof error !== "object") return "The core did not answer"
  var message = text(error.message).replace(/\s+/g, " ").trim()
  if (message !== "") return message
  switch (text(error.code)) {
  case "not_running": return "The proxy is not running"
  case "not_found": return "Not found"
  case "fetch_failed": return "Subscription fetch failed"
  case "rejected": return "The subscription is not usable"
  case "busy": return "The core is busy"
  case "forbidden": return "This account does not own Omihomo"
  default: return "The core refused the change"
  }
}

function readSubscriptions(data) {
  var list = data && typeof data === "object" ? data.subscriptions : null
  if (!Array.isArray(list)) return []
  var out = []
  for (var i = 0; i < list.length; i++) {
    var entry = readSubscription(list[i])
    if (entry) out.push(entry)
  }
  return out
}

function readSubscription(entry) {
  if (!entry || text(entry.name) === "") return null
  return {
    name: text(entry.name),
    updatedAt: number(entry.updated_at, 0),
    upload: optionalNumber(entry.upload),
    download: optionalNumber(entry.download),
    total: optionalNumber(entry.total),
    expire: optionalNumber(entry.expire),
    active: entry.active === true,
    nextUpdate: number(entry.next_update, 0),
    error: text(entry.error)
  }
}

function optionalNumber(value) {
  return value === null || value === undefined ? null : number(value, null)
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

// Saved user rules keep their 1-based removal index even when inactive;
// only effective rules participate in routing tests.
function readRules(data) {
  var list = data && typeof data === "object" ? data.rules : null
  var out = { own: [], injected: [], subscription: [], effective: [] }
  if (!Array.isArray(list)) return out
  for (var i = 0; i < list.length; i++) {
    var entry = list[i]
    if (!entry || typeof entry !== "object") continue
    var rule = { type: text(entry.type), value: text(entry.value), target: text(entry.target),
      inactive: entry.inactive === true }
    var source = text(entry.source)
    if (source === "user") {
      rule.index = out.own.length + 1
      rule.rule = rule.type + "," + rule.value + "," + rule.target
      out.own.push(rule)
    } else if (source === "omihomo") {
      out.injected.push(rule)
    } else {
      out.subscription.push(rule)
    }
    if (!rule.inactive) out.effective.push(rule)
  }
  return out
}

// Mirrors the daemon's per-type validation for the form's four types so it
// can refuse a rule before the call. The daemon is still the real gate.
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

// `groups.list` and the `groups` stream: the running config's groups in its
// own order, each with its members. Members become the group's `all` list,
// and each member's last delay its config entry, the shape the configs view
// draws from.
function readGroups(data) {
  var list = data && typeof data === "object" ? data.groups : null
  var groups = []
  var configs = dict()
  if (!Array.isArray(list)) return { groups: groups, configs: configs }
  for (var i = 0; i < list.length; i++) {
    var group = readGroup(list[i])
    if (!group) continue
    groups.push(group)
    var members = Array.isArray(list[i].members) ? list[i].members : []
    for (var j = 0; j < members.length; j++) {
      var member = members[j]
      if (!member || text(member.name) === "") continue
      var delay = number(member.delay, 0)
      configs[text(member.name)] = {
        name: text(member.name),
        type: text(member.type),
        alive: member.alive === true,
        history: delay > 0 ? [{ delay: delay }] : []
      }
    }
  }
  return { groups: groups, configs: configs }
}

function readGroup(entry) {
  if (!entry || typeof entry !== "object" || text(entry.name) === "") return null
  var members = Array.isArray(entry.members) ? entry.members : []
  var all = []
  for (var i = 0; i < members.length; i++) {
    if (members[i] && text(members[i].name) !== "") all.push(text(members[i].name))
  }
  return {
    name: text(entry.name),
    type: text(entry.type),
    now: text(entry.now),
    all: all,
    selectable: entry.selectable === true,
    system: text(entry.name) === "GLOBAL"
  }
}

// A group in a list replaced by a fresher copy of itself, as `groups.select`
// returns it.
function replaceGroup(groups, group) {
  if (!group) return groups
  var out = []
  for (var i = 0; i < groups.length; i++) out.push(groups[i].name === group.name ? group : groups[i])
  return out
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

// `delay.test` maps each tested config to its delay; 0 is a failed test.
function readDelays(data) {
  var delays = data && typeof data === "object" ? data.delays : null
  var out = dict()
  if (!delays || typeof delays !== "object") return out
  for (var name in delays) out[name] = number(delays[name], 0)
  return out
}

function readTraffic(data) {
  if (!data || typeof data !== "object") return null
  if (data.up === undefined && data.down === undefined) return null
  return { up: number(data.up, 0), down: number(data.down, 0) }
}

// `egress.check` measures the route through the proxy: where traffic leaves
// it, and the round trip.
function readEgress(data) {
  if (!data || typeof data !== "object") return { ip: "", latency: 0 }
  return { ip: text(data.ip), latency: Math.round(number(data.latency, 0)) }
}

// The `connections` stream only ever states what is open right now, so a
// panel that renders it directly loses every connection the moment it
// finishes and repaints its whole list on each push. The functions below turn
// that snapshot into a log instead: `readConnections` reads one snapshot,
// `mergeConnectionLog` folds it into what came before, and `stackConnections`
// collapses the result into the rows the view draws.
function readConnections(data, nowMs) {
  var now = number(nowMs, Date.now())
  var out = { downloadTotal: 0, uploadTotal: 0, items: [] }
  if (!data || typeof data !== "object") return out
  out.downloadTotal = number(data.download_total, 0)
  out.uploadTotal = number(data.upload_total, 0)
  var list = data.connections
  if (!Array.isArray(list)) return out
  for (var i = 0; i < list.length; i++) {
    var entry = list[i]
    if (!entry || typeof entry !== "object") continue
    var started = number(entry.started_at, 0)
    out.items.push({
      id: text(entry.id),
      host: connectionHost(entry),
      network: text(entry.network).toUpperCase(),
      process: baseName(entry.process) || (text(entry.inbound) === "inner" ? "mihomo" : ""),
      inboundType: text(entry.inbound),
      sourceIP: text(entry.source_ip),
      sourcePort: number(entry.source_port, 0),
      destinationIP: text(entry.destination_ip),
      destinationPort: number(entry.destination_port, 0),
      chain: text(entry.chain),
      rule: text(entry.rule),
      upload: number(entry.upload, 0),
      download: number(entry.download, 0),
      startMs: started > 0 ? started : now,
      open: true,
      lastSeenMs: now,
      closedAtMs: 0
    })
  }
  return out
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

function connectionHost(entry) {
  var host = text(entry.host)
  if (host === "") host = text(entry.destination_ip)
  var port = number(entry.destination_port, 0) > 0 ? text(entry.destination_port) : ""
  if (host === "") return port === "" ? "unknown" : ":" + port
  return port === "" ? host : host + ":" + port
}

function baseName(path) {
  var parts = text(path).split("/")
  return text(parts[parts.length - 1])
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

// How long ago a unix-milliseconds time was; empty for 0, which the daemon
// sends for "never".
function relativeTime(ms, nowMs) {
  var then = number(ms, 0)
  if (then <= 0) return ""
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

// One emoji as a reader sees it: a flag (two regional indicators), or a
// pictograph with its variation selector, skin tone, tag sequence (subdivision
// flags), and any zero-width-joined pictographs after it.
var PICTOGRAPH = "(?:\\uD83C[\\uDF00-\\uDFFF]|\\uD83D[\\uDC00-\\uDFFF]|\\uD83E[\\uDD00-\\uDFFF]|[\\u2600-\\u27BF])\\uFE0F?(?:\\uD83C[\\uDFFB-\\uDFFF])?"
var EMOJI = "(?:(?:\\uD83C[\\uDDE6-\\uDDFF]){2}|" + PICTOGRAPH + "(?:\\uDB40[\\uDC20-\\uDC7F])*(?:\\u200D" + PICTOGRAPH + ")*)"
var LEADING_EMOJI = new RegExp("^(" + EMOJI + ")\\s+")
var TRAILING_EMOJI = new RegExp("\\s+((?:" + EMOJI + ")(?:\\s*" + EMOJI + ")*)\\s*$")
var EMOJI_ALL = new RegExp(EMOJI, "g")

// Splits a name into a leading flag or emoji, the text, and any emoji trailing
// it. Each emoji end only counts when whitespace separates it from the text, so
// "🇯🇵Node" stays one piece.
function nameParts(name) {
  var rest = text(name)
  var mark = ""
  var lead = LEADING_EMOJI.exec(rest)
  if (lead) {
    mark = lead[1]
    rest = rest.slice(lead[0].length)
  }
  var tail = []
  var trail = TRAILING_EMOJI.exec(rest)
  if (trail && trail.index > 0) {
    tail = trail[1].match(EMOJI_ALL)
    rest = rest.slice(0, trail.index)
  }
  return { mark: mark, rest: rest, tail: tail }
}

// A file-name-safe key for one emoji: its code points in hex, e.g. "1f1ef-1f1f5".
function emojiKey(emoji) {
  var s = text(emoji)
  var out = []
  for (var i = 0; i < s.length; i++) {
    var c = s.charCodeAt(i)
    if (c >= 0xD800 && c <= 0xDBFF && i + 1 < s.length) {
      c = (c - 0xD800) * 0x400 + (s.charCodeAt(i + 1) - 0xDC00) + 0x10000
      i++
    }
    out.push(c.toString(16))
  }
  return out.join("-")
}

if (typeof module !== "undefined") {
  module.exports = {
    RULE_TYPES: RULE_TYPES,
    MODES: MODES,
    isSystemGroup: isSystemGroup,
    text: text,
    number: number,
    dict: dict,
    parseJson: parseJson,
    defaultStatus: defaultStatus,
    readStatus: readStatus,
    statusRouting: statusRouting,
    stateLabel: stateLabel,
    daemonError: daemonError,
    readSubscriptions: readSubscriptions,
    readSubscription: readSubscription,
    subscriptionUsage: subscriptionUsage,
    readRules: readRules,
    validateRule: validateRule,
    isCidr: isCidr,
    readGroups: readGroups,
    readGroup: readGroup,
    replaceGroup: replaceGroup,
    ruleTargets: ruleTargets,
    userGroups: userGroups,
    primaryGroupName: primaryGroupName,
    groupByName: groupByName,
    groupForConfig: groupForConfig,
    historyDelay: historyDelay,
    readDelays: readDelays,
    readTraffic: readTraffic,
    readEgress: readEgress,
    readConnections: readConnections,
    mergeConnectionLog: mergeConnectionLog,
    stackConnections: stackConnections,
    openConnectionCount: openConnectionCount,
    connectionHost: connectionHost,
    baseName: baseName,
    ruleQueryHost: ruleQueryHost,
    ipv4Number: ipv4Number,
    inIpv4Cidr: inIpv4Cidr,
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
    filterNames: filterNames,
    nameParts: nameParts,
    emojiKey: emojiKey
  }
}
