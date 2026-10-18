import QtQuick
import Quickshell.Io

Item {
  id: root

  property string commandPath: decodeURIComponent(String(Qt.resolvedUrl("../bin/omihomo-install")).replace(/^file:\/\//, ""))
  readonly property string installedVersion: _result.installed || ""
  readonly property string availableVersion: _result.available || ""
  readonly property string pluginVersion: _result.plugin || ""
  readonly property bool updateAvailable: _result.update_available === true
  readonly property string releaseUrl: _result.release_url || ""
  readonly property bool checking: _checking
  readonly property string error: _error
  signal completed()

  property var _result: ({})
  property bool _checking: false
  property string _error: ""
  property string _failure: ""
  property double _lastAttempt: 0
  readonly property int _cacheMs: 12 * 60 * 60 * 1000

  function check() {
    if (_checking) return
    _lastAttempt = Date.now()
    _error = ""
    _failure = ""
    _checking = true
    deadline.restart()
    process.command = [commandPath, "check"]
    process.running = true
  }

  function checkIfDue() {
    if (enabled && (_lastAttempt === 0 || Date.now() - _lastAttempt >= _cacheMs)) check()
  }

  function finish(message) {
    if (!_checking) return
    deadline.stop()
    _error = message
    _checking = false
    completed()
  }

  function versionParts(version) {
    if (typeof version !== "string" || !/^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(-[1-9][0-9]{0,8})?$/.test(version)) return null
    return version.split(/[.-]/).map(Number)
  }

  function newer(available, installed) {
    for (var i = 0; i < available.length; i++) {
      if (available[i] !== installed[i]) return available[i] > installed[i]
    }
    return false
  }

  function receive(text, exitCode, exitStatus) {
    if (_failure !== "") return finish(_failure)
    var result
    try { result = JSON.parse(text) } catch (_) { return finish("Invalid update check response") }
    if (!result || typeof result !== "object" || Array.isArray(result)) return finish("Invalid update check response")
    if (Object.prototype.hasOwnProperty.call(result, "error"))
      return finish(typeof result.error === "string" && result.error.length > 0 && result.error.length <= 512
        ? result.error : "Update check failed")
    if (exitCode !== 0 || exitStatus !== 0) return finish("Update check failed")
    var available = versionParts(result.available)
    var installed = result.installed === "" ? [] : versionParts(result.installed)
    var plugin = versionParts(result.plugin)
    if (!available || available.length !== 4 || !installed || (installed.length !== 0 && installed.length !== 4)
        || !plugin || plugin.length !== 3 || typeof result.update_available !== "boolean"
        || result.release_url !== "https://github.com/readeem/omihomo/releases/tag/v" + result.available.split("-")[0])
      return finish("Invalid update check response")
    var stale = installed.length > 0 && (newer(available, installed) || newer(available.slice(0, 3), plugin))
    if (result.update_available !== stale) return finish("Invalid update check response")
    _result = { installed: result.installed, available: result.available, plugin: result.plugin,
      update_available: stale, release_url: result.release_url }
    finish("")
  }

  onEnabledChanged: Qt.callLater(checkIfDue)
  Component.onCompleted: Qt.callLater(checkIfDue)

  Timer {
    interval: root._cacheMs
    repeat: true
    running: root.enabled
    onTriggered: root.checkIfDue()
  }

  Timer {
    id: deadline
    interval: 30000
    onTriggered: {
      root._failure = "Update check timed out"
      if (process.running) process.signal(9)
      else root.finish(root._failure)
    }
  }

  Process {
    id: process
    stdout: StdioCollector {
      id: output
      onTextChanged: {
        if (text.length <= 8192) return
        root._failure = "Update check response is too large"
        process.signal(9)
      }
    }
    onExited: function(exitCode, exitStatus) { root.receive(output.text, exitCode, exitStatus) }
  }
}
