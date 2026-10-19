import QtQuick
import qs.Commons

Rectangle {
  id: panelSwitch
  required property Theme theme
  property bool checked: false
  property bool busy: false
  property bool interactive: true
  property color foreground: theme.foreground
  property int trackWidth: Style.space(40)
  property int trackHeight: Style.space(22)
  readonly property alias containsMouse: switchMouse.containsMouse
  signal toggled()

  implicitWidth: trackWidth
  implicitHeight: trackHeight
  radius: Style.cornerRadius > 0 ? height / 2 : 0
  color: checked ? theme.accent : theme.hoverFill
  border.width: 1
  border.color: switchMouse.containsMouse ? foreground : (checked ? theme.accent : theme.outline)
  opacity: enabled ? 1 : 0.35

  Rectangle {
    width: parent.height - Style.space(6)
    height: width
    radius: Style.cornerRadius > 0 ? width / 2 : 0
    x: panelSwitch.checked ? panelSwitch.width - width - Style.space(3) : Style.space(3)
    anchors.verticalCenter: parent.verticalCenter
    color: panelSwitch.checked ? panelSwitch.theme.surface : panelSwitch.theme.dim
    Behavior on x { NumberAnimation { duration: typeof Style.duration === "function" ? Style.duration(220) : 220; easing.type: Easing.OutBack } }
  }

  MouseArea {
    id: switchMouse
    anchors.fill: parent
    enabled: panelSwitch.interactive
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: if (!panelSwitch.busy) panelSwitch.toggled()
  }
}
