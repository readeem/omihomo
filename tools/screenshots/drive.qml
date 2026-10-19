import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Commons as Commons
import qs.Ui
import "omihomo/widget" as Omihomo

// Holds the real panel open against the fake daemon for tools/screenshots/drive,
// which reaches it through `qs ipc call drive ...`. Keys and clicks go through
// Qt's input path, as a user's would. The panel closes itself after handing a
// command to the terminal, so it is reopened.
ShellRoot {
  id: drive

  property int shots: 0
  // The stand-in KeyboardPanel, the one child of the panel with a content height.
  property Item card: null
  property string shotPath: ""
  readonly property var modifiers: ({
    shift: Qt.ShiftModifier, ctrl: Qt.ControlModifier, alt: Qt.AltModifier, super: Qt.MetaModifier
  })

  Timer {
    interval: 100
    repeat: true
    running: true
    onTriggered: {
      var service = panel.service
      var absent = service.link === "absent"
      if (!absent && !service.apiReady) return
      if (!panel.opened) panel.open()
      if (!absent && (!service.proxiesLoaded || service.subscriptions.length === 0
        || service.trafficHistory.length < 60 || service.egressIp === "")) return
      running = false
      console.log("DRIVE_READY")
    }
  }

  // Stands in for the bar. Of what the panel hands Omarchy's floating terminal,
  // only the fake installer's install and update run, and never through a
  // shell: anything else, uninstall's sudo included, is only logged.
  PluginBarApi {
    id: bar
    pluginId: "dev.readeem.omihomo"
    moduleName: "dev.readeem.omihomo"
    foreground: Commons.Color.foreground
    barForeground: Commons.Color.foreground
    urgent: Commons.Color.urgent
    fontFamily: Style.font.family
    _run: function(command) {
      var actions = ["install", "update"]
      for (let i = 0; i < actions.length; i++) {
        if (command !== "omarchy-launch-floating-terminal-with-presentation "
          + Util.shellQuote(Util.shellQuote(panel.installerPath) + " " + actions[i])) continue
        console.log("DRIVE_TERMINAL ran omihomo-install " + actions[i])
        Quickshell.execDetached([panel.installerPath, actions[i]])
        return
      }
      console.log("DRIVE_TERMINAL would run: " + command)
    }
  }

  // Lets bindings and the panel's short transitions finish before a grab.
  Timer {
    id: settle
    interval: 400
    onTriggered: {
      var n = drive.shots
      var path = drive.shotPath
      var grabbed = frame.grabToImage(function(result) {
        console.log(result.saveToFile(path) ? "DRIVE_SAVED " + n : "DRIVE_FAILED " + n + " could not write " + path)
      })
      if (!grabbed) console.log("DRIVE_FAILED " + n + " could not grab the panel")
    }
  }

  IpcHandler {
    target: "drive"

    // Presses each space-separated key: a character ("j", "M", "/"), a Qt key
    // name ("Return", "Escape", "Down", "Backspace"), either behind
    // modifiers ("shift+Tab", "ctrl+k"). Returns what it could not press.
    function keys(specs: string): string {
      var specList = specs.split(" ").filter(function(spec) { return spec !== "" })
      for (let i = 0; i < specList.length; i++) {
        const parts = specList[i].split("+")
        const name = parts.pop()
        let held = 0
        for (let m = 0; m < parts.length; m++) {
          if (drive.modifiers[parts[m]] === undefined) return "unknown modifier " + parts[m]
          held |= drive.modifiers[parts[m]]
        }
        if (name.length === 1) events.keyClickChar(name, held, -1)
        else if (Qt["Key_" + name] !== undefined) events.keyClick(Qt["Key_" + name], held, -1)
        else return "unknown key " + name
      }
      return ""
    }

    function type(text: string): void {
      for (let i = 0; i < text.length; i++) events.keyClickChar(text[i], 0, -1)
    }

    // Takes a point in screenshot pixels.
    function click(x: real, y: real): void {
      var scale = frame.Window.window ? frame.Window.window.devicePixelRatio : 1
      events.mouseClick(frame, x / scale, y / scale, Qt.LeftButton, 0, -1)
    }

    // Saves the panel to an absolute path once it settles, then logs
    // DRIVE_SAVED or DRIVE_FAILED with the returned number.
    function shot(path: string): int {
      drive.shotPath = path
      drive.shots++
      settle.restart()
      return drive.shots
    }

    function state(): string {
      var service = panel.service
      return JSON.stringify({
        view: panel.view, cursor: panel.cursorActive ? panel.cursorRow : null,
        editing: panel.editing ? panel.editing.placeholderText || true : null,
        filter: panel.filterOpen, picking: panel.targetPicking,
        armed: panel.removeArmed || (panel.uninstallArmed ? "uninstall" : ""),
        link: service.link, core: service.coreState, mode: service.effectiveMode, tun: service.tunActive,
        group: panel.browsedGroup, config: service.currentConfig, subscription: service.activeSubscription,
        rules: service.rules.length, busy: service.busy, status: service.actionStatus, error: service.lastError
      })
    }
  }

  FloatingWindow {
    title: "Omihomo panel"
    implicitWidth: 422
    implicitHeight: 722
    // A fixed size makes Hyprland float the window under --visible.
    minimumSize: Qt.size(422, 722)
    maximumSize: Qt.size(422, 722)
    color: Commons.Color.background

    // Sized to the card, which is shorter than the panel's 720 during setup.
    Item {
      id: frame
      width: parent.width
      height: panel.height + 2

      Omihomo.Panel {
        id: panel
        x: 1
        y: 1
        width: 420
        height: drive.card ? drive.card.height : 720
        bar: bar
        onOpenedChanged: if (!opened) Qt.callLater(open)
        Component.onCompleted: {
          service.cliPath = Quickshell.env("OMIHOMO_DRIVE_CLI")
          drive.card = Array.prototype.find.call(children, function(child) { return child.contentHeight !== undefined })
        }
      }

      TestEvent { id: events }
    }
  }
}
