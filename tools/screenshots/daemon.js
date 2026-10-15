#!/usr/bin/env node
// Stands in for the omihomo daemon during a screenshot capture: it speaks the
// socket protocol (docs/daemon.md) and answers from data/. Nothing leaves the
// machine, and the live core and its state are never touched.
//
//   daemon.js SOCKET DATA_DIR STARTED_UNIX_SECONDS
"use strict"

const fs = require("node:fs")
const net = require("node:net")
const path = require("node:path")

const [socketPath, data, started] = process.argv.slice(2)
const read = (name) => fs.readFileSync(path.join(data, name), "utf8")
const json = (name) => JSON.parse(read(name))
const lines = (name) => read(name).split("\n").filter((line) => line !== "").map((line) => JSON.parse(line))

const startedAt = Number(started) * 1000
const status = {
  state: "on", detail: "", active_subscription: "Aerowave", primary_group: "Proxy", mode: "rule",
  tun: true, tun_redirect: false, tailscale: true, tailscale_present: true, tailnet_proxy: false,
  started_at: startedAt, tun_bypass: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10"]
}
const groups = json("groups.json")
const rules = json("rules.json")
const subscriptions = { subscriptions: json("subscriptions.json").map(({ updated_ago, ...sub }) =>
  ({ ...sub, updated_at: Date.now() - updated_ago * 1000 })) }
const traffic = lines("traffic.jsonl")
const allConnections = lines("connections.jsonl")

// The first push also lists the connections that close, so the panel's log
// keeps them as closed from the second push on.
let connectionPushes = 0
function connections() {
  const now = Date.now()
  const open = allConnections.filter((c) => connectionPushes === 0 || !c.closes)
  connectionPushes++
  return { upload_total: 412000000, download_total: 5100000000, open: open.length,
    connections: open.map(({ age, closes, ...c }) => ({ ...c, started_at: now - age * 1000 })) }
}

const snapshots = { status: () => status, groups: () => groups, rules: () => rules,
  subscriptions: () => subscriptions }

function send(client, message) {
  if (!client.destroyed) client.write(JSON.stringify(message) + "\n")
}

function reply(client, id, result, error) {
  send(client, error ? { type: "reply", id, error } : { type: "reply", id, result })
}

function handle(client, request) {
  const args = request.args || {}
  switch (request.op) {
  case "status": return reply(client, request.id, status)
  case "subscribe":
    for (const stream of args.streams) {
      client.streams.add(stream)
      if (snapshots[stream]) send(client, { type: "event", stream, data: snapshots[stream]() })
      // The whole last minute of traffic at once, so the graph is full.
      if (stream === "traffic") traffic.forEach((data) => send(client, { type: "event", stream, data }))
    }
    return reply(client, request.id, {})
  case "unsubscribe":
    args.streams.forEach((stream) => client.streams.delete(stream))
    return reply(client, request.id, {})
  case "delay.test":
    return setTimeout(() => reply(client, request.id, json("group-delay.json")), 300)
  case "egress.check": return reply(client, request.id, { ip: "203.0.113.47", latency: 85 })
  }
  reply(client, request.id, null, { code: "unsupported", message: "not available in a screenshot capture" })
}

setInterval(() => {
  for (const client of clients) {
    if (client.streams.has("connections")) send(client, { type: "event", stream: "connections", data: connections() })
  }
}, 500)

const clients = new Set()
try { fs.unlinkSync(socketPath) } catch (e) {}
net.createServer((client) => {
  client.streams = new Set()
  clients.add(client)
  client.on("close", () => clients.delete(client))
  client.on("error", () => {})
  send(client, { type: "hello", protocol: 1, version: "v2.0.0" })
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
