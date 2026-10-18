import QtQuick
import Quickshell
import "." as Omihomo

ShellRoot {
  id: suite

  property int step: 0
  property bool passed: true
  property bool automaticCompleted: false
  readonly property string fixtures: Quickshell.env("OMIHOMO_UPDATE_FIXTURES")
  readonly property var cases: [
    { command: "good command", installed: "1.0.0-1", available: "1.0.1-1", update: true },
    { command: "error", error: "Network unavailable", retain: true },
    { command: "malformed", error: "Invalid update check response", retain: true },
    { command: "array", error: "Invalid update check response", retain: true },
    { command: "multiple", error: "Invalid update check response", retain: true },
    { command: "url", error: "Invalid update check response", retain: true },
    { command: "type", error: "Invalid update check response", retain: true },
    { command: "downgrade", error: "Invalid update check response", retain: true },
    { command: "stale", error: "Invalid update check response", retain: true },
    { command: "plugin", error: "Invalid update check response", retain: true },
    { command: "nonzero", error: "Update check failed", retain: true },
    { command: "oversized", error: "Update check response is too large", retain: true },
    { command: "revision", installed: "1.0.1-1", available: "1.0.1-2", update: true },
    { command: "widget", installed: "1.0.1-1", available: "1.0.1-1", update: true },
    { command: "equal", installed: "1.0.1-2", available: "1.0.1-2", update: false },
    { command: "absent", installed: "", available: "1.0.1-1", update: false },
    { command: "hang", error: "Update check timed out" }
  ]

  function equal(actual, expected, message) {
    if (actual === expected) return
    passed = false
    console.error("UPDATE_STATE_FAIL " + message + ": got " + actual + ", expected " + expected)
  }

  function next() {
    if (step === cases.length) {
      equal(automaticCompleted, true, "enabled service checks automatically")
      console.log(passed ? "UPDATE_STATE_PASS" : "UPDATE_STATE_FAILED")
      Qt.quit()
      return
    }
    updater.commandPath = fixtures + "/" + cases[step].command
    updater.check()
    equal(updater.checking, true, "check starts")
    updater.check()
  }

  Omihomo.UpdateService {
    id: updater
    enabled: false
    onCompleted: {
      var current = suite.cases[suite.step]
      suite.equal(checking, false, "completed means no check in flight")
      suite.equal(error, current.error || "", current.command + " error")
      if (current.installed !== undefined) {
        suite.equal(installedVersion, current.installed, current.command + " installed")
        suite.equal(availableVersion, current.available, current.command + " available")
        suite.equal(updateAvailable, current.update, current.command + " newer")
        suite.equal(releaseUrl, "https://github.com/readeem/omihomo/releases/tag/v1.0.1", "validated notes")
      }
      if (current.retain) {
        suite.equal(installedVersion, "1.0.0-1", "failure retains installed")
        suite.equal(availableVersion, "1.0.1-1", "failure retains latest")
        suite.equal(updateAvailable, true, "failure retains update")
        suite.equal(releaseUrl, "https://github.com/readeem/omihomo/releases/tag/v1.0.1", "failure retains safe notes")
      }
      // Re-enabling a cached service must not start another process.
      enabled = true
      Qt.callLater(function() {
        suite.equal(updater.checking, false, "cached enable does not check")
        updater.enabled = false
        suite.step++
        suite.next()
      })
    }
  }

  Omihomo.UpdateService {
    commandPath: suite.fixtures + "/equal"
    onCompleted: {
      suite.equal(error, "", "automatic check succeeds")
      suite.equal(installedVersion, "1.0.1-2", "automatic check publishes result")
      suite.automaticCompleted = true
    }
  }

  Component.onCompleted: Qt.callLater(next)
}
