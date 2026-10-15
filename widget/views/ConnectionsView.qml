import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"
import "../Model.js" as Model

ColumnLayout {
  id: view

  required property var panel
  readonly property var service: panel.service
  readonly property Theme theme: panel.theme
  readonly property alias list: connList
  readonly property var hotFlow: {
    var row = panel.cursorRow
    var stack = panel.cursorActive && row && row.s === "conn" ? panel.connectionAt(row.i) : null
    return stack && stack.open ? { source: stack.process || "unknown", target: stack.chain || "DIRECT" } : null
  }

  spacing: Style.space(8)

  RowLayout {
    Layout.fillWidth: true

    PanelText {
      theme: view.theme
      Layout.fillWidth: true
      text: "↓ " + Model.formatBytes(view.service.connectionsDownload)
        + "  ↑ " + Model.formatBytes(view.service.connectionsUpload) + " total"
      color: view.theme.foreground
    }

    PanelText {
      theme: view.theme
      text: "process → config · width = bytes"
      color: view.theme.faint
    }
  }

  FlowDiagram {
    theme: view.theme
    flows: view.panel.flows
    hot: view.hotFlow
    Layout.fillWidth: true
    Layout.preferredHeight: Math.max(Style.space(44),
      Style.space(22) * Math.max(view.panel.flows.sources.length, view.panel.flows.targets.length))
  }

  SectionLine {
    theme: view.theme
    Layout.fillWidth: true
    title: "OPEN · " + view.service.openConnectionCount
    note: "bar = bytes, log scale"
  }

  PanelText {
    theme: view.theme
    visible: view.service.connectionStacks.length === 0
    Layout.fillWidth: true
    text: view.panel.liveReady ? "Nothing logged yet." : "The proxy is not running."
    color: view.theme.dim
    font.pixelSize: Style.font.body
  }

  ScrollList {
    id: connList
    theme: view.theme
    Layout.fillWidth: true
    Layout.fillHeight: true
    spacing: Style.space(2)
    model: view.service.connectionStacks

    delegate: Item {
      id: connectionEntry
      required property var modelData
      required property int index
      readonly property bool closedHead: !modelData.open
        && (index === 0 || view.service.connectionStacks[index - 1].open)
      width: connList.rowWidth
      height: connection.implicitHeight + (closedHead ? Style.space(28) : 0)

      SectionLine {
        theme: view.theme
        visible: connectionEntry.closedHead
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.topMargin: Style.space(8)
        title: "CLOSED · " + view.panel.closedLogged
      }

      ConnectionRow {
        id: connection
        panel: view.panel
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        height: implicitHeight
        stack: connectionEntry.modelData
        rowIndex: connectionEntry.index
      }
    }
  }

  PanelText {
    theme: view.theme
    visible: view.panel.errorAt("conns")
    Layout.fillWidth: true
    text: "▲ " + view.service.lastError
    color: view.theme.urgent
    wrapMode: Text.WordWrap
  }

  RowLayout {
    Layout.fillWidth: true
    spacing: Style.space(6)

    SmallButton {
      theme: view.theme
      text: "close all"
      danger: true
      enabled: view.service.openConnectionCount > 0
      onClicked: view.service.closeAllConnections()
    }

    SmallButton {
      theme: view.theme
      text: "clear log"
      enabled: view.panel.closedLogged > 0
      onClicked: view.service.clearConnectionLog()
    }
  }
}
