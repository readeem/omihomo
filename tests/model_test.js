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
    permissions_ok: true, tailscale_enabled: true, tailscale_present: true
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

test("subscription detail shows quota, expiry, and staleness", () => {
  const now = Date.parse("2026-08-18T12:00:00Z")
  const detail = Model.subscriptionDetail({
    name: "home",
    updatedAt: "2026-08-12T12:00:00Z",
    upload: 12000000000,
    download: 130000000000,
    total: 500000000000,
    expire: Date.parse("2027-03-12T00:00:00Z") / 1000
  }, now)
  assert.match(detail, /142 GB of 500 GB/)
  assert.match(detail, /expires 12 Mar/)
  assert.match(detail, /updated 6d ago/)
})

test("a subscription with no userinfo shows only its staleness", () => {
  const now = Date.parse("2026-08-18T12:00:00Z")
  const detail = Model.subscriptionDetail({
    name: "backup", updatedAt: "2026-08-18T09:00:00Z",
    upload: null, download: null, total: null, expire: null
  }, now)
  assert.equal(detail, "updated 3h ago")
})

test("rule validation mirrors the CLI's per-type rules", () => {
  assert.equal(Model.validateRule("IP-CIDR", "10.0.0.0/8", "DIRECT"), "")
  assert.equal(Model.validateRule("IP-CIDR", "fd00::/64", "DIRECT"), "")
  assert.notEqual(Model.validateRule("IP-CIDR", "example.com", "DIRECT"), "")
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
      { type: "DomainKeyword", payload: "google", proxy: "Proxy" },
      { type: "Match", payload: "", proxy: "Proxy" }
    ]
  }))
  const own = Model.parseRules(JSON.stringify([
    { index: 1, kind: "prepend", rule: "DOMAIN-SUFFIX,internal.example,DIRECT" },
    { index: 2, kind: "prepend", rule: "PROCESS-NAME,syncthing,DIRECT" }
  ]))
  const theirs = Model.stripOwnRules(merged, own)
  assert.deepEqual(theirs.map(r => r.value), ["google", ""])
  assert.equal(merged[0].type, "DOMAIN-SUFFIX")
  assert.equal(merged[3].type, "MATCH")
})

test("append and filter rules never strip a subscription rule", () => {
  const merged = Model.parseApiRules(JSON.stringify({
    rules: [{ type: "DomainKeyword", payload: "google", proxy: "Proxy" }]
  }))
  const own = Model.parseRules(JSON.stringify([
    { index: 1, kind: "append", rule: "DOMAIN-KEYWORD,google,Proxy" }
  ]))
  assert.equal(Model.stripOwnRules(merged, own).length, 1)
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

test("config parameters stay inside what mihomo actually states", () => {
  const parameters = Model.configParameters({
    name: "Tokyo 01", type: "Vmess", udp: true, xudp: false, tfo: false,
    alive: true, history: [{ delay: 182 }],
    server: "should-never-render", port: 443
  })
  const labels = parameters.map(p => p.label)
  assert.deepEqual(labels, ["Type", "UDP", "Alive", "Latency"])
  assert.equal(parameters[3].value, "182 ms")
  assert.deepEqual(Model.configParameters(null), [])
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
