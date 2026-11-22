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
    const row = panel.cursorRow
    if (!panel.cursorActive || !row || row.s !== "conn") return null
    const stack = panel.connectionAt(row.i)
    if (!stack) return null
    const flows = panel.flows
    const process = stack.process || "unknown"
    const source = flows.sources.some(node => node.name === process) ? process : ""
    const target = flows.targets.some(node => node.name === stack.chain) ? stack.chain : ""
    if (!stack.blocked && stack.upload + stack.download === 0
        && (source !== process || target !== stack.chain)) return null
    return flows.links.find(link => link.source === source && link.target === target)
      || (stack.blocked && flows.links.find(link => link.source === "" && link.target === target))
      || null
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

  PanelText {
    theme: view.theme
    Layout.fillWidth: true
    visible: !!view.service.connectionSummary.session
    color: view.theme.dim
    wrapMode: Text.NoWrap
    fontSizeMode: Text.HorizontalFit
    minimumPixelSize: Style.font.caption
    text: {
      var open = view.service.openConnectionCount
      var shown = view.service.openConnections.length
      return "Open " + open + (shown < open ? " (" + shown + " shown)" : "")
        + "   ·   Closed " + (view.service.connectionSummary.closed || 0)
        + "   ·   Blocked " + (view.service.connectionSummary.blocked || 0)
    }
  }

  PanelText {
    theme: view.theme
    visible: view.service.connectionStacks.length === 0 && !view.service.connectionHistoryLoading
      && !view.service.connectionSummary.closed && !view.service.connectionSummary.blocked
    Layout.fillWidth: true
    text: view.service.connectionSummary.cleared_at ? "Log is empty."
      : view.service.connectionSummary.session ? "Nothing logged yet." : "No proxy session yet."
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
