import QtQuick
import QtQuick.Shapes
import qs.Commons

Shape {
  id: chevron
  property color color: "white"
  property bool expanded: false
  property bool doubleArrow: false
  implicitWidth: 12
  implicitHeight: 16
  ShapePath {
    strokeColor: chevron.color
    strokeWidth: Style.space(1.5)
    fillColor: "transparent"
    capStyle: ShapePath.RoundCap
    joinStyle: ShapePath.RoundJoin
    PathSvg {
      path: chevron.doubleArrow
        ? (chevron.expanded ? "M 2 2 L 6 6 L 10 2 M 2 14 L 6 10 L 10 14" : "M 2 6 L 6 2 L 10 6 M 2 10 L 6 14 L 10 10")
        : (chevron.expanded ? "M 2 6 L 6 10 L 10 6" : "M 4 4 L 8 8 L 4 12")
    }
  }
}
