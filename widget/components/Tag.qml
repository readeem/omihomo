import QtQuick
import qs.Commons

Rectangle {
  id: tag

  required property Theme theme
  property string text: ""
  property color tint: theme.dim
  property bool icon: false
  property bool strong: false
  property bool filled: false
  property bool pill: true
  property real maxWidth: Style.space(110)

  implicitWidth: icon ? implicitHeight : Math.min(tagText.implicitWidth, maxWidth) + Style.space(10)
  implicitHeight: tagText.implicitHeight + Style.space(2)
  radius: pill ? (Style.cornerRadius > 0 ? height / 2 : 0) : Math.min(theme.controlRadius, Style.space(4))
  color: filled ? tint : "transparent"
  border.width: 1
  border.color: Util.alpha(tint, strong ? 0.9 : 0.35)

  TextMetrics {
    id: iconMetrics
    font: iconText.font
    text: tag.text
  }

  PanelText {
    id: iconText
    theme: tag.theme
    visible: tag.icon
    text: tag.text
    color: tag.filled ? tag.theme.surface : tag.tint
    x: (tag.width - iconMetrics.tightBoundingRect.width) / 2 - iconMetrics.tightBoundingRect.x
    y: (tag.height - iconMetrics.tightBoundingRect.height) / 2 - iconMetrics.tightBoundingRect.y - baselineOffset
  }

  NameText {
    id: tagText
    visible: !tag.icon
    theme: tag.theme
    anchors.centerIn: parent
    width: Math.min(implicitWidth, tag.maxWidth)
    text: tag.text
    color: tag.filled ? tag.theme.surface : tag.tint
  }
}
