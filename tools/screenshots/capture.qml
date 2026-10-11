import QtQuick
import Quickshell
import qs.Commons
import qs.Commons as Commons
import "omihomo" as Omihomo

// Opens the real panel against the fake CLI and controller, saves each view,
// then composes the README preview from three of them over the wallpaper.
ShellRoot {
  id: capture

  readonly property string outDir: Quickshell.env("OMIHOMO_SHOTS_OUT")
  readonly property string wallpaper: Quickshell.env("OMIHOMO_SHOTS_WALLPAPER")
  readonly property var shots: [
    { view: "configs", file: "configs.png" },
    { view: "subs", file: "subscriptions.png" },
    { view: "rules", file: "rules.png", ruleQuery: "www.netflix.com" },
    { view: "conns", file: "connections.png" },
    { view: "settings", file: "settings.png" }
  ]
  readonly property var previewShots: ["configs.png", "connections.png", "rules.png"]

  property var service: null
  property string stage: "load"
  property int shot: 0
  property int settled: 0

  function find(item, test) {
    if (!item) return null
    if (test(item)) return item
    var kids = item.children || []
    for (var i = 0; i < kids.length; i++) {
      var hit = find(kids[i], test)
      if (hit) return hit
    }
    return null
  }

  function fail(message) {
    console.error("CAPTURE_FAILED " + message)
    Qt.exit(1)
  }

  function save(item, file, size) {
    stage = "saving"
    var done = function(result) {
      if (!result.saveToFile(outDir + "/" + file)) return fail("could not write " + file)
      console.log("CAPTURE_SAVED " + file)
      capture.shot += 1
      capture.settled = 0
      capture.stage = capture.shot < capture.shots.length ? "shoot" : "preview"
      if (file === "preview.png") {
        console.log("CAPTURE_DONE")
        Qt.quit()
      }
    }
    if (!(size ? item.grabToImage(done, size) : item.grabToImage(done))) fail("could not grab " + file)
  }

  function step() {
    if (stage === "load") {
      service = service || find(panel, function(item) { return item.groupTestedAt !== undefined })
      if (!service || !service.apiReady) return
      if (!panel.opened) panel.open()
      if (!service.proxiesLoaded || service.subscriptions.length === 0
        || service.subscriptionRules.length === 0 || service.trafficHistory.length < 60
        || service.egressIp === "" || service.busy) return
      service.testGroup("Proxy")
      stage = "test"
    } else if (stage === "test") {
      if (service.pingTestsRunning || !service.groupTestedAt["Proxy"]) return
      panel.view = "conns"
      stage = "log"
    } else if (stage === "log") {
      if (service.connectionLog.length - service.openConnectionCount < 3) return
      stage = "shoot"
    } else if (stage === "shoot") {
      var next = shots[shot]
      if (settled++ === 0) {
        panel.view = next.view
        if (next.ruleQuery) find(panel, function(item) {
          return item.placeholderText === "domain, URL, or IP address"
        }).text = next.ruleQuery
      } else if (settled > 8) {
        save(panelFrame, next.file)
      }
    } else if (stage === "preview") {
      desktop.visible = true
      if (settled++ < 8 || !desktop.ready) return
      save(desktopFrame, "preview.png", Qt.size(1200, 675))
    }
  }

  Timer {
    interval: 100
    repeat: true
    running: true
    onTriggered: capture.step()
  }

  Timer {
    interval: 45000
    running: true
    onTriggered: capture.fail("timed out at stage " + capture.stage + ": " + (capture.service ? JSON.stringify({
      state: capture.service.coreState, api: capture.service.apiAddress, proxies: capture.service.proxiesLoaded,
      subs: capture.service.subscriptions.length, rules: capture.service.subscriptionRules.length,
      traffic: capture.service.trafficHistory.length, egress: capture.service.egressIp,
      log: capture.service.connectionLog.length, error: capture.service.lastError
    }) : "no service"))
  }

  FloatingWindow {
    id: panelWindow
    implicitWidth: 422
    implicitHeight: 722
    color: Commons.Color.background

    Item {
      id: panelFrame
      anchors.fill: parent

      Image {
        anchors.fill: parent
        source: capture.wallpaper
        fillMode: Image.PreserveAspectCrop
      }

      Omihomo.Panel {
        id: panel
        x: 1
        y: 1
        width: 420
        height: 720
      }
    }
  }

  FloatingWindow {
    id: desktop
    readonly property bool ready: background.status === Image.Ready
      && previewImages.count === capture.previewShots.length
      && [0, 1, 2].every(function(i) { return previewImages.itemAt(i).status === Image.Ready })

    visible: false
    implicitWidth: 1600
    implicitHeight: 900
    color: Commons.Color.background

    Item {
      id: desktopFrame
      anchors.fill: parent

      Image {
        id: background
        anchors.fill: parent
        source: capture.wallpaper
        fillMode: Image.PreserveAspectCrop
        smooth: true
        mipmap: true
      }

      Rectangle {
        width: parent.width
        height: 26
        color: Commons.Color.bar.background

        Row {
          x: 12
          anchors.verticalCenter: parent.verticalCenter
          spacing: 14

          Repeater {
            model: 5
            Text {
              text: index + 1
              color: index === 1 ? Commons.Color.accent : Commons.Color.foreground
              font.family: Style.font.family
              font.pixelSize: Style.font.body
            }
          }
        }

        Text {
          anchors.centerIn: parent
          text: "Sunday 11:49"
          color: Commons.Color.foreground
          font.family: Style.font.family
          font.pixelSize: Style.font.body
        }
      }

      Row {
        y: 53
        anchors.horizontalCenter: parent.horizontalCenter
        spacing: 88

        Repeater {
          id: previewImages
          model: desktop.visible ? capture.previewShots : []
          Image {
            width: 422
            height: 722
            source: "file://" + capture.outDir + "/" + modelData
            cache: false
            smooth: true
            mipmap: true
          }
        }
      }
    }
  }
}
