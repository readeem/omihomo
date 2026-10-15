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
