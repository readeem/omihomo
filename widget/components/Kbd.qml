import QtQuick
import qs.Commons

Rectangle {
  id: kbd

  required property Theme theme
  property string text: ""

  implicitWidth: Math.max(kbdText.implicitWidth + Style.space(8), Style.space(18))
  implicitHeight: Style.space(18)
  radius: Math.min(Style.cornerRadius, 4)
  color: Util.alpha(theme.foreground, 0.06)
  border.color: Util.alpha(theme.foreground, 0.2)
  border.width: 1

  PanelText {
    id: kbdText
    theme: kbd.theme
    anchors.centerIn: parent
    anchors.verticalCenterOffset: -0.5
    text: kbd.text
    color: kbd.theme.foreground
  }
}
