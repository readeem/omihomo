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
      elide: Text.ElideRight
      text: "↓ " + Model.formatBytes(view.service.connectionsDownload)
        + "  ↑ " + Model.formatBytes(view.service.connectionsUpload) + " this session"
      color: view.theme.foreground
    }

    PanelText {
      theme: view.theme
      text: "process → config"
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
    note: view.service.openConnections.length < view.service.openConnectionCount ? "Showing " + view.service.openConnections.length : "bar = bytes, log scale"
  }

  SectionLine {
    theme: view.theme
    Layout.fillWidth: true
    visible: !!view.service.connectionSummary.session
    title: "CLOSED · " + (view.service.connectionSummary.closed || 0)
      + "     BLOCKED · " + (view.service.connectionSummary.blocked || 0)
  }

  PanelText {
    theme: view.theme
    visible: view.service.connectionStacks.length === 0 && !view.service.connectionHistoryLoading
      && !view.service.connectionSummary.closed && !view.service.connectionSummary.blocked
    Layout.fillWidth: true
    text: view.service.connectionSummary.session ? "Nothing logged yet." : "No proxy session yet."
    color: view.theme.dim
    font.pixelSize: Style.font.body
  }

  ScrollList {
    objectName: "connectionList"
    id: connList
    theme: view.theme
    Layout.fillWidth: true
    Layout.fillHeight: true
    spacing: Style.space(2)
    keyedEntries: view.service.connectionStacks
    entryKey: function(e) { return e.key }

    delegate: ConnectionRow {
      required property var entry
      required property int index
      panel: view.panel
      width: connList.rowWidth
      height: implicitHeight
      stack: entry
      rowIndex: index
    }
  }

  RowLayout {
    visible: view.service.previousConnectionHistory || view.service.moreConnectionHistory
    Layout.alignment: Qt.AlignHCenter
    SmallButton {
      theme: view.theme
      enabled: view.service.previousConnectionHistory && !view.service.connectionHistoryLoading
      text: "Newer history"
      onClicked: view.service.changeHistoryPage(false)
    }
    SmallButton {
      theme: view.theme
      enabled: view.service.moreConnectionHistory && !view.service.connectionHistoryLoading
      text: "Older history"
      onClicked: view.service.changeHistoryPage(true)
    }
  }

  PanelText {
    theme: view.theme
    visible: view.service.connectionSummary.incomplete === true
    Layout.fillWidth: true
    text: "History storage limit reached or unavailable. Session totals are still counted."
    color: view.theme.urgent
    wrapMode: Text.WordWrap
  }

  PanelText {
    theme: view.theme
    visible: view.panel.errorAt("conns")
    Layout.fillWidth: true
    text: "▲ " + view.service.lastError
    color: view.theme.urgent
    wrapMode: Text.WordWrap
  }

}
