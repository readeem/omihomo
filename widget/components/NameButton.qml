import QtQuick

SmallButton {
  id: button

  required property string label

  implicitWidth: caption.implicitWidth + horizontalPadding * 2 + _reservedBorderLeft + _reservedBorderRight
  Accessible.name: label

  NameText {
    id: caption
    theme: button.theme
    anchors.centerIn: parent
    width: Math.min(implicitWidth, Math.max(0, button.width - button.horizontalPadding * 2 - button.borderLeft - button.borderRight))
    text: button.label
    color: button.foreground
    pixelSize: button.fontSize
  }
}
