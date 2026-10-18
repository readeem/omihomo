import QtQuick
import qs.Commons

// One emoji, drawn from its cached image. Until that image exists, the font
// glyph stands in.
Item {
  id: glyph

  required property Theme theme
  property string emoji: ""
  property color color: theme.foreground
  property int pixelSize: Style.font.bodySmall
  readonly property string source: theme.emoji ? theme.emoji.image(emoji) : ""

  implicitWidth: source !== "" ? image.width : fallback.implicitWidth
  implicitHeight: fallback.implicitHeight

  onEmojiChanged: if (theme.emoji) theme.emoji.request(emoji)
  Component.onCompleted: if (theme.emoji) theme.emoji.request(emoji)

  Image {
    id: image
    visible: glyph.source !== ""
    anchors.verticalCenter: parent.verticalCenter
    height: Math.round(glyph.pixelSize * 1.2)
    width: implicitHeight > 0 ? Math.round(height * implicitWidth / implicitHeight) : height
    source: glyph.source
    fillMode: Image.PreserveAspectFit
    smooth: true
    mipmap: true
    asynchronous: true
  }
  PanelText {
    id: fallback
    theme: glyph.theme
    visible: glyph.source === ""
    text: glyph.emoji
    color: glyph.color
    font.pixelSize: glyph.pixelSize
  }
}
