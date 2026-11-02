#!/usr/bin/env node
// Stands in for the omihomo daemon while the panel is captured or driven: it
// speaks the socket protocol (internal/protocol), starts from data/, and keeps
// the panel's changes in memory. Nothing leaves the machine, and the live core
// and its state are never touched.
//
//   daemon.js SOCKET DATA_DIR STARTED_UNIX_SECONDS [--after FIFO]
//
// With --after it is a core not yet installed: it listens, with a fresh
// runtime, only once a line arrives on FIFO.
"use strict"

const fs = require("node:fs")
const net = require("node:net")
const path = require("node:path")

const [socketPath, data, started, after, fifo] = process.argv.slice(2)
if (after === "--after") fs.readFileSync(fifo)
const read = (name) => fs.readFileSync(path.join(data, name), "utf8")
const json = (name) => JSON.parse(read(name))
const lines = (name) => read(name).split("\n").filter((line) => line !== "").map((line) => JSON.parse(line))

const status = {
  state: "on", detail: "", active_subscription: "Aerowave", ...json("settings/settings.json"),
  tailscale_present: true, started_at: after === "--after" ? Date.now() : Number(started) * 1000,
  tun_bypass: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10"]
}
let userRules = json("settings/rules.json")
const runtimeRules = json("rules.json").rules
const groups = json("groups.json")
const targets = new Set(["DIRECT", "REJECT", "REJECT-DROP"].concat(
  groups.groups.flatMap((group) => [group.name].concat(group.members.map((member) => member.name)))))
const delays = json("group-delay.json").delays
let subscriptions = json("subscriptions.json").map(({ updated_ago, ...sub }) =>
  ({ ...sub, updated_at: Date.now() - updated_ago * 1000 }))
const traffic = lines("traffic.jsonl")
const allConnections = lines("connections.jsonl")
const running = () => status.state !== "stopped"

// Hosting stands in for the real adapter: a relay whose host starts with
// "down." is unreachable, any other one is connected while the proxy is on, a
// credential is stored and a share is enabled. Only the share and relay
// fields are checked here; the daemon owns the real validation.
const hostingData = json("hosting.json")
const hosting = { relay: hostingData.relay, credentialId: hostingData.credential_id, paused: false,
  shares: hostingData.shares, nextShare: hostingData.shares.length + 1, changedAt: Date.now() }
const shareFields = ({ active, ...share }) => share
let lastHostingState = ""
function hostingSnapshot() {
  const live = hosting.relay && hosting.credentialId !== "" && !hosting.paused && running()
    && hosting.shares.some((share) => share.enabled)
  const down = live && hosting.relay.host.startsWith("down.")
  const state = !live ? "idle" : down ? "error" : "connected"
  if (state !== lastHostingState) { lastHostingState = state; hosting.changedAt = Date.now() }
  const shares = hosting.shares.map((share) => ({ ...shareFields(share), active: live && !down && share.enabled ? share.active || 0 : 0 }))
  const out = { config: { ...(hosting.relay ? { relay: hosting.relay } : {}), shares: shares.map(shareFields), paused: hosting.paused },
    state, changed_at: hosting.changedAt, active: shares.reduce((sum, share) => sum + share.active, 0), shares,
    refused: down ? 0 : 3, dropped: 0, oversized: 0,
    credential_present: hosting.credentialId !== "", credential_id: hosting.credentialId }
  if (down) out.reason = "relay unavailable: connection refused"
  return out
}

// The first push also lists the connections that close, so the panel's log
// keeps them as closed from the second push on.
const closed = new Set()
let connectionPushes = 0
function connections() {
  const now = Date.now()
  const open = allConnections.filter((c) => !closed.has(c.id) && (connectionPushes === 0 || !c.closes))
  connectionPushes++
  return { upload_total: 412000000, download_total: 5100000000, open: open.length,
    connections: open.map(({ age, closes, ...c }) => ({ ...c, started_at: now - age * 1000 })) }
}

function ruleSnapshot() {
  let index = 0
  let caught = false
  const own = []
  function add(rule, category, disabled) {
    const inactive = disabled || rule.disabled === true || !targets.has(rule.target)
    own.push({ ...rule, category, index: ++index, source: "user", inactive,
      inactive_reason: disabled ? "Category disabled" : rule.disabled ? "Rule disabled" : inactive ? "Unavailable target or rule provider" : "" })
  }
  userRules.forEach(entry => {
    if (entry.category) entry.category.rules.forEach(r => add(r,entry.category.id,entry.disabled))
    else add(entry,"",false)
  })
  const rules = own.concat(runtimeRules.map(r => {
    const disabled = r.source === "omihomo" ? status.omihomo_rules_disabled === true : status.subscription_rules_disabled === true
    return { ...r, disabled, inactive: disabled, category: r.source === "omihomo" ? "omihomo" : "subscription" }
  }))
  rules.forEach(r => {
    if (r.inactive) return
    r.unreachable = caught
    if (r.type === "MATCH") caught = true
  })
  return { entries: userRules, rules, omihomo_disabled: status.omihomo_rules_disabled === true,
    subscription_disabled: status.subscription_rules_disabled === true }
}

const snapshots = {
  status: () => status,
  groups: () => groups,
  rules: ruleSnapshot,
  subscriptions: () => ({ subscriptions }),
  hosting: hostingSnapshot
}

const clients = new Set()

function send(client, message) {
  if (!client.destroyed) client.write(JSON.stringify(message) + "\n")
}

function push(...streams) {
  for (const client of clients) for (const stream of streams) {
    if (client.streams.has(stream)) send(client, { type: "event", stream, data: snapshots[stream]() })
  }
}

const fail = (code, message) => ({ error: { code, message } })
const later = (ms, answer) => new Promise((resolve) => setTimeout(() => resolve(answer()), ms))

function findSubscription(name) {
  return subscriptions.find((sub) => sub.name === name)
}

function findGroup(name) {
  return groups.groups.find((group) => group.name === name)
}

// Each op answers { result } or { error }, or a promise of one.
const ops = {
  "test.stream-error": (args) => {
    for (const client of clients) {
      if (client.streams.has(args.stream)) send(client, { type: "event", stream: args.stream,
        error: { code: "too_large", message: args.stream + " snapshot exceeds the event size limit" } })
    }
    return { result: {} }
  },
  status: () => ({ result: status }),
  subscribe: (args, client) => {
    for (const stream of args.streams) {
      if (client.streams.has(stream)) continue
      client.streams.add(stream)
      if (snapshots[stream]) send(client, { type: "event", stream, data: snapshots[stream]() })
      // The whole last minute of traffic at once, so the graph is full.
      if (stream === "traffic") traffic.forEach((data) => send(client, { type: "event", stream, data }))
    }
    return { result: {} }
  },
  unsubscribe: (args, client) => {
    args.streams.forEach((stream) => client.streams.delete(stream))
    return { result: {} }
  },
  start: () => later(400, () => {
    Object.assign(status, { state: "on", detail: "", started_at: Date.now() })
    push("status", "rules", "hosting")
    return { result: status }
  }),
  stop: () => {
    status.state = "stopped"
    push("status", "rules", "hosting")
    return { result: status }
  },
  "settings.apply": (args) => {
    const { mode, tun, tun_redirect, tailscale, tailnet_proxy, primary_group, dns_destination } = args.settings
    if (!["rule", "global", "direct"].includes(mode)) return fail("invalid", `unknown mode ${mode}`)
    if (primary_group !== "" && !findGroup(primary_group)) return fail("not_found", `no group ${primary_group}`)
    Object.assign(status, { dns_destination: dns_destination || "", mode, tun, tun_redirect, tailscale, tailnet_proxy, primary_group: primary_group || "Proxy" })
    status.omihomo_rules_disabled = args.settings.omihomo_rules_disabled === true
    status.subscription_rules_disabled = args.settings.subscription_rules_disabled === true
    userRules = args.rules
    push("status", "rules")
    return { result: status }
  },
  ...hostingOps(),
  "rules.list": () => ({ result: snapshots.rules() }),
  "groups.list": () => running() ? { result: groups } : fail("not_running", "the proxy is stopped"),
  "groups.select": (args) => {
    const group = findGroup(args.group)
    if (!group) return fail("not_found", `no group ${args.group}`)
    if (!group.selectable) return fail("invalid", `${group.name} picks its own member`)
    if (!group.members.some((m) => m.name === args.proxy)) return fail("not_found", `no member ${args.proxy}`)
    group.now = args.proxy
    push("groups")
    return { result: group }
  },
  "delay.test": (args) => later(300, () => {
    const group = findGroup(args.group)
    if (!group) return fail("not_found", `no group ${args.group}`)
    const result = {}
    for (const member of group.members) {
      if (args.proxies && !args.proxies.includes(member.name)) continue
      member.delay = result[member.name] = delays[member.name] || 0
      member.alive = member.delay > 0
    }
    push("groups")
    return { result: { delays: result } }
  }),
  "connections.close": (args) => {
    allConnections.filter((c) => args.all || args.ids.includes(c.id)).forEach((c) => closed.add(c.id))
    return { result: {} }
  },
  "egress.check": () => ({ result: { ip: "203.0.113.47", latency: 85 } }),
  "subscriptions.add": (args) => later(600, () => {
    let name
    try { name = new URL(args.url).hostname } catch (e) {}
    if (!name) return fail("invalid", "the URL is not valid")
    if (findSubscription(name)) return fail("conflict", `${name} is already added`)
    const sub = { name, updated_at: Date.now(), upload: null, download: null, total: null, expire: null,
      active: false, interval: 43200, next_update: 0, error: "" }
    subscriptions.push(sub)
    push("subscriptions")
    return { result: sub }
  }),
  "subscriptions.update": (args) => later(600, () => {
    const sub = findSubscription(args.name)
    if (!sub) return fail("not_found", `no subscription ${args.name}`)
    sub.updated_at = Date.now()
    push("subscriptions")
    return { result: sub }
  }),
  "subscriptions.activate": (args) => {
    const sub = findSubscription(args.name)
    if (!sub) return fail("not_found", `no subscription ${args.name}`)
    subscriptions.forEach((s) => { s.active = s === sub })
    status.active_subscription = sub.name
    push("status", "subscriptions")
    return { result: status }
  },
  "subscriptions.remove": (args) => {
    const sub = findSubscription(args.name)
    if (!sub) return fail("not_found", `no subscription ${args.name}`)
    if (sub.active && running()) {
      return fail("conflict", `${sub.name} is running; stop the proxy or activate another subscription first`)
    }
    subscriptions = subscriptions.filter((s) => s !== sub)
    if (sub.active) status.active_subscription = ""
    push("status", "subscriptions")
    return { result: {} }
  }
}

function hostingOps() {
  const done = () => { push("hosting"); return { result: hostingSnapshot() } }
  const port = (value) => Number.isInteger(value) && value >= 1 && value <= 65535
  const text = (value, max) => typeof value === "string" && value.trim() !== "" && Buffer.byteLength(value) <= max
  const find = (id) => hosting.shares.find((share) => share.id === id)
  const overlaps = (share) => hosting.shares.some((other) => other.id !== share.id && other.exposed_port === share.exposed_port
    && (other.protocol === "both" || share.protocol === "both" || other.protocol === share.protocol))
  return {
    "hosting.status": () => ({ result: hostingSnapshot() }),
    "hosting.relay.set": ({ relay }) => {
      const reality = relay && relay.reality
      if (!relay || relay.version !== 1 || !text(relay.label, 64) || !text(relay.host, 253) || !port(relay.port)
        || !/^10\.|^172\.(1[6-9]|2\d|3[01])\.|^192\.168\./.test(String(relay.address)) || !reality
        || !text(reality.server_name, 253) || !text(reality.public_key, 64) || !/^([0-9a-f]{2}){1,8}$/i.test(String(reality.short_id)))
        return fail("invalid", "invalid: relay")
      hosting.relay = { ...relay, credential_ref: "hosting-credential" }
      return done()
    },
    "hosting.relay.remove": () => {
      hosting.relay = null
      hosting.credentialId = ""
      hosting.shares.forEach((share) => { share.enabled = false })
      return done()
    },
    "hosting.credential.set": ({ credential }) => {
      if (!Array.isArray(credential) || credential.length !== 36 || !credential.every((b) => Number.isInteger(b) && b >= 0 && b < 128)
        || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(String.fromCharCode(...credential)))
        return fail("invalid", "invalid: credential")
      hosting.credentialId = "hosting-credential"
      return done()
    },
    "hosting.credential.remove": () => { hosting.credentialId = ""; return done() },
    "hosting.share.save": ({ share }) => {
      if (!share || !text(share.label, 64) || !["127.0.0.1", "::1"].includes(share.local_address) || !port(share.local_port)
        || !port(share.exposed_port) || !["tcp", "udp", "both"].includes(share.protocol)) return fail("invalid", "invalid: share")
      const existing = share.id === "" ? null : find(share.id)
      if (share.id !== "" && !existing) return fail("not_found", "no such share")
      const saved = { id: existing ? existing.id : "share-" + hosting.nextShare++, label: share.label, local_address: share.local_address,
        local_port: share.local_port, exposed_port: share.exposed_port, protocol: share.protocol,
        enabled: existing ? existing.enabled : false, active: existing ? existing.active : 0 }
      if (overlaps(saved)) return fail("invalid", "invalid: duplicate exposed port")
      if (existing) Object.assign(existing, saved)
      else hosting.shares.push(saved)
      return done()
    },
    "hosting.share.remove": ({ id }) => {
      if (!find(id)) return fail("not_found", "no such share")
      hosting.shares = hosting.shares.filter((share) => share.id !== id)
      return done()
    },
    "hosting.share.enable": ({ id, enabled }) => {
      const share = find(id)
      if (!share) return fail("not_found", "no such share")
      share.enabled = enabled === true
      return done()
    },
    "hosting.pause": ({ paused }) => { hosting.paused = paused === true; return done() },
    "hosting.retry": () => done()
  }
}

async function handle(client, request) {
  const op = ops[request.op]
  const answer = op ? await op(request.args || {}, client) : fail("unsupported", `${request.op} is not faked`)
  send(client, { type: "reply", id: request.id, ...answer })
}

setInterval(() => {
  if (!running()) return
  for (const client of clients) {
    if (client.streams.has("connections")) send(client, { type: "event", stream: "connections", data: connections() })
  }
}, 500)

try { fs.unlinkSync(socketPath) } catch (e) {}
net.createServer((client) => {
  client.streams = new Set()
  clients.add(client)
  client.on("close", () => clients.delete(client))
  client.on("error", () => {})
  send(client, { type: "hello", protocol: 3, version: "v1.0.0" })
  let buffer = ""
  client.on("data", (chunk) => {
    buffer += chunk
    let newline
    while ((newline = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, newline)
      buffer = buffer.slice(newline + 1)
      if (line !== "") handle(client, JSON.parse(line))
    }
  })
}).listen(socketPath)
