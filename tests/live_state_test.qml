import QtQuick
import Quickshell
import "." as Omihomo

// Drive an open panel's service through fixtures/fake-cli and fixtures/fake-curl,
// where `/proxies` answers slowly and `/traffic` ends on its own.
ShellRoot {
  id: testRoot

  property bool passed: true
  property int stage: 0
  property int trafficReads: 0

  Omihomo.Service {
    id: service
    cliPath: "omihomo-fake-cli"
    panelOpen: true
    onDownloadRateChanged: if (downloadRate === 2) testRoot.trafficReads += 1
  }

  Timer {
    interval: 10
    repeat: true
    running: true
    onTriggered: testRoot.advance()
  }

  Timer {
    interval: 6000
    running: true
    onTriggered: {
      testRoot.check(testRoot.stage, 3, "every stage finishes in time")
      testRoot.finish()
    }
  }

  function check(actual, expected, message) {
    if (actual === expected) return
    passed = false
    console.error("LIVE_STATE_FAIL " + message + ": got " + actual + ", expected " + expected)
  }

  function finish() {
    console.log(passed ? "LIVE_STATE_PASS" : "LIVE_STATE_FAILED")
    Qt.quit()
  }

  function advance() {
    if (stage === 0) {
      if (!service.proxiesLoaded || service.busy) return
      check(service.currentConfig, "Pass", "the first read shows the core's selection")
      // This read starts before the selection and answers after it.
      service.refreshLive()
      service.selectConfig("Proxy", "Fail")
      stage = 1
      return
    }
    if (stage === 1) {
      if (service.pendingConfig !== "") return
      check(service.currentConfig, "Fail", "a read from before the selection cannot undo it")
      stage = 2
      return
    }
    if (stage === 2) {
      if (trafficReads < 2) return
      stage = 3
      finish()
    }
  }
}
