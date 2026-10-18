#!/usr/bin/env node
// Stands in for the omihomo daemon while the panel is captured or driven: it
// speaks the socket protocol (docs/daemon.md), starts from data/, and keeps
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

const snapshots = {
  status: () => status,
  groups: () => groups,
  rules: () => ({ rules: userRules.map((rule) => ({ ...rule, source: "user", inactive: !targets.has(rule.target) }))
    .concat(running() ? runtimeRules : []) }),
  subscriptions: () => ({ subscriptions })
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
  status: () => ({ result: status }),
  subscribe: (args, client) => {
    for (const stream of args.streams) {
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
    push("status", "rules")
    return { result: status }
  }),
  stop: () => {
    status.state = "stopped"
    push("status", "rules")
    return { result: status }
  },
  "settings.apply": (args) => {
    const { mode, tun, tun_redirect, tailscale, tailnet_proxy, primary_group } = args.settings
    if (!["rule", "global", "direct"].includes(mode)) return fail("invalid", `unknown mode ${mode}`)
    if (primary_group !== "" && !findGroup(primary_group)) return fail("not_found", `no group ${primary_group}`)
    Object.assign(status, { mode, tun, tun_redirect, tailscale, tailnet_proxy, primary_group: primary_group || "Proxy" })
    userRules = args.rules
    push("status", "rules")
    return { result: status }
  },
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
  send(client, { type: "hello", protocol: 2, version: "v1.0.0" })
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
