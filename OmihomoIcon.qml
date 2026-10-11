import QtQuick
import qs.Commons
import qs.Commons as Commons

// Omihomo's mark, drawn natively rather than shipped as an SVG: a globe, built
// from a circle for the sphere, two mirrored curves for the meridian, and a bar
// for the equator. It is painted opaque on one surface and faded as a whole, so
// a translucent colour never darkens where the strokes meet.
Canvas {
  id: root

  property real iconSize: Style.font.icon
  property color color: Commons.Color.foreground
  // The core is installed but not running.
  property bool crossed: false
  // Something needs the user's attention; shown as a dot in this colour.
  property color alertColor: "transparent"

  width: iconSize
  height: iconSize
  implicitWidth: iconSize
  implicitHeight: iconSize
  opacity: color.a

  readonly property real stroke: Math.max(1, Math.round(iconSize * 0.1))

  onColorChanged: requestPaint()
  onCrossedChanged: requestPaint()
  onAlertColorChanged: requestPaint()
  onWidthChanged: requestPaint()
  onVisibleChanged: if (visible) requestPaint()

  onPaint: {
    var ctx = getContext("2d")
    ctx.reset()
    var c = width / 2
    var r = (width - stroke) / 2
    ctx.strokeStyle = Qt.rgba(color.r, color.g, color.b, 1)
    ctx.lineWidth = stroke
    ctx.lineCap = "round"
    ctx.beginPath()
    ctx.arc(c, c, r, 0, Math.PI * 2)
    ctx.moveTo(c, c + r)
    ctx.bezierCurveTo(c - r * 0.805, c + r * 0.12, c - r * 0.335, c - r * 0.7, c, c - r)
    ctx.moveTo(c, c + r)
    ctx.bezierCurveTo(c + r * 0.805, c + r * 0.12, c + r * 0.335, c - r * 0.7, c, c - r)
    ctx.moveTo(c - r, c)
    ctx.lineTo(c + r, c)
    ctx.stroke()
    if (crossed) {
      var bar = Math.max(2, width * 0.13)
      var reach = c - bar / 2
      ctx.beginPath()
      ctx.moveTo(c - reach, c + reach)
      ctx.lineTo(c + reach, c - reach)
      ctx.globalCompositeOperation = "destination-out"
      ctx.lineWidth = bar + stroke * 2
      ctx.stroke()
      ctx.globalCompositeOperation = "source-over"
      ctx.lineWidth = bar
      ctx.stroke()
    }
    if (alertColor.a > 0) {
      var dot = width * 0.17
      ctx.globalCompositeOperation = "destination-out"
      ctx.beginPath()
      ctx.arc(width - dot, dot, dot + stroke, 0, Math.PI * 2)
      ctx.fill()
      ctx.globalCompositeOperation = "source-over"
      ctx.fillStyle = alertColor
      ctx.beginPath()
      ctx.arc(width - dot, dot, dot, 0, Math.PI * 2)
      ctx.fill()
    }
  }
}
