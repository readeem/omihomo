import QtQuick
import QtQuick.Shapes
import qs.Commons

// Omihomo's mark, drawn natively rather than shipped as an SVG: a globe, built
// from a circle for the sphere, two mirrored curves for the meridian, and a bar
// for the equator. Tiny SVGs render unevenly in a bar slot; these do not.
Item {
  id: root

  property real iconSize: Style.font.icon
  property color color: Color.foreground
  // The core is installed but not running.
  property bool crossed: false

  width: iconSize
  height: iconSize
  implicitWidth: iconSize
  implicitHeight: iconSize

  readonly property real stroke: Math.max(1, Math.round(iconSize * 0.1))
  // Everything is laid out against the sphere's stroke centreline, so the
  // meridian's tips and the equator's ends land inside the ring rather than
  // crossing it.
  readonly property real centre: iconSize / 2
  readonly property real sphereRadius: (iconSize - stroke) / 2

  Rectangle {
    anchors.fill: parent
    radius: width / 2
    color: "transparent"
    border.width: root.stroke
    border.color: root.color
  }

  // The meridian: one curve down the left of the sphere and its mirror down the
  // right, both running pole to pole. Control points are fractions of the
  // sphere's radius, so the shape holds at every icon size.
  Shape {
    anchors.fill: parent
    preferredRendererType: Shape.CurveRenderer

    ShapePath {
      strokeWidth: root.stroke
      strokeColor: root.color
      fillColor: "transparent"
      capStyle: ShapePath.RoundCap
      startX: root.centre
      startY: root.centre + root.sphereRadius

      PathCubic {
        control1X: root.centre - root.sphereRadius * 0.805
        control1Y: root.centre + root.sphereRadius * 0.12
        control2X: root.centre - root.sphereRadius * 0.335
        control2Y: root.centre - root.sphereRadius * 0.7
        x: root.centre
        y: root.centre - root.sphereRadius
      }
    }

    ShapePath {
      strokeWidth: root.stroke
      strokeColor: root.color
      fillColor: "transparent"
      capStyle: ShapePath.RoundCap
      startX: root.centre
      startY: root.centre + root.sphereRadius

      PathCubic {
        control1X: root.centre + root.sphereRadius * 0.805
        control1Y: root.centre + root.sphereRadius * 0.12
        control2X: root.centre + root.sphereRadius * 0.335
        control2Y: root.centre - root.sphereRadius * 0.7
        x: root.centre
        y: root.centre - root.sphereRadius
      }
    }
  }

  Rectangle {
    anchors.centerIn: parent
    width: root.iconSize - root.stroke
    height: root.stroke
    color: root.color
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
}
