import QtQuick
import Quickshell
import "." as Omihomo

// Quickshell links its QML modules into the `qs` executable, so this focused
// service test runs as a tiny shell config rather than through qmltestrunner.
ShellRoot {
  id: testRoot

  property bool passed: true

  Omihomo.Service {
    id: coreService
    // `true` preserves the asynchronous Process boundary without touching the
    // real Omihomo CLI, systemd unit, override file, or mihomo controller.
    cliPath: "/usr/bin/true"
  }

  Omihomo.Service { id: tunService; cliPath: "/usr/bin/true" }
  Omihomo.Service { id: autostartService; cliPath: "/usr/bin/true" }
  Omihomo.Service { id: modeService; cliPath: "/usr/bin/true" }
  // Opening the panel has to read the subscriptions itself. The panel's
  // `panelOpen` binding updates after its own onOpenedChanged handler, so a
  // read driven from there would miss the first open entirely.
  Omihomo.Service { id: openService; cliPath: "omihomo-fake-cli" }

  Omihomo.Service { id: failingCoreService; cliPath: "/usr/bin/false" }
  Omihomo.Service { id: failingTunService; cliPath: "/usr/bin/false" }

  Timer {
    id: openCheck
    interval: 10
    repeat: true
    property int ticks: 0
    onTriggered: {
      ticks += 1
      if (openService.subscriptions.length === 0 && ticks < 100) return
      stop()
      check(openService.subscriptions.length, 1, "opening the panel loads subscriptions")
      failureCheck.start()
    }
  }

  Timer {
    id: failureCheck
    interval: 10
    repeat: true
    onTriggered: {
      if (failingCoreService.busy || failingTunService.busy) return
      stop()
      check(failingCoreService.coreActive, false, "failed core command rolls back")
      check(failingCoreService._desiredCoreRunning, -1, "failed core command clears desired state")
      check(failingTunService.tunActive, false, "failed TUN command rolls back")
      check(failingTunService._desiredTunEnabled, -1, "failed TUN command clears desired state")
      finish()
    }
  }

  function status(state, tun, autostart) {
    return JSON.stringify({
      state: state,
      detail: "",
      active_subscription: "",
      primary_group: "",
      tun_enabled: tun === true,
      autostart_enabled: autostart === true,
      uptime: null
    })
  }

  function check(actual, expected, message) {
    if (actual === expected) return
    passed = false
    console.error("SERVICE_STATE_FAIL " + message + ": got " + actual + ", expected " + expected)
  }

  function finish() {
    console.log(passed ? "SERVICE_STATE_PASS" : "SERVICE_STATE_FAILED")
    Qt.quit()
  }

  function run() {
    coreService.applyStatus(status("stopped", false, false))
    check(coreService.coreRunning, false, "confirmed core starts off")

    coreService.toggleCore()
    check(coreService.coreActive, true, "core changes immediately after click")

    // A status command launched before the click may finish after it. The
    // confirmed value is still off, but the rendered value must stay on.
    coreService.applyStatus(status("stopped", false, false))
    check(coreService.coreActive, true, "stale status cannot undo pending toggle")

    coreService.applyStatus(status("on", false, false))
    check(coreService.coreActive, true, "confirmed core stays on")
    check(coreService._desiredCoreRunning, -1, "core confirmation clears desired state")

    tunService.applyStatus(status("on", false, false))
    tunService.toggleTun()
    check(tunService.tunActive, true, "TUN changes immediately after click")
    tunService.applyStatus(status("on", false, false))
    check(tunService.tunActive, true, "stale status cannot undo pending TUN")
    tunService.applyStatus(status("on", true, false))
    check(tunService._desiredTunEnabled, -1, "TUN confirmation clears desired state")

    autostartService.applyStatus(status("stopped", false, false))
    autostartService.toggleAutostart()
    check(autostartService.autostartActive, true, "autostart changes immediately after click")
    autostartService.applyStatus(status("stopped", false, false))
    check(autostartService.autostartActive, true, "stale status cannot undo pending autostart")
    autostartService.applyStatus(status("stopped", false, true))
    check(autostartService._desiredAutostartEnabled, -1, "autostart confirmation clears desired state")

    modeService.applyStatus(status("on", false, false))
    modeService.applyConfigs('{"mode":"rule","mixed-port":7890}')
    modeService.setMode("global")
    check(modeService.effectiveMode, "global", "mode changes immediately after click")
    modeService.applyConfigs('{"mode":"rule","mixed-port":7890}')
    check(modeService.effectiveMode, "global", "stale configs cannot undo pending mode")
    modeService.applyConfigs('{"mode":"global","mixed-port":7890}')
    check(modeService._desiredMode, "", "mode confirmation clears desired state")

    failingCoreService.applyStatus(status("stopped", false, false))
    failingCoreService.toggleCore()
    check(failingCoreService.coreActive, true, "failed core command starts optimistically")

    failingTunService.applyStatus(status("on", false, false))
    failingTunService.toggleTun()
    check(failingTunService.tunActive, true, "failed TUN command starts optimistically")

    check(openService.subscriptions.length, 0, "subscriptions start empty")
    openService.panelOpen = true
    openCheck.start()
  }

  Component.onCompleted: Qt.callLater(run)
}
