#!/usr/bin/env node
// A fake omihomo daemon for the widget's QML state tests. It speaks the
// daemon's socket protocol (docs/daemon.md) from memory: no core, no root, no
// network. Each test gets its own socket, and the flags shape what it does:
//
//   fake-daemon.js SOCKET [--protocol N] [--refuse] [--latency MS] [--fail OP,...]
//
// --protocol  the version its hello announces, 2 by default
// --refuse    greet every client with `refused` and hang up
// --latency   how long start, groups.select, delay.test and egress.check take
// --fail      ops that answer `core_failed` instead
//
// Test-only ops, which the widget never sends, let a test steer it:
//
//   test.drop    disconnect every client
//   test.status  merge its args into the status and push it
//   test.push    push {stream, data} as is
"use strict"

const fs = require("node:fs")
const net = require("node:net")

const args = process.argv.slice(2)
const socketPath = args.shift()
const options = { protocol: 2, refuse: false, busy: 0, latency: 0, fail: new Set() }
while (args.length > 0) {
  const flag = args.shift()
  if (flag === "--protocol") options.protocol = Number(args.shift())
  else if (flag === "--refuse") options.refuse = true
  else if (flag === "--busy-once") options.busy = 1
  else if (flag === "--latency") options.latency = Number(args.shift())
  else if (flag === "--fail") args.shift().split(",").forEach((op) => options.fail.add(op))
  else throw new Error("unknown flag " + flag)
}

const status = {
  state: "stopped", detail: "", active_subscription: "work", primary_group: "",
  mode: "rule", tun: false, tun_redirect: false, tailscale: false, tailscale_present: true,
  tailnet_proxy: false, started_at: 0, tun_bypass: ["10.0.0.0/8", "192.168.0.0/16"]
}
const delays = { Pass: 40, Fail: 50 }
const proxy = { name: "Proxy", type: "select", now: "Pass", selectable: true }
let rules = []
let subscriptions = [
  { name: "work", updated_at: 1791741355865, upload: 1024, download: 2048, total: 1 << 30,
    expire: null, active: true, interval: 43200, next_update: 0, error: "" }
]
let nextSubscription = 1
let heldEdit = null
let holdEdit = false
const clients = new Set()

function running() {
  return status.state === "on" || status.state === "degraded"
}

function groups() {
  if (!running()) return { groups: [] }
  const members = Object.keys(delays).map((name) =>
    ({ name, type: "vless", alive: delays[name] > 0, delay: delays[name] }))
  return { groups: [{ ...proxy, members }] }
}

function ruleList() {
  const own = rules.map((rule) => ({ ...rule, source: "user" }))
  if (!running()) return { rules: own }
  return { rules: own.concat(
    { type: "DOMAIN-SUFFIX", value: "ts.net", target: "DIRECT", source: "omihomo" },
    { type: "MATCH", value: "", target: "Proxy", source: "subscription" }) }
}

function connections() {
  return { upload_total: 10, download_total: 20, open: 1, connections: [{
    id: "c1", network: "tcp", inbound: "tun", host: "example.com", process: "firefox",
    source_ip: "198.18.0.1", source_port: 50000, destination_ip: "93.184.215.14",
    destination_port: 443, chain: "Pass", rule: "MATCH", upload: 10, download: 20,
    started_at: Date.now() - 1000
  }] }
}

const snapshots = {
  status: () => status,
  groups,
  rules: ruleList,
  subscriptions: () => ({ subscriptions })
}

function send(client, message) {
  if (!client.destroyed) client.write(JSON.stringify(message) + "\n")
}

function push(stream, data) {
  for (const client of clients) {
    if (client.streams.has(stream)) send(client, { type: "event", stream, data })
  }
}

function publish() {
  for (const stream of Object.keys(snapshots)) push(stream, snapshots[stream]())
}

function later(fn) {
  return new Promise((resolve) => setTimeout(() => resolve(fn()), options.latency))
}

function failure(code, message) {
  return { error: { code, message } }
}

const ops = {
  "status": () => ({ result: status }),
  "subscribe": (client, a) => {
    for (const stream of a.streams) {
      if (client.streams.has(stream)) continue
      client.streams.add(stream)
      if (snapshots[stream]) send(client, { type: "event", stream, data: snapshots[stream]() })
    }
    return { result: {} }
  },
  "unsubscribe": (client, a) => {
    a.streams.forEach((stream) => client.streams.delete(stream))
    return { result: {} }
  },
  "start": () => later(() => {
    status.state = "on"
    status.started_at = Date.now()
    publish()
    return { result: status }
  }),
  "stop": () => {
    status.state = "stopped"
    status.started_at = 0
    publish()
    return { result: status }
  },
  "settings.apply": (client, a) => {
    if (!["rule", "global", "direct"].includes(a.settings.mode))
      return failure("invalid", "mode must be rule, global or direct")
    if (a.settings.tailscale === true && !status.tailscale_present)
      return failure("conflict", "tailscaled is not installed")
    Object.assign(status, { mode: "rule", tun: false, tun_redirect: false, tailscale: false,
      tailnet_proxy: false, primary_group: "" }, a.settings)
    rules = a.rules
    publish()
    if (holdEdit) return new Promise((resolve) => { heldEdit = () => resolve({ result: { ...status } }) })
    return { result: status }
  },
  "test.hold-edit": () => { holdEdit = true; return { result: {} } },
  "test.release-edit": () => {
    holdEdit = false
    if (heldEdit) heldEdit()
    heldEdit = null
    return { result: {} }
  },
  "groups.list": () => ({ result: groups() }),
  "groups.select": (client, a) => later(() => {
    if (!running()) return failure("not_running", "the proxy is not running")
    if (a.group !== proxy.name || delays[a.proxy] === undefined) return failure("not_found", "not found")
    proxy.now = a.proxy
    const group = groups().groups[0]
    push("groups", groups())
    return { result: group }
  }),
  "delay.test": (client, a) => later(() => {
    if (!running()) return failure("not_running", "the proxy is not running")
    const names = a.proxies && a.proxies.length > 0 ? a.proxies : Object.keys(delays)
    const result = {}
    // A config named Fail* never answers; the rest take 91 ms.
    for (const name of names) {
      delays[name] = name.startsWith("Fail") ? 0 : 91
      result[name] = delays[name]
    }
    push("groups", groups())
    return { result: { delays: result } }
  }),
  "egress.check": () => later(() => running()
    ? { result: { ip: "203.0.113.7", latency: 42 } }
    : failure("not_running", "the proxy is not running")),
  "rules.list": () => ({ result: ruleList() }),
  "subscriptions.list": () => ({ result: { subscriptions } }),
  "subscriptions.add": (client, a) => {
    if (!a.url.startsWith("https://")) return failure("fetch_failed", "only HTTPS is allowed")
    const sub = { ...subscriptions[0], name: "added-" + nextSubscription++, active: false }
    subscriptions.push(sub)
    push("subscriptions", { subscriptions })
    return { result: sub }
  },
  "subscriptions.update": (client, a) => {
    const sub = subscriptions.find((entry) => entry.name === a.name)
    if (!sub) return failure("not_found", "no such subscription")
    sub.updated_at = Date.now()
    push("subscriptions", { subscriptions })
    return { result: sub }
  },
  "subscriptions.activate": (client, a) => {
    if (!subscriptions.some((entry) => entry.name === a.name)) return failure("not_found", "no such subscription")
    subscriptions.forEach((entry) => { entry.active = entry.name === a.name })
    status.active_subscription = a.name
    if (running()) status.started_at = Date.now()
    publish()
    return { result: status }
  },
  "subscriptions.remove": (client, a) => {
    subscriptions = subscriptions.filter((entry) => entry.name !== a.name)
    push("subscriptions", { subscriptions })
    return { result: {} }
  },
  "connections.list": () => ({ result: connections() }),
  "connections.close": () => ({ result: {} }),
  "test.drop": () => {
    setImmediate(() => clients.forEach((client) => client.destroy()))
    return { result: {} }
  },
  "test.status": (client, a) => {
    Object.assign(status, a)
    publish()
    return { result: status }
  },
  "test.push": (client, a) => {
    push(a.stream, a.data)
    return { result: {} }
  }
}

async function handle(client, line) {
  const request = JSON.parse(line)
  const op = ops[request.op]
  let answer
  if (!op) answer = failure("unknown_op", "unknown op " + request.op)
  else if (options.fail.has(request.op)) answer = await later(() => failure("core_failed", request.op + " failed"))
  else answer = await op(client, request.args || {})
  send(client, { type: "reply", id: request.id, ...answer })
}

// Periodic streams, faster than the daemon's once a second so tests stay short.
setInterval(() => {
  if (!running()) return
  push("traffic", { up: 1, down: 2 })
  push("connections", connections())
}, 100)

try { fs.unlinkSync(socketPath) } catch (e) {}
const server = net.createServer((client) => {
  if (options.busy > 0) {
    options.busy--
    client.end(JSON.stringify({ type: "refused",
      error: { code: "busy", message: "too many clients" } }) + "\n")
    return
  }
  if (options.refuse) {
    client.end(JSON.stringify({ type: "refused",
      error: { code: "forbidden", message: "this account does not own omihomo" } }) + "\n")
    return
  }
  client.streams = new Set()
  clients.add(client)
  client.on("close", () => clients.delete(client))
  client.on("error", () => {})
  send(client, { type: "hello", protocol: options.protocol, version: "v0.0.0-fake" })
  let buffer = ""
  client.on("data", (chunk) => {
    buffer += chunk
    let newline
    while ((newline = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, newline)
      buffer = buffer.slice(newline + 1)
      if (line !== "") handle(client, line)
    }
  })
})
server.listen(socketPath, () => console.log("FAKE_DAEMON_READY " + socketPath))
process.on("SIGTERM", () => { server.close(); process.exit(0) })
