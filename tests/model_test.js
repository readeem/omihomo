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
    primary_group: "Proxy", tun_enabled: true, autostart_enabled: true
  }))
  assert.equal(status.state, "degraded")
  assert.equal(status.detail, "tun device is missing")
  assert.equal(status.activeSubscription, "home")
  assert.equal(status.primaryGroup, "Proxy")
  assert.equal(status.tunEnabled, true)
  assert.equal(status.autostartEnabled, true)
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
      "Proxy": { name: "Proxy", type: "Selector", now: "Tokyo 01", all: ["Tokyo 01", "DIRECT"] },
      "Auto": { name: "Auto", type: "URLTest", now: "Tokyo 01", all: ["Tokyo 01"] },
      "Tokyo 01": { name: "Tokyo 01", type: "Vmess", udp: true, history: [{ delay: 182 }] },
      "DIRECT": { name: "DIRECT", type: "Direct", history: [] }
    }
  }))
  assert.deepEqual(parsed.groups.map(g => g.name), ["Auto", "Proxy"])
  assert.equal(parsed.groups.find(g => g.name === "Proxy").selectable, true)
  assert.equal(parsed.groups.find(g => g.name === "Auto").selectable, false)
  assert.equal(Model.historyDelay(parsed.configs["Tokyo 01"]), 182)
  assert.equal(parsed.configs["Proxy"], undefined)
})

test("the primary group falls back to the first selectable group", () => {
  const groups = [
    { name: "Auto", selectable: false },
    { name: "Proxy", selectable: true }
  ]
  assert.equal(Model.primaryGroupName(groups, "Proxy"), "Proxy")
  assert.equal(Model.primaryGroupName(groups, ""), "Proxy")
  assert.equal(Model.primaryGroupName(groups, "Deleted"), "Proxy")
  assert.equal(Model.primaryGroupName([], "Proxy"), "")
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

test("connections carry a host, a duration, and their totals", () => {
  const now = Date.parse("2026-08-18T12:00:00Z")
  const parsed = Model.parseConnections(JSON.stringify({
    downloadTotal: 4096, uploadTotal: 512,
    connections: [
      { id: "a", upload: 1, download: 2, start: "2026-08-18T11:00:00Z",
        chains: ["Tokyo 01", "Proxy"], rule: "DomainSuffix", rulePayload: "example.org",
        metadata: { network: "tcp", host: "github.com", destinationPort: "443",
                    processPath: "/usr/bin/firefox" } },
      { id: "b", upload: 0, download: 0, start: "2026-08-18T11:59:00Z", chains: [],
        rule: "Match", metadata: { network: "udp", host: "", destinationIP: "1.1.1.1",
                                   destinationPort: "53" } }
    ]
  }), now)
  assert.equal(parsed.downloadTotal, 4096)
  // Newest first: the one-minute-old connection sorts above the hour-old one.
  assert.deepEqual(parsed.items.map(c => c.id), ["b", "a"])
  assert.equal(parsed.items[0].host, "1.1.1.1:53")
  assert.equal(parsed.items[1].host, "github.com:443")
  assert.equal(parsed.items[1].chain, "Tokyo 01")
  assert.equal(parsed.items[1].rule, "DomainSuffix(example.org)")
  assert.equal(parsed.items[1].process, "firefox")
  assert.equal(Model.formatDuration(parsed.items[1].durationMs), "1h 0m")
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
