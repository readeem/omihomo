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

  spacing: 0

  Repeater {
    model: Model.nameParts(name.text)

    Item {
      id: part
      required property var modelData
      // Rounding down a fractional native-text width can elide a fitting segment.
      implicitWidth: Math.ceil(modelData.emoji ? glyph.implicitWidth : label.implicitWidth)
      implicitHeight: modelData.emoji ? glyph.implicitHeight : label.implicitHeight
      Layout.fillWidth: !modelData.emoji
      Layout.minimumWidth: modelData.emoji ? implicitWidth : 0
      Layout.maximumWidth: implicitWidth

      EmojiGlyph {
        id: glyph
        theme: name.theme
        visible: part.modelData.emoji
        anchors.centerIn: parent
        emoji: part.modelData.emoji ? part.modelData.text : ""
        color: name.color
        pixelSize: name.pixelSize
      }

      PanelText {
        id: label
        theme: name.theme
        visible: !part.modelData.emoji
        anchors.fill: parent
        text: part.modelData.emoji ? "" : part.modelData.text
        color: name.color
        font.pixelSize: name.pixelSize
        font.bold: name.bold
        elide: Text.ElideRight
      }
    }
  }

  Item { Layout.fillWidth: true }
}
