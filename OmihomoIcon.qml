import QtQuick
import qs.Commons
import qs.Ui

// Omihomo's mark, drawn natively rather than shipped as an SVG: three routing
// lanes, the middle one shifted to read as traffic taking a different path.
// Tiny SVGs render unevenly in a bar slot; rectangles do not.
Item {
  id: root

  property real iconSize: Style.font.icon
  property color color: Color.foreground
  property color badgeColor: Color.urgent
  // The core is installed but not running.
  property bool crossed: false
  // Something needs attention — a degraded core, or a missing TUN device.
  property bool warning: false
  // TUN is carrying the traffic.
  property bool tunnelled: false

  width: iconSize
  height: iconSize
  implicitWidth: iconSize
  implicitHeight: iconSize

  readonly property real laneHeight: Math.max(2, Math.round(iconSize * 0.16))
  readonly property real laneGap: Math.max(2, Math.round(iconSize * 0.14))

  Column {
    anchors.centerIn: parent
    spacing: root.laneGap

    Lane { laneWidth: root.iconSize; offset: 0 }
    Lane { laneWidth: root.iconSize * 0.6; offset: root.iconSize * 0.4 }
    Lane { laneWidth: root.iconSize; offset: 0 }
  }

  Rectangle {
    visible: root.crossed
    anchors.centerIn: parent
    width: parent.width * 1.22
    height: Math.max(2, parent.height * 0.13)
    radius: height / 2
    color: root.color
    rotation: -45
  }

  BorderSurface {
    visible: root.warning || root.tunnelled
    width: Math.max(6, parent.width * 0.36)
    height: width
    radius: width / 2
    color: root.warning ? root.badgeColor : root.color
    anchors.right: parent.right
    anchors.bottom: parent.bottom
    borderSpec: Border.flat(Color.popups.background, 1)
  }

  component Lane: Rectangle {
    property real laneWidth: 0
    property real offset: 0
    x: offset
    width: laneWidth
    height: root.laneHeight
    radius: height / 2
    color: root.color
  }
}
