import QtQuick
import Quickshell
import "." as Omihomo

// Exercise latency checks through a real asynchronous Process with a fake
// controller. This catches state that disappears before the request exits.
ShellRoot {
  id: testRoot

  property bool passed: true
  property int stage: 0

  Omihomo.Service {
    id: service
    cliPath: "/usr/bin/true"
    apiAddress: "controller.test:9090"
  }

  Timer {
    interval: 10
    repeat: true
    running: true
    onTriggered: testRoot.advance()
  }

  function status() {
    return JSON.stringify({
      state: "on",
      detail: "",
      active_subscription: "Test",
      primary_group: "Proxy",
      tun_enabled: false,
      autostart_enabled: false,
      uptime: null
    })
  }

  function check(actual, expected, message) {
    if (actual === expected) return
    passed = false
    console.error("PING_STATE_FAIL " + message + ": got " + actual + ", expected " + expected)
  }

  function finish() {
    console.log(passed ? "PING_STATE_PASS" : "PING_STATE_FAILED")
    Qt.quit()
  }

  function advance() {
    if (stage === 0) {
      service.applyStatus(status())
      service.groups = [{ name: "Proxy", type: "Selector", now: "Pass",
        all: ["Pass", "Fail"], selectable: true, system: false }]
      service.configEntries = ({
        Pass: { name: "Pass", history: [{ delay: 40 }], alive: true },
        Fail: { name: "Fail", history: [{ delay: 50 }], alive: true }
      })
      service.testConfig("Fail")
      check(service.configTestState("Fail"), "testing", "single test starts immediately")
      stage = 1
      return
    }
    if (stage === 1) {
      if (service.pingTestsRunning) {
        check(service.configTestState("Fail"), "testing", "single test stays visible while curl runs")
        return
      }
      check(service.configTestState("Fail"), "failed", "single failure persists")
      service.configEntries = ({
        Pass: { name: "Pass", history: [{ delay: 40 }], alive: true },
        Fail: { name: "Fail", history: [{ delay: 50 }], alive: true }
      })
      check(service.configTestState("Fail"), "failed", "refresh cannot erase failure")
      service.testConfig("Pass")
      check(service.configTestState("Pass"), "testing", "next single test replaces old state")
      stage = 2
      return
    }
    if (stage === 2) {
      if (service.pingTestsRunning) {
        check(service.configTestState("Pass"), "testing", "successful test stays visible while curl runs")
        return
      }
      check(service.configTestState("Pass"), "success", "single success is recorded")
      check(service.configDelay("Pass"), 87, "single success records its delay")
      service.testGroup("Proxy")
      check(service.configTestState("Pass"), "testing", "bulk test marks first config immediately")
      check(service.configTestState("Fail"), "testing", "bulk test marks every config immediately")
      stage = 3
      return
    }
    if (stage === 3) {
      if (service.pingTestsRunning) {
        check(service.configTestState("Pass"), "testing", "bulk state stays visible while curl runs")
        check(service.configTestState("Fail"), "testing", "bulk failure stays pending until completion")
        return
      }
      check(service.configTestState("Pass"), "success", "bulk success is recorded")
      check(service.configDelay("Pass"), 91, "bulk success records its delay")
      check(service.configTestState("Fail"), "failed", "missing bulk result is an explicit failure")
      service.refreshTrace()
      check(service.traceTesting, true, "egress test starts immediately")
      stage = 4
      return
    }
    if (stage === 4) {
      if (service.traceTesting) return
      check(service.traceFailed, true, "egress failure is recorded")
      finish()
    }
  }
}
