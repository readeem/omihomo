import QtQuick
import Quickshell
import qs.Ui
import "omihomo/widget" as Omihomo

// Loads the panel on the shell it is staged with and shows every view, for
// panel_state_test.sh to fail on any QML warning the widget raises there.
ShellRoot {
  PluginBarApi { id: bar; pluginId: "dev.readeem.omihomo" }

  FloatingWindow {
    implicitWidth: 422
    implicitHeight: 722
    Omihomo.Panel {
      id: panel
      width: 420
      height: 720
      bar: bar
      Component.onCompleted: open()
    }
  }

  StateTest {
    name: "PANEL"
    steps: ["configs", "subs", "rules", "conns", "settings"].map(function(view, i) {
      return { name: view, until: function() { return panel.service.proxiesLoaded }, then: function() { panel.view = view } }
    }).concat([{ then: function() { panel.close() } }])
  }
}
