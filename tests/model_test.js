#!/usr/bin/env node
// Model.js is the widget's parsing and formatting seam: every CLI payload,
// mihomo API response, and rendered string passes through it, and none of it
// needs QML to run. These tests exercise the shapes the panel actually sees.

const test = require("node:test")
const assert = require("node:assert/strict")
const Model = require("../Model.js")

test("status parses the CLI's stable object", () => {
  const status = Model.parseStatus(JSON.stringify({
    state: "degraded", status: "degraded", detail: "tun device is missing",
    ip: null, latency: null, download: null, upload: null, config: null,
    uptime: "Tue 2026-08-18 13:06:48 MSK", active_subscription: "home",
    primary_group: "Proxy", tun_enabled: true, autostart_enabled: true,
    permissions_ok: true, tailscale_enabled: true, tailscale_present: true,
    tailnet_proxy: true
  }))
  assert.equal(status.state, "degraded")
  assert.equal(status.detail, "tun device is missing")
  assert.equal(status.activeSubscription, "home")
  assert.equal(status.primaryGroup, "Proxy")
  assert.equal(status.tunEnabled, true)
  assert.equal(status.autostartEnabled, true)
  assert.equal(status.permissionsOk, true)
  assert.equal(status.tailscaleEnabled, true)
  assert.equal(status.tailscalePresent, true)
  assert.equal(status.tailnetProxy, true)
  assert.ok(status.startedMs > 0)
})

test("status nulls read as empty, and garbage falls back to unknown", () => {
  const status = Model.parseStatus(JSON.stringify({
    state: "stopped", detail: null, active_subscription: null, primary_group: null,
    tun_enabled: false, uptime: null
  }))
  assert.equal(status.detail, "")
  assert.equal(status.activeSubscription, "")
  assert.equal(status.startedMs, 0)
  assert.equal(status.autostartEnabled, false)
  assert.equal(status.permissionsOk, false)
  assert.equal(Model.parseStatus("not json").state, "unknown")
})

test("CLI errors carry the exit-code class", () => {
  const structured = Model.cliError('{"error":"no active subscription","code":13}', 13)
  assert.equal(structured.message, "no active subscription")
  assert.equal(structured.code, 13)

  const raw = Model.cliError("bash: line 3: boom\n", 1)
  assert.equal(raw.message, "bash: line 3: boom")

  const silent = Model.cliError("", 21)
  assert.equal(silent.message, "Subscription fetch failed")
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

test("rule validation mirrors the CLI's per-type rules", () => {
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

test("proxies split into groups and configs", () => {
  const parsed = Model.parseProxies(JSON.stringify({
    proxies: {
      "GLOBAL": { name: "GLOBAL", type: "Selector", now: "DIRECT", all: ["DIRECT", "Proxy"] },
      "Proxy": { name: "Proxy", type: "Selector", now: "Tokyo 01", all: ["Tokyo 01", "DIRECT"] },
      "Auto": { name: "Auto", type: "URLTest", now: "Tokyo 01", all: ["Tokyo 01"] },
      "Tokyo 01": { name: "Tokyo 01", type: "Vmess", udp: true, history: [{ delay: 182 }] },
      "DIRECT": { name: "DIRECT", type: "Direct", history: [] }
    }
  }))
  assert.deepEqual(parsed.groups.map(g => g.name), ["Auto", "GLOBAL", "Proxy"])
  assert.equal(parsed.groups.find(g => g.name === "GLOBAL").system, true)
  assert.equal(parsed.groups.find(g => g.name === "Proxy").selectable, true)
  assert.equal(parsed.groups.find(g => g.name === "Proxy").system, false)
  assert.equal(parsed.groups.find(g => g.name === "Auto").selectable, false)
  assert.equal(Model.historyDelay(parsed.configs["Tokyo 01"]), 182)
  assert.equal(parsed.configs["Proxy"], undefined)
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

test("our own rules are peeled off the head of the merged list", () => {
  const merged = Model.parseApiRules(JSON.stringify({
    rules: [
      { type: "DomainSuffix", payload: "internal.example", proxy: "DIRECT" },
      { type: "ProcessName", payload: "syncthing", proxy: "DIRECT" },
      { type: "DomainSuffix", payload: "tailscale.com", proxy: "GLOBAL" },
      { type: "DomainKeyword", payload: "google", proxy: "Proxy" },
      { type: "Match", payload: "", proxy: "Proxy" }
    ]
  }))
  const own = Model.parseRules(JSON.stringify([
    { index: 1, kind: "prepend", rule: "DOMAIN-SUFFIX,internal.example,DIRECT" },
    { index: 2, kind: "prepend", rule: "PROCESS-NAME,syncthing,DIRECT" }
  ]))
  const split = Model.splitApiRules(merged, own, ["DOMAIN-SUFFIX,tailscale.com,GLOBAL"])
  assert.deepEqual(split.injected.map(r => r.value), ["tailscale.com"])
  assert.deepEqual(split.subscription.map(r => r.value), ["google", ""])
  assert.equal(merged[0].type, "DOMAIN-SUFFIX")
  assert.equal(merged[4].type, "MATCH")
})

test("injected rules the runtime does not carry yet are not peeled", () => {
  const merged = Model.parseApiRules(JSON.stringify({
    rules: [
      { type: "DomainKeyword", payload: "tailscale.com", proxy: "GLOBAL" },
      { type: "Match", payload: "", proxy: "Proxy" }
    ]
  }))
  const split = Model.splitApiRules(merged, [], ["DOMAIN-SUFFIX,tailscale.com,GLOBAL"])
  assert.equal(split.injected.length, 0)
  assert.equal(split.subscription.length, 2)
})

test("append and filter rules never strip a subscription rule", () => {
  const merged = Model.parseApiRules(JSON.stringify({
    rules: [{ type: "DomainKeyword", payload: "google", proxy: "Proxy" }]
  }))
  const own = Model.parseRules(JSON.stringify([
    { index: 1, kind: "append", rule: "DOMAIN-KEYWORD,google,Proxy" }
  ]))
  assert.equal(Model.splitApiRules(merged, own, []).subscription.length, 1)
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

test("mihomo rule types render as config syntax", () => {
  assert.equal(Model.apiRuleType("DomainSuffix"), "DOMAIN-SUFFIX")
  assert.equal(Model.apiRuleType("IPCIDR"), "IP-CIDR")
  assert.equal(Model.apiRuleType("GeoIP"), "GEOIP")
  assert.equal(Model.apiRuleType("Match"), "MATCH")
})

test("one trace call yields both the egress IP and the round trip", () => {
  const trace = Model.parseTrace("fl=123\nip=203.0.113.9\nts=1\n\n0.183\n")
  assert.equal(trace.ip, "203.0.113.9")
  assert.equal(trace.latency, 183)
})

test("a partial traffic line is dropped rather than read as zero", () => {
  assert.deepEqual(Model.parseTraffic('{"up":120,"down":4096}'), { up: 120, down: 4096 })
  assert.equal(Model.parseTraffic('{"up":12'), null)
  assert.equal(Model.parseTraffic(""), null)
})

const snapshot = (connections, extra) => JSON.stringify(Object.assign({
  downloadTotal: 4096, uploadTotal: 512, connections
}, extra || {}))

const conn = (id, host, process, over) => Object.assign({
  id, upload: 1, download: 2, start: "2026-08-18T11:00:00Z",
  chains: ["Tokyo 01", "Proxy"], rule: "DomainSuffix", rulePayload: "example.org",
  metadata: { network: "tcp", host, destinationPort: "443", processPath: process }
}, over || {})

test("connection labels use the API name and identify internal traffic", () => {
  const items = Model.parseConnections(snapshot([
    { id: "named", metadata: { process: "firefox", type: "Tun" } },
    { id: "internal", metadata: { type: "Inner" } }
  ])).items
  assert.equal(items[0].process, "firefox")
  assert.equal(items[1].process, "mihomo")
})

test("socket lookup identifies only unambiguous local connections", () => {
  const item = Model.parseConnections(snapshot([{
    id: "udp", metadata: { type: "Tun", network: "udp", sourceIP: "192.168.42.10",
      sourcePort: "53000", destinationIP: "203.0.113.5", destinationPort: "443" }
  }])).items[0]
  const socket = { network: "udp", localIP: "0.0.0.0", localPort: 53000,
    peerIP: "0.0.0.0", peerPort: 0, process: "firefox" }
  const table = { addresses: ["192.168.42.10"], sockets: [socket] }
  assert.equal(Model.enrichConnections([item], table)[0].process, "firefox")
  assert.equal(Model.enrichConnections([item], { addresses: [], sockets: [socket] })[0].process, "")
  assert.equal(Model.enrichConnections([item], { addresses: table.addresses,
    sockets: [socket, {...socket, process: "another-app"}] })[0].process, "")
  assert.equal(Model.enrichConnections([item], { addresses: table.addresses,
    sockets: [{...socket, network: "tcp"}] })[0].process, "")
  assert.equal(Model.enrichConnections([item], { addresses: table.addresses,
    sockets: [{...socket, peerPort: 443, peerIP: "203.0.113.6"}] })[0].process, "")
  assert.equal(Model.enrichConnections([{...item, process: "known"}], table)[0].process, "known")
})

test("a connection keeps its resolved owner when the socket disappears", () => {
  const raw = snapshot([{id: "udp", metadata: {type: "Tun", network: "udp",
    sourceIP: "192.168.42.10", sourcePort: "53000", destinationIP: "203.0.113.5", destinationPort: "443"}}])
  const first = Model.parseConnections(raw).items
  first[0].process = "firefox"
  assert.equal(Model.mergeConnectionLog(first, Model.parseConnections(raw).items)[0].process, "firefox")
  const changed = Model.parseConnections(raw).items
  changed[0].sourcePort = 53001
  assert.equal(Model.mergeConnectionLog(first, changed)[0].process, "")
})

test("a connection snapshot carries a host, a start, and its totals", () => {
  const now = Date.parse("2026-08-18T12:00:00Z")
  const parsed = Model.parseConnections(snapshot([
    conn("a", "github.com", "/usr/bin/firefox"),
    { id: "b", upload: 0, download: 0, start: "2026-08-18T11:59:00Z", chains: [],
      rule: "Match", metadata: { network: "udp", host: "", destinationIP: "1.1.1.1",
                                 destinationPort: "53" } }
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
  const first = Model.parseConnections(snapshot([
    conn("a", "github.com", "/usr/bin/firefox"),
    conn("b", "cdn.jsdelivr.net", "/usr/bin/firefox")
  ]), 1000)
  let log = Model.mergeConnectionLog([], first.items, 1000, 200)
  assert.equal(Model.openConnectionCount(log), 2)

  // "b" is gone from the second snapshot, and "c" is new.
  const second = Model.parseConnections(snapshot([
    conn("a", "github.com", "/usr/bin/firefox", { download: 900 }),
    conn("c", "github.com", "/usr/bin/firefox")
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
  const open = Model.parseConnections(snapshot([conn("keep", "github.com", "/usr/bin/firefox")]), 1000)
  let log = Model.mergeConnectionLog([], open.items, 1000, 2)
  for (let i = 0; i < 4; i++) {
    const round = Model.parseConnections(snapshot([
      conn("keep", "github.com", "/usr/bin/firefox"),
      conn("gone" + i, "cdn.jsdelivr.net", "/usr/bin/firefox")
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
  const first = Model.parseConnections(snapshot([
    conn("a", "github.com", "/usr/bin/firefox", { start: "2026-08-18T11:00:00Z", download: 10 }),
    conn("b", "github.com", "/usr/bin/firefox", { start: "2026-08-18T11:30:00Z", download: 20 }),
    conn("c", "github.com", "/usr/bin/curl", { start: "2026-08-18T11:45:00Z", download: 30 })
  ]), 1000)
  let log = Model.mergeConnectionLog([], first.items, 1000, 200)

  // Firefox keeps one socket to github and loses the other.
  const second = Model.parseConnections(snapshot([
    conn("a", "github.com", "/usr/bin/firefox", { start: "2026-08-18T11:00:00Z", download: 10 }),
    conn("c", "github.com", "/usr/bin/curl", { start: "2026-08-18T11:45:00Z", download: 30 })
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

test("systemd's ActiveEnterTimestamp parses as local time", () => {
  const ms = Model.parseUnitTimestamp("Tue 2026-08-18 13:06:48 MSK")
  const date = new Date(ms)
  assert.equal(date.getFullYear(), 2026)
  assert.equal(date.getMonth(), 7)
  assert.equal(date.getDate(), 18)
  assert.equal(date.getHours(), 13)
  assert.equal(Model.parseUnitTimestamp(""), 0)
  assert.equal(Model.parseUnitTimestamp("n/a"), 0)
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
  const parsed = Model.parseConfigs(JSON.stringify({tun: {enable: true, "auto-route": true,
    "route-exclude-address": ["192.168.0.0/16"]}}))
  assert.deepEqual(parsed.routing, routing)
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
  const parsed = Model.parseProxies('{"proxies":{"__proto__":{"type":"Vless"}}}')
  assert.deepEqual(Object.keys(parsed.configs), ["__proto__"])
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

test("mode cycles through mihomo's three modes", () => {
  assert.equal(Model.nextMode("rule"), "global")
  assert.equal(Model.nextMode("global"), "direct")
  assert.equal(Model.nextMode("direct"), "rule")
  // An unknown mode lands on the first, rather than skipping past it.
  assert.equal(Model.nextMode(""), "rule")
})

test("a leading flag or emoji splits off a name with its space", () => {
  assert.deepEqual(Model.leadingEmoji("🇩🇪 Германия #1-3 🚀"), { mark: "🇩🇪", rest: "Германия #1-3 🚀" })
  assert.deepEqual(Model.leadingEmoji("🚀  Fast"), { mark: "🚀", rest: "Fast" })
  assert.deepEqual(Model.leadingEmoji("☁️ Cloud"), { mark: "☁️", rest: "Cloud" })
  assert.deepEqual(Model.leadingEmoji("🇩🇪Германия"), { mark: "", rest: "🇩🇪Германия" })
  assert.deepEqual(Model.leadingEmoji("DIRECT"), { mark: "", rest: "DIRECT" })
})
