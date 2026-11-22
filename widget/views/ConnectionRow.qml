import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../components"
import "../Model.js" as Model

NavRow {
  id: connectionRow

  property var stack: null
  readonly property bool live: !!stack && stack.open
  readonly property double bytes: stack ? stack.download + stack.upload : 0

  section: "conn"
  implicitHeight: Style.space(24)
  opacity: live || (stack && stack.blocked) ? 1 : 0.72
  onActivated: panel.activateConnectionAt(connectionRow.rowIndex)

  Rectangle {
    anchors.left: parent.left
    anchors.top: parent.top
    anchors.bottom: parent.bottom
    anchors.margins: 1
    width: (parent.width - 2) * Math.max(0.02, Math.min(1, connectionRow.panel.largestStack > 5000 && connectionRow.bytes > 5000
      ? Math.log(connectionRow.bytes / 5000) / Math.log(connectionRow.panel.largestStack / 5000) : 0))
    radius: connectionRow.theme.controlRadius
    color: Util.alpha(connectionRow.theme.foreground, 0.07)
  }

  RowLayout {
    anchors.fill: parent
    anchors.leftMargin: Style.space(6)
    anchors.rightMargin: Style.space(2)
    spacing: Style.space(6)

    PanelText {
      theme: connectionRow.theme
      text: connectionRow.stack && connectionRow.stack.blocked ? "⊘" : (connectionRow.live ? "●" : "○")
      color: connectionRow.stack && connectionRow.stack.blocked ? connectionRow.theme.urgent : (connectionRow.live ? connectionRow.theme.accent : connectionRow.theme.faint)
      font.pixelSize: Style.font.caption
    }

    PanelText {
      theme: connectionRow.theme
      Layout.maximumWidth: Style.space(96)
      text: connectionRow.stack ? (connectionRow.stack.processStatus === "no_local_socket" ? "No socket" : connectionRow.stack.process || "unknown") : ""
      color: connectionRow.live ? connectionRow.theme.foreground : connectionRow.theme.dim
      font.bold: connectionRow.live
      elide: Text.ElideRight
    }

    PanelText {
      theme: connectionRow.theme
      visible: connectionRow.stack && connectionRow.stack.count > 1
      text: connectionRow.stack ? "×" + connectionRow.stack.count : ""
      color: connectionRow.theme.dim
    }

    PanelText {
      theme: connectionRow.theme
      Layout.fillWidth: true
      text: connectionRow.stack ? connectionRow.stack.host : ""
      color: connectionRow.theme.dim
      elide: Text.ElideRight
    }

    Tag {
      theme: connectionRow.theme
      visible: text !== ""
      text: connectionRow.stack ? connectionRow.stack.chain : ""
      maxWidth: Style.space(80)
    }

    PanelText {
      theme: connectionRow.theme
      text: Model.formatBytes(connectionRow.bytes)
      color: connectionRow.live ? connectionRow.theme.foreground : connectionRow.theme.dim
    }

    InlineAction {
      theme: connectionRow.theme
      opacity: connectionRow.hasCursor && connectionRow.live ? 1 : 0
      iconText: "×"
      tooltipText: "Close · x"
      foreground: connectionRow.theme.foreground
      hoverColor: connectionRow.theme.urgent
      size: Style.space(18)
      onClicked: connectionRow.panel.activateConnectionAt(connectionRow.rowIndex)
    }
  }
  PanelToolTip {
    visible: connectionRow.hovered && !!connectionRow.stack
    text: {
      var stack = connectionRow.stack
      if (!stack) return ""
      var parts = []
      if (stack.network !== "") parts.push(stack.network)
      if (stack.inbound !== "") parts.push(stack.inbound)
      var attribution = Model.processDiagnostic(stack)
      if (attribution !== "") parts.push(attribution)
      if (stack.rule !== "") parts.push(stack.rule)
      if (connectionRow.live) {
        parts.push(Model.formatDuration(connectionRow.panel.nowMs - stack.startMs))
      } else {
        parts.push((stack.blocked ? "blocked " : "closed ") + Model.relativeSince(stack.closedAtMs, connectionRow.panel.nowMs))
      }
      return parts.join(" · ")
    }
    fontFamily: connectionRow.theme.fontFamily
  }
}
