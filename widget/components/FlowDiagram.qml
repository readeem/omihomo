import QtQuick
import qs.Commons
import "../Model.js" as Model

Canvas {
  id: diagram

  required property Theme theme
  property var flows: ({ sources: [], targets: [], links: [] })
  // The open stack under the cursor, as { source, target }, or null.
  property var hot: null

  onFlowsChanged: requestPaint()
  onHotChanged: requestPaint()
  onWidthChanged: requestPaint()
  onHeightChanged: requestPaint()
  onVisibleChanged: if (visible) requestPaint()

  onPaint: {
    var ctx = getContext("2d")
    ctx.reset()
    var sources = flows.sources
    var targets = flows.targets
    ctx.font = Style.font.bodySmall + "px \"" + theme.fontFamily + "\""
    ctx.textBaseline = "middle"
    if (sources.length === 0) {
      ctx.fillStyle = theme.css(theme.dim)
      ctx.fillText("No open connections.", 0, height / 2)
      return
    }
    var pad = Style.space(14)
    var left = Style.space(86)
    var right = width - Style.space(96)

    function rowY(index, count) {
      return count < 2 ? height / 2 : pad + index * (height - pad * 2) / (count - 1)
    }
    function indexOf(nodes, name) {
      for (let n = 0; n < nodes.length; n++) if (nodes[n].name === name) return n
      return -1
    }
    function fit(text, room) {
      var out = text
      if (ctx.measureText(out).width <= room) return out
      while (out.length > 1 && ctx.measureText(out + "…").width > room) out = out.slice(0, -1)
      return out + "…"
    }
    function isHot(source, target) {
      return hot !== null && (source === undefined || source === hot.source)
        && (target === undefined || target === hot.target)
    }

    var i
    var links = flows.links.slice().sort(function(a, b) {
      return isHot(a.source, a.target) - isHot(b.source, b.target)
    })
    var blend = Qt.tint(theme.surface, Util.alpha(theme.foreground, 0.22))
    var muted = Qt.rgba(blend.r, blend.g, blend.b, 1)
    for (i = 0; i < links.length; i++) {
      const link = links[i]
      const y1 = rowY(indexOf(sources, link.source), sources.length)
      const y2 = rowY(indexOf(targets, link.target), targets.length)
      const middle = (left + right) / 2
      ctx.beginPath()
      ctx.moveTo(left + 4, y1)
      ctx.bezierCurveTo(middle, y1, middle, y2, right - 4, y2)
      ctx.lineWidth = Math.max(1.5, Math.min(12, 3.5 * Math.log(Math.max(1, link.bytes / 2e4)) / Math.LN10))
      ctx.strokeStyle = theme.css(isHot(link.source, link.target) ? theme.accent : muted)
      ctx.stroke()
    }

    for (i = 0; i < sources.length; i++) {
      const sy = rowY(i, sources.length)
      ctx.fillStyle = theme.css(isHot(sources[i].name, undefined) ? theme.accent : theme.foreground)
      ctx.fillRect(left, sy - 7, 4, 14)
      ctx.textAlign = "right"
      ctx.fillText(fit(sources[i].name, left - 8), left - 6, sy)
    }

    ctx.textAlign = "left"
    for (i = 0; i < targets.length; i++) {
      const ty = rowY(i, targets.length)
      ctx.fillStyle = theme.css(isHot(undefined, targets[i].name) ? theme.accent : theme.foreground)
      ctx.fillRect(right - 4, ty - 7, 4, 14)
      ctx.fillText(fit(targets[i].name, width - right - 8), right + 6, ty - 5)
      ctx.fillStyle = theme.css(theme.dim)
      ctx.fillText(Model.formatBytes(targets[i].bytes), right + 6, ty + 7)
    }
  }
}
