import QtQuick
import QtQuick.Layouts
import QtQuick.Shapes as Shapes
import qs.Commons
import qs.Ui

Rectangle {
  id: tab

  required property var panel
  readonly property Theme theme: panel.theme
  property string target: ""
  property int number: 0
  property string label: ""
  property string count: ""
  property bool alert: false
  property bool gear: false
  readonly property bool current: panel.view === target
  readonly property color iconColor: panel.tunRedirectBlocked ? theme.urgent
    : (current || tabMouse.containsMouse ? theme.foreground : theme.dim)
  onIconColorChanged: settingsIcon.requestPaint()

  Layout.fillWidth: true
  implicitHeight: Style.space(44)
  radius: theme.controlRadius
  color: current || tabMouse.containsMouse ? theme.hoverFill : "transparent"
  border.width: 1
  border.color: current ? theme.outline : "transparent"

  PanelText {
    theme: tab.theme
    visible: !tab.gear
    anchors.left: parent.left
    anchors.top: parent.top
    anchors.leftMargin: Style.space(6)
    anchors.topMargin: Style.space(3)
    text: String(tab.number)
    color: tab.theme.faint
  }

  PanelText {
    theme: tab.theme
    anchors.horizontalCenter: parent.horizontalCenter
    anchors.verticalCenter: tab.gear ? parent.verticalCenter : undefined
    anchors.bottom: tab.gear ? undefined : parent.bottom
    anchors.bottomMargin: Style.space(6)
    visible: !tab.gear
    text: tab.label
    color: tab.current ? tab.theme.foreground : tab.theme.dim
    font.pixelSize: tab.gear ? Style.font.iconLarge : Style.font.body
  }

  Canvas {
    id: settingsIcon
    visible: tab.gear
    anchors.centerIn: parent
    width: Style.space(17)
    height: width
    onVisibleChanged: if (visible) requestPaint()
    onWidthChanged: requestPaint()
    onHeightChanged: requestPaint()
    onPaint: {
      var ctx = getContext("2d")
      ctx.reset()
      ctx.scale(width / 24, height / 24)
      ctx.strokeStyle = tab.theme.css(tab.iconColor)
      ctx.lineWidth = 1.8
      ctx.lineCap = "round"
      ctx.beginPath()
      ctx.moveTo(4, 7); ctx.lineTo(14, 7)
      ctx.moveTo(18, 7); ctx.lineTo(20, 7)
      ctx.moveTo(4, 17); ctx.lineTo(8, 17)
      ctx.moveTo(12, 17); ctx.lineTo(20, 17)
      ctx.stroke()
      ctx.beginPath()
      ctx.arc(16, 7, 2, 0, Math.PI * 2)
      ctx.moveTo(12, 17); ctx.arc(10, 17, 2, 0, Math.PI * 2)
      ctx.stroke()
    }
  }

  Rectangle {
    visible: !tab.gear && tab.count !== ""
    anchors.top: parent.top
    anchors.right: parent.right
    anchors.topMargin: Style.space(3)
    anchors.rightMargin: Style.space(5)
    implicitWidth: countText.implicitWidth + Style.space(8)
    implicitHeight: Style.space(15)
    radius: Math.min(tab.theme.controlRadius, Style.space(4))
    color: tab.alert ? tab.theme.urgent : tab.theme.hoverFill
    PanelText {
      id: countText
      theme: tab.theme
      anchors.centerIn: parent
      text: tab.count
      color: tab.alert ? tab.theme.surface : tab.theme.dim
    }
  }

  Shapes.Shape {
    id: underline
    visible: tab.current
    anchors.fill: parent
    anchors.margins: tab.border.width
    preferredRendererType: Shapes.Shape.CurveRenderer
    readonly property real r: Math.max(0, tab.radius - tab.border.width)
    readonly property real t: Style.space(2)

    Shapes.ShapePath {
      strokeColor: "transparent"
      fillColor: tab.theme.accent
      PathSvg {
        readonly property real w: underline.width
        readonly property real h: underline.height
        readonly property real r: underline.r
        readonly property real t: underline.t
        path: "M 0 " + (h - r) + " Q 0 " + h + " " + r + " " + h + " H " + (w - r)
          + " Q " + w + " " + h + " " + w + " " + (h - r) + " V " + (h - r - t)
          + " Q " + w + " " + (h - t) + " " + (w - r) + " " + (h - t) + " H " + r
          + " Q 0 " + (h - t) + " 0 " + (h - r - t) + " Z"
      }
    }
  }

  MouseArea {
    id: tabMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onContainsMouseChanged: if (containsMouse) tab.panel.cursorActive = false
    onClicked: tab.panel.openView(tab.gear && tab.current ? "configs" : tab.target)
  }

  PanelToolTip {
    visible: tabMouse.containsMouse
    text: tab.gear ? "Settings · M" : tab.label + " · " + tab.number
    fontFamily: tab.theme.fontFamily
  }
}
