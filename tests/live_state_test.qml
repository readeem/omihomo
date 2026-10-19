import QtQuick
import Quickshell
import "." as Omihomo

// An open panel's live state against a fake daemon whose start, selection,
// delay tests and egress checks each take 150 ms: latency checks, selection,
// egress, traffic, connections, and a subscription switch.
ShellRoot {
  id: testRoot

  Omihomo.StateTest { id: t; name: "LIVE" }

  Omihomo.Service {
    id: svc
    socketPath: t.socket("ok")
    panelOpen: true
  }

  Component.onCompleted: t.steps = [
    { name: "connect", until: function() { return svc.ready && svc.coreState === "stopped" }, then: function() {
        t.check(svc.proxiesLoaded, false, "a stopped proxy has no groups to load")
        svc.testConfig("Pass")
        t.check(svc.configTestState("Pass"), "", "nothing is tested while the proxy is stopped")
        svc.toggleCore()
      } },
    { name: "start", until: function() { return svc.proxiesLoaded && svc.currentConfig !== "" }, then: function() {
        t.check(svc.currentConfig, "Pass", "the first push shows the core's selection")
        t.check(svc.configDelay("Pass"), 40, "configs carry their last delay")
        svc.testConfig("Fail")
        t.check(svc.configTestState("Fail"), "testing", "a single test starts at once")
        t.check(svc.pingTestsRunning, true, "a single test is in flight")
        svc.testConfig("Pass")
        t.check(svc.configTestState("Pass"), "", "one test runs at a time")
      } },
    { name: "single failure", until: function() { return !svc.pingTestsRunning }, then: function() {
        t.check(svc.configTestState("Fail"), "failed", "a single failure is recorded")
        svc.applyGroups({ groups: [{ name: "Proxy", type: "select", now: "Pass", selectable: true,
          members: [{ name: "Pass", delay: 40, alive: true }, { name: "Fail", delay: 50, alive: true }] }] })
        t.check(svc.configTestState("Fail"), "failed", "an older push cannot erase a failure")
        svc.testGroup("Proxy")
        t.check(svc.configTestState("Pass"), "testing", "a group test marks every config at once")
        t.check(svc.configTestState("Fail"), "testing", "a group test marks every config at once")
      } },
    { name: "group test", until: function() { return !svc.pingTestsRunning }, then: function() {
        t.check(svc.configTestState("Pass"), "success", "a group success is recorded")
        t.check(svc.configDelay("Pass"), 91, "a group success records its delay")
        t.check(svc.configTestState("Fail"), "failed", "a group failure is recorded")
        t.check(svc.groupTestedAt.Proxy > 0, true, "a group test records when it ran")
      } },
    // Opening the panel on a stopped proxy checks nothing; the start does.
    { name: "first egress", until: function() { return !svc.traceTesting && svc.egressIp !== "" }, then: function() {
        t.check(svc.egressIp, "203.0.113.7", "the egress check reports the exit address")
        t.check(svc.egressLatency, 42, "the egress check reports the round trip")
        svc.selectConfig("Proxy", "Fail")
        t.check(svc.pendingConfig, "Fail", "a selection is pending until its reply")
        svc.egressIp = ""
        svc.refreshTrace()
        t.check(svc.traceTesting, true, "an egress check shows at once")
        t.check(svc._traceQueued, true, "an egress check waits for a pending selection")
      } },
    { name: "selection", until: function() { return svc.pendingConfig === "" }, then: function() {
        t.check(svc.currentConfig, "Fail", "the selection's reply shows the new config")
        t.check(svc.traceTesting, true, "the queued egress check runs after the selection")
      } },
    { name: "egress after selection", until: function() { return !svc.traceTesting }, then: function() {
        t.check(svc.egressIp, "203.0.113.7", "the queued egress check lands")
        t.check(svc.traceFailed, false, "the queued egress check succeeds")
        svc.call("test.status", { primary_group: "Other" })
      } },
    { name: "another client changes primary group", until: function() { return svc.configuredPrimaryGroup === "Other" }, then: function() {
        t.check(svc.egressIp, "", "a different primary group invalidates the old egress")
        t.check(svc.traceTesting, true, "a different primary group starts an egress check")
      } },
    { name: "egress after primary group", until: function() { return !svc.traceTesting }, then: function() {
        t.check(svc.egressIp, "203.0.113.7", "the new primary group's check lands")
        svc.setPrimaryGroup("Proxy")
      } },
    { name: "widget changes primary group", until: function() { return !svc.busy }, then: function() {
        t.check(svc.egressIp, "", "the widget's primary group change invalidates egress")
        t.check(svc.traceTesting, true, "the widget's primary group change checks egress")
      } },
    { name: "traffic and connections", until: function() {
        return svc.trafficHistory.length >= 2 && svc.connectionLog.length === 1
      }, then: function() {
        t.check(svc.downloadRate, 2, "traffic pushes set the rates")
        t.check(svc.connectionLog[0].process, "firefox", "connections carry their process")
        t.check(svc.connectionsDownload, 20, "connections carry the totals")
        t.check(svc.openConnectionCount, 1, "the log counts open connections")
        svc.addSubscription("https://example.test/sub")
      } },
    { name: "stream errors invalidate snapshots", until: function() { return !svc.busy && svc.subscriptions.length === 2 }, then: function() {
        var runtime = svc.runtimeKey
        svc.receive(JSON.stringify({ type: "event", stream: "groups",
          error: { code: "too_large", message: "groups event too large" } }))
        t.check(svc.proxiesLoaded, false, "a groups error invalidates the loaded snapshot")
        t.check(svc.currentConfig, "", "a groups error removes the old selection")
        t.check(svc.groups.length, 0, "a groups error removes stale entries")
        t.check(svc.lastError, "groups event too large", "stream failures are visible")
        t.check(svc.errorScope, "configs", "groups failures appear with configs")
        svc.selectConfig("Proxy", "Pass")
        svc.testConfig("Pass")
        svc.setPrimaryGroup("Proxy")
        t.check(svc.busy, false, "stale groups cannot be selected or made primary")
        t.check(svc.pingTestsRunning, false, "stale configs cannot be tested")
        svc.applyEvent("traffic", { up: 1, down: 2 })
        svc.clearMessages()
        t.check(svc.lastError, "groups event too large", "opening another form cannot hide a stream failure")
        svc.reportDone("unrelated action succeeded")
        t.check(svc.lastError, "groups event too large", "unrelated success cannot hide a stream failure")
        svc.refresh()
        t.check(svc.runtimeKey, runtime, "refresh recovery does not need a new runtime")
      } },
    { name: "refresh recovers an existing subscription", until: function() {
        return svc.proxiesLoaded && svc.currentConfig !== ""
      }, then: function() {
        t.check(svc.lastError, "", "a valid groups snapshot clears its failure")
        svc.applyEvent("rules", { rules: [{ index: 1, type: "DOMAIN", value: "example.test", target: "DIRECT", source: "user" }] })
        t.check(svc.rules.length, 1, "an editable rule is loaded before the failure")
        var cases = [
          { stream: "rules", scope: "rules" },
          { stream: "subscriptions", scope: "sub:" },
          { stream: "traffic", scope: "core" },
          { stream: "connections", scope: "conns" },
          { stream: "status", scope: "core" }
        ]
        for (var i = 0; i < cases.length; i++) {
          var entry = cases[i]
          svc.receive(JSON.stringify({ type: "event", stream: entry.stream,
            error: { code: "too_large", message: entry.stream + " too large" } }))
          t.check(svc.lastError, entry.stream + " too large", entry.stream + " failure is visible")
          t.check(svc.errorScope, entry.scope, entry.stream + " failure has a scope")
          if (entry.stream === "rules") svc.removeRule(1)
          if (entry.stream === "subscriptions") svc.activateSubscription("work")
          if (entry.stream === "connections") svc.closeAllConnections()
          t.check(svc.busy, false, entry.stream + " cannot act on its invalid snapshot")
        }
        t.check(svc.rules.length, 0, "rules errors remove editable stale indices")
        t.check(svc.effectiveRules.length, 0, "rules errors remove stale rule evaluation")
        t.check(svc.subscriptions.length, 0, "subscription errors remove stale entries")
        t.check(svc.downloadRate, 0, "traffic errors remove stale rates")
        t.check(svc.trafficHistory.length, 0, "traffic errors remove stale history")
        t.check(svc.connectionLog.length, 0, "connection errors remove stale close targets")
        t.check(svc.connectionsDownload, 0, "connection errors remove stale totals")
        t.check(svc.apiReady, false, "status errors make live controls unavailable")
        svc.removeRule(1)
        svc.activateSubscription("work")
        svc.closeAllConnections()
        svc.toggleCore()
        svc.toggleTun()
        t.check(svc.busy, false, "actions cannot use invalid stream values")
        svc.refresh()
      } },
    { name: "all snapshots recover", until: function() {
        return svc.apiReady && svc.subscriptions.length === 2 && svc.effectiveRules.length > 0
          && svc.connectionLog.length > 0 && svc.trafficHistory.length > 0
      }, then: function() {
        t.check(svc.lastError, "", "recovered streams clear their errors")
        svc.call("test.hold-edit", {}, function() {
          svc.addRule("DOMAIN-SUFFIX", "example.test", "DIRECT")
        })
      } },
    { name: "invalidate rules during an edit", until: function() {
        return svc.ruleSaving && svc.rules.length === 1
      }, then: function() {
        svc.receive(JSON.stringify({ type: "event", stream: "rules",
          error: { code: "too_large", message: "rules event too large" } }))
        svc.call("test.release-edit", {})
      } },
    { name: "an edit reply cannot restore invalid rules", until: function() {
        return !svc.ruleSaving
      }, then: function() {
        t.check(svc.rules.length, 0, "an earlier edit reply cannot repopulate invalid rules")
        t.check(svc.lastError, "rules event too large", "an edit reply cannot clear a stream error")
        svc.toggleTun()
        t.check(svc.busy, true, "a rules failure does not block a TUN setting")
        svc.refresh()
      } },
    { name: "settings recover independently of rules", until: function() {
        return !svc.busy && svc.lastError === ""
      }, then: function() {
        t.check(svc.tunEnabled, true, "the unrelated TUN setting reaches the daemon")
      } },
    { name: "add", until: function() { return svc.subscriptions.length === 2 && !svc.busy }, then: function() {
        t.check(svc.subscriptions[1].name, "added-1", "the subscription names itself")
        svc.activateSubscription("added-1")
        t.check(svc.pendingSubscription, "added-1", "an activation is pending until its reply")
      } },
    { name: "switch", until: function() { return !svc.busy }, then: function() {
        t.check(svc.activeSubscription, "added-1", "the activation's reply switches the subscription")
        t.check(svc.traceTesting, true, "switching subscriptions checks the egress again")
        t.check(svc.configTestState("Pass"), "", "switching subscriptions drops old latency results")
        svc.panelOpen = false
        t.check(svc.downloadRate, 0, "closing the panel zeroes the rates")
      } },
    { name: "closed panel", until: function() { return !svc.traceTesting }, then: function() {
        svc.toggleCore()
      } },
    { name: "stop", until: function() { return !svc.busy }, then: function() {
        t.check(svc.coreRunning, false, "the stop's reply confirms it")
        t.check(svc.groups.length, 0, "a stopped proxy has no groups")
        t.check(svc.connectionLog.length, 0, "a stopped proxy has no connection log")
        t.check(svc.trafficHistory.length, 0, "a stopped proxy has no traffic history")
      } }
  ]
}
