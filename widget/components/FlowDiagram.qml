import QtQuick
import qs.Commons
import "../Model.js" as Model

Canvas {
  id: diagram

  required property Theme theme
  property var flows: ({ sources: [], targets: [], links: [] })
  // The open stack under the cursor, as { source, target }, or null.
  property var hot: null

  readonly property real pad: Style.space(14)
  readonly property real sourceX: Style.space(86)
  readonly property real targetX: width - Style.space(96)

  function rowY(index, count) {
    return count < 2 ? height / 2 : pad + index * (height - pad * 2) / (count - 1)
  }

  function isHot(source, target) {
    return hot !== null && (source === undefined || source === hot.source)
      && (target === undefined || target === hot.target)
  }

  PanelText {
    theme: diagram.theme
    visible: diagram.flows.sources.length === 0
    anchors.verticalCenter: parent.verticalCenter
    text: "No open connections."
    color: diagram.theme.dim
  }

  Repeater {
    model: diagram.flows.sources
    NameText {
      required property var modelData
      required property int index
      theme: diagram.theme
      width: Math.min(implicitWidth, diagram.sourceX - Style.space(8))
      x: diagram.sourceX - Style.space(6) - width
      y: diagram.rowY(index, diagram.flows.sources.length) - height / 2
      text: modelData.name
      color: diagram.isHot(modelData.name, undefined) ? diagram.theme.accent : diagram.theme.foreground
    }
  }

  Repeater {
    model: diagram.flows.targets
    Item {
      id: target
      required property var modelData
      required property int index
      x: diagram.targetX + Style.space(6)
      y: diagram.rowY(index, diagram.flows.targets.length)
      width: diagram.width - x - Style.space(2)

      NameText {
        theme: diagram.theme
        width: parent.width
        y: -Style.space(5) - height / 2
        text: target.modelData.name
        color: diagram.isHot(undefined, target.modelData.name) ? diagram.theme.accent : diagram.theme.foreground
      }
      PanelText {
        theme: diagram.theme
        y: Style.space(7) - height / 2
        text: Model.formatBytes(target.modelData.bytes)
        color: diagram.theme.dim
      }
    }
  }

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
    function indexOf(nodes, name) {
      for (let n = 0; n < nodes.length; n++) if (nodes[n].name === name) return n
      return -1
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
      const middle = (sourceX + targetX) / 2
      ctx.beginPath()
      ctx.moveTo(sourceX + 4, y1)
      ctx.bezierCurveTo(middle, y1, middle, y2, targetX - 4, y2)
      ctx.lineWidth = Math.max(1.5, Math.min(12, 3.5 * Math.log(Math.max(1, link.bytes / 2e4)) / Math.LN10))
      ctx.strokeStyle = theme.css(isHot(link.source, link.target) ? theme.accent : muted)
      ctx.stroke()
    }

    for (i = 0; i < sources.length; i++) {
      const sy = rowY(i, sources.length)
      ctx.fillStyle = theme.css(isHot(sources[i].name, undefined) ? theme.accent : theme.foreground)
      ctx.fillRect(sourceX, sy - 7, 4, 14)
    }

    for (i = 0; i < targets.length; i++) {
      const ty = rowY(i, targets.length)
      ctx.fillStyle = theme.css(isHot(undefined, targets[i].name) ? theme.accent : theme.foreground)
      ctx.fillRect(targetX - 4, ty - 7, 4, 14)
    }
  }
}
