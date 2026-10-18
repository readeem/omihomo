import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../Model.js" as Model

RowLayout {
  id: name

  required property Theme theme
  property string text: ""
  property color color: theme.foreground
  property int pixelSize: Style.font.bodySmall
  property bool bold: false
  readonly property var parts: Model.nameParts(text)
  readonly property bool tailed: parts.tail.length > 0

  spacing: markSlot.advanceWidth / 2

  TextMetrics {
    id: markSlot
    font.family: name.theme.fontFamily
    font.pixelSize: name.pixelSize
    text: "MM"
  }

  EmojiGlyph {
    theme: name.theme
    visible: name.parts.mark !== ""
    Layout.minimumWidth: markSlot.advanceWidth
    Layout.maximumWidth: markSlot.advanceWidth
    emoji: name.parts.mark
    color: name.color
    pixelSize: name.pixelSize
  }
  PanelText {
    id: nameRest
    theme: name.theme
    Layout.fillWidth: true
    // With emoji after it, the text keeps to its own width so they follow
    // it, and the spacer below takes the rest of the row.
    Layout.maximumWidth: name.tailed ? implicitWidth : Number.POSITIVE_INFINITY
    text: name.parts.rest
    color: name.color
    font.pixelSize: name.pixelSize
    font.bold: name.bold
    elide: Text.ElideRight
  }
  Repeater {
    model: name.parts.tail
    EmojiGlyph {
      required property string modelData
      theme: name.theme
      emoji: modelData
      color: name.color
      pixelSize: name.pixelSize
    }
  }
  Item {
    visible: name.tailed
    Layout.fillWidth: true
  }
}
