import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui

Item {
  id: readout

  required property Theme theme
  property string label: ""
  property string value: ""
  property bool faded: false
  property bool live: true
  property string tooltip: ""

  signal activated()

  Layout.fillWidth: true
  implicitHeight: readoutColumn.implicitHeight

  Column {
    id: readoutColumn
    width: parent.width
    spacing: Style.space(2)

    PanelText {
      theme: readout.theme
      text: readout.label
      color: readout.theme.dim
      font.letterSpacing: Style.space(0.8)
    }

    PanelText {
      theme: readout.theme
      width: parent.width
      text: readout.value
      opacity: readout.live ? 1 : 0.35
      color: readout.faded ? readout.theme.dim : readout.theme.foreground
      font.pixelSize: Style.font.subtitle
      font.underline: readout.tooltip !== "" && readoutMouse.containsMouse
      elide: Text.ElideRight
    }
  }

  MouseArea {
    id: readoutMouse
    anchors.fill: parent
    enabled: readout.tooltip !== ""
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: readout.activated()
  }

  PanelToolTip {
    visible: readout.tooltip !== "" && readoutMouse.containsMouse
    text: readout.tooltip
    fontFamily: readout.theme.fontFamily
  }
}
