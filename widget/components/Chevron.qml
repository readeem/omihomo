import QtQuick
import qs.Commons

PanelText {
  property bool expanded: false
  property bool doubleArrow: false
  color: theme.foreground
  font.pixelSize: doubleArrow ? Style.font.icon : Style.font.body
  text: doubleArrow ? (expanded ? "󰕎" : "󰕏") : (expanded ? "󰅀" : "󰅂")
}
