import QtQuick
import Quickshell

// Runs a suite's steps in order against the fake daemons that
// daemon_state_test.sh started. A step may wait `until` its condition holds,
// then runs `then`; a step still waiting after five seconds fails the suite.
Item {
  id: runner

  property string name: ""
  property var steps: []
  property bool passed: true
  property int step: 0
  property int ticks: 0
  property bool done: false
  readonly property string sockets: Quickshell.env("OMIHOMO_TEST_SOCKETS") || ""

  function socket(name) {
    return sockets + "/" + name + ".sock"
  }

  function check(actual, expected, message) {
    if (actual === expected) return
    passed = false
    console.error(name + "_STATE_FAIL " + message + ": got " + actual + ", expected " + expected)
  }

  function finish() {
    if (done) return
    done = true
    console.log(passed ? name + "_STATE_PASS" : name + "_STATE_FAILED")
    Qt.quit()
  }

  function advance() {
    if (done) return
    var current = steps[step]
    if (!current) return finish()
    if (current.until && !current.until()) {
      if (++ticks < 500) return
      check(false, true, "step " + step + " (" + current.name + ") timed out")
      return finish()
    }
    ticks = 0
    step++
    if (current.then) current.then()
  }

  Timer {
    interval: 10
    repeat: true
    running: runner.steps.length > 0
    onTriggered: runner.advance()
  }
}
