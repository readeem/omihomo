import QtQuick
import qs.Commons

Rectangle {
  id: toggle
  required property Theme theme
  property bool checked: false
  property bool busy: false
  signal toggled()
  implicitWidth: Style.space(16)
  implicitHeight: Style.space(16)
  radius: Math.min(theme.controlRadius, Style.space(3))
  color: checked ? theme.foreground : "transparent"
  border.width: 1
  border.color: mouse.containsMouse || checked ? theme.foreground : theme.outline
  opacity: enabled ? 1 : 0.4
  PanelText {
    theme: toggle.theme
    anchors.centerIn: parent
    text: toggle.checked ? "✓" : ""
    color: toggle.theme.surface
  }
  MouseArea {
    id: mouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: if (!toggle.busy) toggle.toggled()
  }
}
