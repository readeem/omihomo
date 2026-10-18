import QtQuick
import qs.Commons
import qs.Commons as Commons

// The panel's palette, taken from the bar it sits on, and its emoji images.
// Panel owns the one instance and hands it to every component.
QtObject {
  property var bar: null
  property EmojiImages emoji: null

  readonly property color foreground: bar ? bar.foreground : Commons.Color.foreground
  readonly property color urgent: bar ? bar.urgent : Commons.Color.urgent
  readonly property color accent: Commons.Color.accent
  readonly property color dim: Util.alpha(foreground, 0.58)
  readonly property color faint: Util.alpha(foreground, 0.34)
  readonly property color line: Util.alpha(foreground, 0.11)
  readonly property color surface: Commons.Color.popups.background
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property color hoverFill: Style.hoverFillFor(foreground, accent)
  readonly property color soft: Util.alpha(accent, 0.16)
  readonly property color fill: Util.alpha(foreground, 0.05)
  readonly property color outline: Util.alpha(foreground, 0.22)
  readonly property real controlRadius: Math.min(Style.cornerRadius, Style.space(7))
  readonly property real cardRadius: Math.min(Style.cornerRadius, Style.space(10))

  function css(c) {
    return "rgba(" + Math.round(c.r * 255) + "," + Math.round(c.g * 255) + "," + Math.round(c.b * 255) + "," + c.a + ")"
  }

  function latencyFill(heat) {
    return Qt.tint(Util.alpha(foreground, 0.08), Util.alpha(urgent, 0.06 + 0.5 * heat))
  }
}
