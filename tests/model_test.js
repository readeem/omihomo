#!/usr/bin/env node
// Model.js is the widget's parsing and formatting seam: every message from the
// daemon's socket and every rendered string passes through it, and none of it
// needs QML to run. These tests exercise the shapes the panel actually sees.

const test = require("node:test")
const assert = require("node:assert/strict")
const Model = require("../widget/Model.js")

test("status reads the daemon's Status", () => {
  const status = Model.readStatus({
    state: "degraded", detail: "tun device is missing", active_subscription: "home",
    primary_group: "Proxy", mode: "global", tun: true, tun_redirect: true, tailscale: true,
    tailscale_present: true, tailnet_proxy: true, started_at: 1791741355865,
    tun_bypass: ["10.0.0.0/8", 7]
  })
  assert.equal(status.state, "degraded")
  assert.equal(status.detail, "tun device is missing")
  assert.equal(status.activeSubscription, "home")
  assert.equal(status.primaryGroup, "Proxy")
  assert.equal(status.mode, "global")
  assert.equal(status.tunEnabled, true)
  assert.equal(status.tunRedirect, true)
  assert.equal(status.tailscaleEnabled, true)
  assert.equal(status.tailscalePresent, true)
  assert.equal(status.tailnetProxy, true)
  assert.equal(status.startedMs, 1791741355865)
  assert.deepEqual(status.tunBypass, ["10.0.0.0/8"])
})

test("missing status fields read as empty, and garbage as unknown", () => {
  const status = Model.readStatus({ state: "stopped", detail: null, started_at: 0 })
  assert.equal(status.detail, "")
  assert.equal(status.activeSubscription, "")
  assert.equal(status.startedMs, 0)
  assert.equal(status.tunEnabled, false)
  assert.deepEqual(status.tunBypass, [])
  assert.equal(Model.readStatus(null).state, "unknown")
  assert.equal(Model.readStatus("on").state, "unknown")
})

test("the rule tester sees TUN's bypass only while TUN routes", () => {
  const on = Model.readStatus({ state: "on", tun: true, tun_bypass: ["192.168.0.0/16"] })
  assert.equal(Model.testRules([{ type: "MATCH", target: "Proxy" }], "192.168.1.1",
    Model.statusRouting(on)).bypassed, true)
  const stopped = Model.readStatus({ state: "stopped", tun: true, tun_bypass: ["192.168.0.0/16"] })
  assert.equal(Model.statusRouting(stopped).tunEnabled, false)
  assert.equal(Model.statusRouting(Model.readStatus({ state: "on", tun: false })).tunEnabled, false)
})

test("a daemon error shows its message, else its code's", () => {
  assert.equal(Model.daemonError({ code: "conflict", message: "tailscaled\n is  missing" }),
    "tailscaled is missing")
  assert.equal(Model.daemonError({ code: "fetch_failed", message: "" }), "Subscription fetch failed")
  assert.equal(Model.daemonError({ code: "something_new" }), "The core refused the change")
  assert.equal(Model.daemonError(null), "The core did not answer")
})

test("subscriptions read with their quota, times and last error", () => {
  const subs = Model.readSubscriptions({ subscriptions: [
    { name: "home", updated_at: 1791741355865, upload: 1, download: 2, total: null, expire: 1800000000,
      active: true, interval: 43200, next_update: 1791784555865, error: "fetch failed" },
    { name: "" },
    null
  ] })
  assert.equal(subs.length, 1)
  assert.equal(subs[0].name, "home")
  assert.equal(subs[0].updatedAt, 1791741355865)
  assert.equal(subs[0].total, null)
  assert.equal(subs[0].expire, 1800000000)
  assert.equal(subs[0].active, true)
  assert.equal(subs[0].nextUpdate, 1791784555865)
  assert.equal(subs[0].error, "fetch failed")
  assert.deepEqual(Model.readSubscriptions({}), [])
})

test("a never-updated subscription has no relative time", () => {
  assert.equal(Model.relativeTime(0, 1000), "")
  assert.equal(Model.relativeTime(1000, 1000 + 3 * 60000), "3m ago")
})

test("subscription usage shows quota and expiry", () => {
  const usage = Model.subscriptionUsage({
    name: "home",
    upload: 12000000000,
    download: 130000000000,
    total: 500000000000,
    expire: Date.parse("2027-03-12T00:00:00Z") / 1000
  })
  assert.equal(usage.fraction, 0.284)
  assert.match(usage.text, /142 GB of 500 GB/)
  assert.match(usage.text, /expires 12 Mar/)
})

test("a subscription with no userinfo has no quota bar", () => {
  const usage = Model.subscriptionUsage({
    name: "backup", upload: null, download: null, total: null, expire: null
  })
  assert.equal(usage.fraction, -1)
  assert.equal(usage.text, "no quota reported")
})

test("rule validation mirrors the daemon's checks for the form's types", () => {
  assert.equal(Model.validateRule("IP-CIDR", "10.0.0.0/8", "DIRECT"), "")
  assert.equal(Model.validateRule("IP-CIDR", "fd00::/64", "DIRECT"), "")
  assert.notEqual(Model.validateRule("IP-CIDR", "example.com", "DIRECT"), "")
  assert.notEqual(Model.validateRule("PROCESS-NAME", "Firefox,REJECT", "Proxy"), "")
  assert.notEqual(Model.validateRule("IP-CIDR", "10.0.0.0/8", "DIRECT,no-resolve"), "")
  assert.notEqual(Model.validateRule("IP-CIDR", "10.0.0.0/33", "DIRECT"), "")
  assert.notEqual(Model.validateRule("IP-CIDR", "300.0.0.0/8", "DIRECT"), "")
  assert.equal(Model.validateRule("DOMAIN-SUFFIX", "example.com", "Proxy"), "")
  assert.notEqual(Model.validateRule("DOMAIN-SUFFIX", "example.com/path", "Proxy"), "")
  assert.equal(Model.validateRule("PROCESS-NAME", "syncthing", "DIRECT"), "")
  assert.notEqual(Model.validateRule("DOMAIN-SUFFIX", "example.com", ""), "")
  assert.notEqual(Model.validateRule("DOMAIN-SUFFIX", "", "Proxy"), "")
})

test("groups read in the config's order, with their members as configs", () => {
  const parsed = Model.readGroups({ groups: [
    { name: "Proxy", type: "select", now: "Tokyo 01", selectable: true, members: [
      { name: "Tokyo 01", type: "vmess", alive: true, delay: 182 },
      { name: "DIRECT", type: "direct", alive: true, delay: 0 }] },
    { name: "Auto", type: "url-test", now: "Tokyo 01", selectable: false, members: [
      { name: "Tokyo 01", type: "vmess", alive: true, delay: 182 }] },
    { name: "GLOBAL", type: "select", now: "Proxy", selectable: true, members: "garbage" },
    { name: "" }
  ] })
  assert.deepEqual(parsed.groups.map(g => g.name), ["Proxy", "Auto", "GLOBAL"])
  assert.deepEqual(parsed.groups[0].all, ["Tokyo 01", "DIRECT"])
  assert.equal(parsed.groups[0].selectable, true)
  assert.equal(parsed.groups[1].selectable, false)
  assert.equal(parsed.groups[2].system, true)
  assert.deepEqual(parsed.groups[2].all, [])
  assert.equal(Model.historyDelay(parsed.configs["Tokyo 01"]), 182)
  assert.equal(Model.historyDelay(parsed.configs.DIRECT), 0)
  assert.equal(parsed.configs.Proxy, undefined)
})

test("a selection's reply replaces only its own group", () => {
  const groups = Model.readGroups({ groups: [
    { name: "Proxy", now: "A", members: [{ name: "A" }, { name: "B" }] },
    { name: "Auto", now: "A", members: [{ name: "A" }] }] }).groups
  const replaced = Model.replaceGroup(groups, Model.readGroup({ name: "Proxy", now: "B", members: [] }))
  assert.equal(replaced[0].now, "B")
  assert.equal(replaced[1], groups[1])
  assert.equal(Model.replaceGroup(groups, null), groups)
})

test("provider configs resolve through their owning group", () => {
  const groups = [{ name: "Proxy", all: ["Poland", "France"] }]
  assert.equal(Model.groupForConfig(groups, "Poland"), "Proxy")
  assert.equal(Model.groupForConfig(groups, "Missing"), "")
})

test("the primary group fallback skips mihomo's GLOBAL group", () => {
  const groups = [
    { name: "GLOBAL", selectable: true },
    { name: "Auto", selectable: false },
    { name: "Proxy", selectable: true }
  ]
  assert.equal(Model.primaryGroupName(groups, "Proxy"), "Proxy")
  assert.equal(Model.primaryGroupName(groups, "GLOBAL"), "Proxy")
  assert.equal(Model.primaryGroupName(groups, ""), "Proxy")
  assert.equal(Model.primaryGroupName(groups, "Deleted"), "Proxy")
  assert.equal(Model.primaryGroupName([{ name: "GLOBAL", selectable: true }], ""), "")
  assert.equal(Model.primaryGroupName([], "Proxy"), "")
})

test("system groups are removed from user-facing group lists", () => {
  const groups = [
    { name: "GLOBAL", system: true },
    { name: "Proxy", system: false }
  ]
  assert.deepEqual(Model.userGroups(groups).map(group => group.name), ["Proxy"])
})

test("rules split by source, and the owner's carry their index", () => {
  const rules = Model.readRules({ rules: [
    { type: "DOMAIN-SUFFIX", value: "internal.example", target: "DIRECT", source: "user" },
    { type: "PROCESS-NAME", value: "syncthing", target: "DIRECT", source: "user" },
    { type: "DOMAIN-SUFFIX", value: "ts.net", target: "DIRECT", source: "omihomo" },
    { type: "DOMAIN-KEYWORD", value: "google", target: "Proxy", source: "subscription" },
    { type: "MATCH", value: "", target: "Proxy", source: "subscription" }
  ] })
  assert.deepEqual(rules.own.map(r => [r.index, r.rule]),
    [[1, "DOMAIN-SUFFIX,internal.example,DIRECT"], [2, "PROCESS-NAME,syncthing,DIRECT"]])
  assert.deepEqual(rules.injected.map(r => r.value), ["ts.net"])
  assert.deepEqual(rules.subscription.map(r => r.type), ["DOMAIN-KEYWORD", "MATCH"])
  assert.deepEqual(Model.readRules(null), { own: [], injected: [], subscription: [], effective: [] })
})

test("saved rules with missing targets stay editable but cannot match traffic", () => {
  const parsed = Model.readRules({ rules: [
    { type: "DOMAIN-SUFFIX", value: "example.com", target: "MissingGroup", source: "user", inactive: true },
    { type: "PROCESS-NAME", value: "example-app", target: "DIRECT", source: "user" },
    { type: "MATCH", value: "", target: "DIRECT", source: "subscription" }
  ] })
  assert.equal(parsed.own[0].index, 1)
  assert.equal(parsed.own[0].inactive, true)
  assert.equal(parsed.own[1].index, 2)
  const result = Model.testRules(parsed.effective, "example.com")
  assert.equal(parsed.effective[result.hit].target, "DIRECT")
  assert.equal(parsed.effective.length, 2)
})

test("rule targets list the built-ins, then groups, then every config once", () => {
  const groups = [
    { name: "Proxy", all: ["Auto", "🇩🇪 Berlin", "DIRECT"] },
    { name: "Auto", all: ["🇩🇪 Berlin", "🇯🇵 Tokyo", "constructor", "DIRECT,no-resolve"] }
  ]
  assert.deepEqual(Model.ruleTargets(groups).map(t => t.value + ":" + t.description),
    ["DIRECT:built-in", "REJECT:built-in", "Proxy:group", "Auto:group", "🇩🇪 Berlin:config", "🇯🇵 Tokyo:config",
      "constructor:config"])
})

test("delays, traffic and egress read from their replies", () => {
  const delays = Model.readDelays({ delays: { "Tokyo 01": 91, Dead: 0 } })
  assert.equal(delays["Tokyo 01"], 91)
  assert.equal(delays.Dead, 0)
  assert.deepEqual(Object.keys(Model.readDelays(null)), [])
  assert.deepEqual(Model.readTraffic({ up: 120, down: 4096 }), { up: 120, down: 4096 })
  assert.equal(Model.readTraffic({}), null)
  assert.deepEqual(Model.readEgress({ ip: "203.0.113.9", latency: 183 }), { ip: "203.0.113.9", latency: 183 })
  assert.deepEqual(Model.readEgress(null), { ip: "", latency: 0 })
})

const snapshot = (connections, extra) => Object.assign({
  download_total: 4096, upload_total: 512, open: connections.length, connections
}, extra || {})

const conn = (id, host, process, over) => Object.assign({
  id, upload: 1, download: 2, started_at: Date.parse("2026-08-18T11:00:00Z"), network: "tcp",
  inbound: "tun", host, destination_port: 443, process, chain: "Tokyo 01",
  rule: "DomainSuffix(example.org)"
}, over || {})

test("connection labels use the process name and identify internal traffic", () => {
  const items = Model.readConnections(snapshot([
    { id: "named", process: "firefox", inbound: "tun" },
    { id: "path", process: "curl", inbound: "tun" },
    { id: "internal", inbound: "inner" },
    { id: "unknown", inbound: "tun" }
  ])).items
  assert.deepEqual(items.map(c => c.process), ["firefox", "curl", "mihomo", ""])
})

test("a connection keeps its resolved owner while its endpoints stay the same", () => {
  const raw = snapshot([{ id: "udp", network: "udp", inbound: "tun", source_ip: "192.168.42.10",
    source_port: 53000, destination_ip: "203.0.113.5", destination_port: 443 }])
  const first = Model.readConnections(raw).items
  first[0].process = "firefox"
  assert.equal(Model.mergeConnectionLog(first, Model.readConnections(raw).items)[0].process, "firefox")
  const changed = Model.readConnections(raw).items
  changed[0].sourcePort = 53001
  assert.equal(Model.mergeConnectionLog(first, changed)[0].process, "")
})

test("a connection snapshot carries a host, a start, and its totals", () => {
  const now = Date.parse("2026-08-18T12:00:00Z")
  const parsed = Model.readConnections(snapshot([
    conn("a", "github.com", "firefox"),
    { id: "b", upload: 0, download: 0, started_at: Date.parse("2026-08-18T11:59:00Z"),
      rule: "Match", network: "udp", host: "", destination_ip: "1.1.1.1", destination_port: 53 }
  ]), now)
  assert.equal(parsed.downloadTotal, 4096)
  assert.deepEqual(parsed.items.map(c => c.id), ["a", "b"])
  assert.equal(parsed.items[0].host, "github.com:443")
  assert.equal(parsed.items[0].chain, "Tokyo 01")
  assert.equal(parsed.items[0].rule, "DomainSuffix(example.org)")
  assert.equal(parsed.items[0].process, "firefox")
  assert.equal(parsed.items[0].open, true)
  assert.equal(Model.formatDuration(now - parsed.items[0].startMs), "1h 0m")
  assert.equal(parsed.items[1].host, "1.1.1.1:53")
})

test("a connection missing from the next snapshot is closed, not dropped", () => {
  const first = Model.readConnections(snapshot([
    conn("a", "github.com", "firefox"),
    conn("b", "cdn.jsdelivr.net", "firefox")
  ]), 1000)
  let log = Model.mergeConnectionLog([], first.items, 1000, 200)
  assert.equal(Model.openConnectionCount(log), 2)

  // "b" is gone from the second snapshot, and "c" is new.
  const second = Model.readConnections(snapshot([
    conn("a", "github.com", "firefox", { download: 900 }),
    conn("c", "github.com", "firefox")
  ]), 5000)
  log = Model.mergeConnectionLog(log, second.items, 5000, 200)

  assert.deepEqual(log.map(e => e.id), ["a", "b", "c"])
  assert.equal(Model.openConnectionCount(log), 2)
  const closed = log.find(e => e.id === "b")
  assert.equal(closed.open, false)
  // It closed when it was last seen, not when the poll noticed it was gone.
  assert.equal(closed.closedAtMs, 1000)
  // The reopened figures land, but the original start survives them.
  assert.equal(log.find(e => e.id === "a").download, 900)
  assert.equal(log.find(e => e.id === "a").startMs, first.items[0].startMs)
})

test("the log keeps every open connection and caps the closed ones", () => {
  const open = Model.readConnections(snapshot([conn("keep", "github.com", "firefox")]), 1000)
  let log = Model.mergeConnectionLog([], open.items, 1000, 2)
  for (let i = 0; i < 4; i++) {
    const round = Model.readConnections(snapshot([
      conn("keep", "github.com", "firefox"),
      conn("gone" + i, "cdn.jsdelivr.net", "firefox")
    ]), 2000 + i * 1000)
    log = Model.mergeConnectionLog(log, round.items, 2000 + i * 1000, 2)
  }
  // "keep" and the last round's "gone3" are still open, and of the three that
  // have closed only the two most recent survive the cap.
  assert.deepEqual(log.filter(e => e.open).map(e => e.id), ["keep", "gone3"])
  assert.deepEqual(log.filter(e => !e.open).map(e => e.id), ["gone1", "gone2"])
  assert.equal(Model.openConnectionCount(log), 2)
})

test("stacks group by process, destination, and state", () => {
  const first = Model.readConnections(snapshot([
    conn("a", "github.com", "firefox", { started_at: Date.parse("2026-08-18T11:00:00Z"), download: 10 }),
    conn("b", "github.com", "firefox", { started_at: Date.parse("2026-08-18T11:30:00Z"), download: 20 }),
    conn("c", "github.com", "curl", { started_at: Date.parse("2026-08-18T11:45:00Z"), download: 30 })
  ]), 1000)
  let log = Model.mergeConnectionLog([], first.items, 1000, 200)

  // Firefox keeps one socket to github and loses the other.
  const second = Model.readConnections(snapshot([
    conn("a", "github.com", "firefox", { started_at: Date.parse("2026-08-18T11:00:00Z"), download: 10 }),
    conn("c", "github.com", "curl", { started_at: Date.parse("2026-08-18T11:45:00Z"), download: 30 })
  ]), 5000)
  log = Model.mergeConnectionLog(log, second.items, 5000, 200)

  const stacks = Model.stackConnections(log)
  // Two processes on one host, newest open stack first, and firefox's closed
  // socket is its own row rather than a count hidden inside the open one.
  assert.equal(stacks.length, 3)
  assert.deepEqual(stacks.map(s => [s.process, s.open, s.count]),
    [["curl", true, 1], ["firefox", true, 1], ["firefox", false, 1]])
  assert.deepEqual(stacks[2].ids, ["b"])
  assert.equal(stacks[2].closedAtMs, 1000)

  // A stack sums its members and is as old as the oldest of them.
  const together = Model.stackConnections(Model.mergeConnectionLog([], first.items, 1000, 200))
  const firefox = together.find(s => s.process === "firefox")
  assert.equal(firefox.count, 2)
  assert.equal(firefox.download, 30)
  assert.equal(firefox.startMs, Date.parse("2026-08-18T11:00:00Z"))
})

test("the rule tester stops at the first hit, mihomo's order", () => {
  const rules = [
    { type: "DOMAIN-SUFFIX", value: "github.com", target: "Proxy" },
    { type: "RULE-SET", value: "streaming", target: "Streaming" },
    { type: "DOMAIN-SUFFIX", value: "googlevideo.com", target: "Streaming" },
    { type: "GEOIP", value: "private", target: "DIRECT" },
    { type: "MATCH", value: "", target: "Proxy" }
  ]
  const video = Model.testRules(rules, "https://rr3.googlevideo.com:443/videoplayback?x=1")
  assert.equal(video.host, "rr3.googlevideo.com")
  assert.equal(video.hit, 2)
  assert.deepEqual(video.verdicts, ["miss", "unknown", "hit"])
  assert.equal(video.unknown, 1)

  assert.equal(Model.testRules(rules, "10.2.3.4").hit, 3)
  assert.equal(Model.testRules(rules, "example.org").hit, 4)
  assert.equal(Model.testRules(rules, "notgithub.com").verdicts[0], "miss")
  assert.equal(Model.testRules(rules, "  ").hit, -1)
})

test("IP rules on a hostname depend on the core's DNS answer", () => {
  assert.equal(Model.ruleVerdict({ type: "IP-CIDR", value: "10.0.0.0/8" }, "example.org"), "unknown")
  assert.equal(Model.ruleVerdict({ type: "IP-CIDR", value: "10.0.0.0/8" }, "10.9.9.9"), "hit")
  assert.equal(Model.ruleVerdict({ type: "IP-CIDR", value: "192.168.0.0/16" }, "192.169.0.1"), "miss")
  assert.equal(Model.ruleVerdict({ type: "DOMAIN-KEYWORD", value: "openai" }, "chat.openai.com"), "hit")
  assert.equal(Model.ruleVerdict({ type: "PROCESS-NAME", value: "steam" }, "steampowered.com"), "unknown")
})

test("rule testing accounts for the active TUN route exclusions", () => {
  const rules = [{ type: "MATCH", target: "Proxy" }]
  const routing = { tunEnabled: true, tunAutoRoute: true, routeExcludeAddresses: ["192.168.0.0/16"] }
  const result = Model.testRules(rules, "192.168.42.1", routing)
  assert.equal(result.bypassed, true)
  assert.equal(result.bypassCidr, "192.168.0.0/16")
  assert.equal(result.hit, -1)
  assert.equal(Model.testRules(rules, "203.0.113.5", routing).bypassed, false)
  assert.equal(Model.testRules(rules, "192.168.42.1", {...routing, tunEnabled: false}).hit, 0)
  assert.equal(Model.testRules(rules, "192.168.42.1", {...routing, tunAutoRoute: false}).hit, 0)
  assert.equal(Model.testRules(rules, "192.168.42.1", {...routing, routeExcludeAddresses: []}).hit, 0)
  assert.deepEqual(Model.statusRouting(Model.readStatus({ state: "on", tun: true,
    tun_bypass: ["192.168.0.0/16"] })), routing)
})

test("a later rule hit is tentative when an earlier rule is unresolved", () => {
  assert.equal(Model.testRules([{type: "RULE-SET", value: "private", target: "DIRECT"},
    {type: "MATCH", target: "Proxy"}], "192.168.42.1").certain, false)
  assert.equal(Model.testRules([{type: "MATCH", target: "Proxy"}], "203.0.113.5").certain, true)
})

test("targets tally largest first", () => {
  const tally = Model.ruleTargetTally([
    { target: "DIRECT" }, { target: "Proxy" }, { target: "DIRECT" }, { target: "" }
  ])
  assert.deepEqual(tally, [{ target: "DIRECT", count: 2 }, { target: "Proxy", count: 1 }])
})

test("remote names that shadow object prototypes stay plain data", () => {
  const parsed = Model.readGroups(JSON.parse('{"groups":[{"name":"P","members":[{"name":"__proto__","type":"vless"}]}]}'))
  assert.deepEqual(Object.keys(parsed.configs), ["__proto__"])
  assert.deepEqual(Object.keys(Model.readDelays(JSON.parse('{"delays":{"__proto__":5}}'))), ["__proto__"])
  assert.deepEqual(Model.ruleTargetTally([{ target: "constructor" }]), [{ target: "constructor", count: 1 }])
  const flows = Model.connectionFlows([{ process: "constructor", chain: "DIRECT", download: 5, upload: 0, open: true }], 4, 4)
  assert.deepEqual(flows.sources, [{ name: "constructor", bytes: 5 }])
})

test("connection flows fold the smallest nodes into other", () => {
  const stack = (process, chain, bytes, open) => ({ process, chain, download: bytes, upload: 0, open })
  const flows = Model.connectionFlows([
    stack("firefox", "London 05", 500, true),
    stack("firefox", "Frankfurt 04", 300, true),
    stack("steam", "DIRECT", 900, true),
    stack("git", "Frankfurt 04", 50, true),
    stack("code", "Frankfurt 04", 999999, false)
  ], 2, 3)
  assert.deepEqual(flows.sources.map(n => n.name), ["steam", "other"])
  assert.equal(flows.sources[1].bytes, 850)
  assert.deepEqual(flows.targets.map(n => n.name), ["DIRECT", "London 05", "Frankfurt 04"])
  assert.deepEqual(flows.links[0], { source: "steam", target: "DIRECT", bytes: 900 })
  assert.equal(flows.links.reduce((sum, link) => sum + link.bytes, 0), 1750)
})

test("formatting is decimal and compact", () => {
  assert.equal(Model.formatBytes(0), "0 B")
  assert.equal(Model.formatBytes(999), "999 B")
  assert.equal(Model.formatBytes(1500), "1.5 kB")
  assert.equal(Model.formatBytes(142000000000), "142 GB")
  assert.equal(Model.formatRate(1500), "1.5 kB/s")
  assert.equal(Model.formatDuration(45000), "45s")
  assert.equal(Model.formatDuration(3600000 * 3 + 60000 * 12), "3h 12m")
  assert.equal(Model.formatDuration(86400000 * 5 + 3600000 * 3), "5d 3h")
  assert.equal(Model.formatDelay(0), "—")
  assert.equal(Model.formatDelay(182), "182 ms")
})

test("latency heat runs from the fastest measured delay to the slowest", () => {
  const span = Model.delaySpan([0, 206, 689, 330, 0])
  assert.deepEqual(span, { low: 206, high: 689 })
  assert.equal(Model.delayHeat(206, span), 0)
  assert.equal(Model.delayHeat(689, span), 1)
  assert.ok(Math.abs(Model.delayHeat(447.5, span) - 0.5) < 1e-9)
  assert.equal(Model.delayHeat(0, span), 0)
  assert.equal(Model.delayHeat(300, Model.delaySpan([300])), 0)
})

test("the config filter matches case-insensitively anywhere in the name", () => {
  const names = ["Tokyo 01", "TOKYO 02", "Frankfurt 01"]
  assert.deepEqual(Model.filterNames(names, "tokyo"), ["Tokyo 01", "TOKYO 02"])
  assert.deepEqual(Model.filterNames(names, " 01"), ["Tokyo 01", "Frankfurt 01"])
  assert.deepEqual(Model.filterNames(names, ""), names)
})

test("leading and trailing emoji split off a name with their spaces", () => {
  assert.deepEqual(Model.nameParts("🇯🇵 Node A 🚀"), { mark: "🇯🇵", rest: "Node A", tail: ["🚀"] })
  assert.deepEqual(Model.nameParts("🚀  Fast"), { mark: "🚀", rest: "Fast", tail: [] })
  assert.deepEqual(Model.nameParts("☁️ Cloud"), { mark: "☁️", rest: "Cloud", tail: [] })
  assert.deepEqual(Model.nameParts("🇯🇵Node"), { mark: "", rest: "🇯🇵Node", tail: [] })
  assert.deepEqual(Model.nameParts("DIRECT"), { mark: "", rest: "DIRECT", tail: [] })
  assert.deepEqual(Model.nameParts("🇯🇵 Node B 🚀🔥 "), { mark: "🇯🇵", rest: "Node B", tail: ["🚀", "🔥"] })
  assert.deepEqual(Model.nameParts("🇯🇵 Node B 🚀 🟢"), { mark: "🇯🇵", rest: "Node B", tail: ["🚀", "🟢"] })
  assert.deepEqual(Model.nameParts("Node🚀"), { mark: "", rest: "Node🚀", tail: [] })
  assert.deepEqual(Model.nameParts("👨‍💻 Dev 👍🏽"), { mark: "👨‍💻", rest: "Dev", tail: ["👍🏽"] })
  // An emoji-only name keeps its emoji as the mark rather than as an empty name with a tail.
  assert.deepEqual(Model.nameParts("🇯🇵 🚀"), { mark: "🇯🇵", rest: "🚀", tail: [] })
})

test("an emoji keys by its code points", () => {
  assert.equal(Model.emojiKey("🇯🇵"), "1f1ef-1f1f5")
  assert.equal(Model.emojiKey("☁️"), "2601-fe0f")
  assert.equal(Model.emojiKey("👍🏽"), "1f44d-1f3fd")
})
