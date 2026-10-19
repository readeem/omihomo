import QtQuick
import qs.Commons

Canvas {
  id: graph

  required property Theme theme
  property var samples: []
  property int capacity: 2

  onSamplesChanged: requestPaint()
  onWidthChanged: requestPaint()
  onHeightChanged: requestPaint()
  onVisibleChanged: if (visible) requestPaint()

  onPaint: {
    var ctx = getContext("2d")
    ctx.reset()
    var list = samples || []
    var slots = Math.max(2, capacity)
    var step = width / (slots - 1)
    var offset = slots - list.length
    var peak = 1
    var i
    for (i = 0; i < list.length; i++) peak = Math.max(peak, list[i].down, list[i].up)

    ctx.strokeStyle = theme.css(theme.line)
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.moveTo(0, height - 0.5)
    ctx.lineTo(width, height - 0.5)
    ctx.stroke()
    if (list.length < 2) return

    function trace(key) {
      ctx.beginPath()
      for (let j = 0; j < list.length; j++) {
        const x = (offset + j) * step
        const y = height - 1 - (list[j][key] / peak) * (height - 3)
        if (j === 0) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      }
    }

    var series = [["down", theme.foreground, 0.14], ["up", theme.accent, 0.2]]
    for (i = 0; i < series.length; i++) {
      trace(series[i][0])
      ctx.lineTo(width, height)
      ctx.lineTo(offset * step, height)
      ctx.closePath()
      ctx.fillStyle = theme.css(Util.alpha(series[i][1], series[i][2]))
      ctx.fill()
      trace(series[i][0])
      ctx.strokeStyle = theme.css(series[i][1])
      ctx.lineWidth = 1.2
      ctx.stroke()
    }
  }
}
