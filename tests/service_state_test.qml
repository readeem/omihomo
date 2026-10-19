import QtQuick
import Quickshell
import "../widget" as Omihomo

// The connection, optimistic writes, and failures, against fake daemons.
// Quickshell links its QML modules into the `qs` executable, so each suite runs
// as a tiny shell config rather than through qmltestrunner.
ShellRoot {
  id: testRoot

  property int savedRules: 0
  property int failedRules: 0

  StateTest { id: t; name: "SERVICE" }

  Omihomo.Service { id: absent; socketPath: t.socket("none") }
  Omihomo.Service { id: refused; socketPath: t.socket("refuse") }
  Omihomo.Service {
    id: capacity
    socketPath: t.socket("busy")
    property var refusal: null
    onLastErrorCodeChanged: if (lastErrorCode === "busy")
      refusal = { link: link, code: lastErrorCode, message: lastError }
  }
  Omihomo.Service { id: outdated; socketPath: t.socket("old") }
  Omihomo.Service { id: newer; socketPath: t.socket("new") }
  Omihomo.Service {
    id: svc
    socketPath: t.socket("ok")
    onRuleAdded: testRoot.savedRules++
  }
  Omihomo.Service {
    id: failing
    socketPath: t.socket("fail")
    onRuleAdded: testRoot.failedRules++
  }

  property double reconnectedAt: 0

  Component.onCompleted: t.steps = [
    { name: "every daemon answers or not", until: function() {
        return absent.link === "absent" && refused.link === "refused" && capacity.refusal !== null && outdated.link === "outdated"
          && newer.link === "newer" && svc.ready && failing.ready && svc.coreState === "stopped"
      }, then: function() {
        t.check(absent.coreState, "not-installed", "no daemon reads as not installed")
        t.check(absent.installed, false, "no daemon is not installed")
        t.check(refused.installed, false, "a refusal leaves nothing to show")
        t.check(refused.lastErrorCode, "forbidden", "ownership refusal keeps its code")
        t.check(capacity.refusal.link, "busy", "capacity refusal never offers ownership takeover")
        t.check(capacity.refusal.code, "busy", "capacity refusal keeps its code")
        t.check(capacity.refusal.message, "too many clients", "capacity refusal shows the daemon's message")
        t.check(outdated.installed, false, "an older protocol is never spoken")
        t.check(newer.installed, false, "a newer protocol is never spoken")
        t.check(outdated.call("status"), 0, "no request goes to a daemon of another protocol")
        t.check(svc.installed, true, "a greeted daemon is installed")
        t.check(svc.daemonVersion, "v0.0.0-fake", "the greeting carries the daemon's version")
        t.check(svc.subscriptions.length, 0, "a closed panel does not subscribe to subscriptions")

        svc.toggleCore()
        t.check(svc.coreActive, true, "the switch moves on the click")
        t.check(svc.coreRunning, false, "the proxy is not on until the daemon says so")
        // A push that left the daemon before the start cannot move it back.
        svc.applyStatus({ state: "stopped", active_subscription: "work", mode: "rule" })
        t.check(svc.coreActive, true, "a stale status cannot undo a pending start")
        svc.toggleCore()
        t.check(svc._desiredCoreRunning, 1, "a second click waits for the first")
      } },
    { name: "capacity becomes available", until: function() { return capacity.ready }, then: function() {
        t.check(capacity.lastError, "", "a successful greeting clears the capacity refusal")
      } },
    { name: "start lands", until: function() { return !svc.busy }, then: function() {
        t.check(svc.coreRunning, true, "the start's reply confirms it")
        t.check(svc._desiredCoreRunning, -1, "the reply clears the desired state")
        t.check(svc.startedMs > 0, true, "a running proxy has a start time")

        svc.toggleTun()
        t.check(svc.tunActive, true, "TUN moves on the click")
        svc.applyStatus({ state: "on", active_subscription: "work", mode: "rule", tun: false,
          started_at: svc.startedMs })
        t.check(svc.tunActive, true, "a stale status cannot undo pending TUN")
      } },
    { name: "TUN lands", until: function() { return !svc.busy }, then: function() {
        t.check(svc.tunEnabled, true, "TUN's reply confirms it")
        t.check(svc._desiredTunEnabled, -1, "TUN's reply clears the desired state")
        t.check(svc.routing.tunEnabled, true, "the rule tester sees TUN routing")
        svc.setMode("global")
        t.check(svc.effectiveMode, "global", "the mode moves on the click")
      } },
    { name: "mode lands", until: function() { return !svc.busy }, then: function() {
        t.check(svc.mode, "global", "the mode's reply confirms it")
        t.check(svc._desiredMode, "", "the mode's reply clears the desired state")
        t.check(svc.addRule("DOMAIN-SUFFIX", "steamcontent.com", "DIRECT"), true, "a rule write starts")
        t.check(svc.addRule("DOMAIN-SUFFIX", "steamcontent.com", "DIRECT"), false,
          "a second rule write waits for the first")
        t.check(testRoot.savedRules, 0, "the form waits for the write's reply")
      } },
    { name: "rule lands", until: function() { return testRoot.savedRules === 1 }, then: function() {
        t.check(svc.rules.length, 0, "a closed panel does not load rules from command replies")
        svc.panelOpen = true
      } },
    { name: "opening the panel subscribes", until: function() {
        return svc.subscriptions.length === 1 && svc.proxiesLoaded && svc.trafficHistory.length > 0
      }, then: function() {
        t.check(svc.rules.length, 1, "opening the panel loads the saved rule")
        t.check(svc.rules[0].index, 1, "the owner's rule carries its index")
        t.check(svc.injectedRules.length, 1, "Omihomo's rules are read-only")
        t.check(svc.currentConfig, "Pass", "groups arrive with the panel")
        t.check(svc.subscriptions[0].active, true, "subscriptions arrive with the panel")
        // A daemon restarting under socket activation drops the connection.
        svc.call("test.drop", {})
      } },
    { name: "the connection drops", until: function() { return svc.link === "connecting" }, then: function() {
        t.check(svc.installed, true, "a dropped connection keeps the panel")
        t.check(svc.coreState, "on", "a dropped connection keeps the last status")
        t.check(svc.groups.length, 1, "a dropped connection keeps the groups")
        t.check(svc.busy, false, "a dropped connection fails what was in flight")
        t.check(svc.call("status"), 0, "nothing is sent without a connection")
      } },
    { name: "it comes back", until: function() { return svc.ready && svc.trafficHistory.length > 0 }, then: function() {
        t.check(svc.coreState, "on", "the status is pushed again")
        t.check(svc.groups.length, 1, "the groups are pushed again")
        svc.configTestResults = ({ Pass: { state: "success", delay: 40 } })
        // Another run of the core makes everything read from the last one stale.
        svc.call("test.status", { started_at: svc.startedMs + 1 })
      } },
    { name: "a new runtime", until: function() { return svc.configTestState("Pass") === "" }, then: function() {
        t.check(svc.configTestState("Pass"), "", "a restarted core drops old latency results")
        failing.toggleCore()
        t.check(failing.coreActive, true, "a failing start still moves on the click")
      } },
    { name: "a failed start", until: function() { return !failing.busy }, then: function() {
        t.check(failing.coreActive, false, "a failed start rolls the switch back")
        t.check(failing._desiredCoreRunning, -1, "a failed start clears the desired state")
        t.check(failing.lastError, "start failed", "a failed start says why")
        t.check(failing.errorScope, "core", "a failed start's error sits with the switch")
        failing.toggleTun()
        t.check(failing.tunActive, true, "a failing TUN change still moves on the click")
      } },
    { name: "a failed setting", until: function() { return !failing.busy }, then: function() {
        t.check(failing.tunActive, false, "a failed TUN change rolls back")
        t.check(failing.errorScope, "settings", "a failed setting's error sits in settings")
        t.check(failing.addRule("DOMAIN-SUFFIX", "steamcontent.com", "DIRECT"), true,
          "a failing rule write still starts")
      } },
    { name: "a failed rule", until: function() { return !failing.busy }, then: function() {
        t.check(testRoot.failedRules, 0, "a failed rule write does not clear the form")
        t.check(failing.errorScope, "rules", "a failed rule's error sits under the form")
        failing.addSubscription("https://example.test/sub")
        t.check(failing.actionStatus, "Fetching subscription…", "an add says it is fetching")
      } },
    { name: "a failed add", until: function() { return !failing.busy }, then: function() {
        t.check(failing.errorScope, "sub:", "a failed add's error sits under the add field")
        t.check(failing.pendingSubscription, "", "a failed add is no longer pending")
      } }
  ]
}
