import QtQuick
import QtQuick.Shapes as Shapes
import qs.Commons
import qs.Ui

TextField {
  id: field
  property var panel: null
  Keys.onTabPressed: if (panel) panel.switchPanel(1)
  Keys.onBacktabPressed: if (panel) panel.switchPanel(-1)
  required property Theme theme
  property bool highlighted: false
  implicitWidth: Style.space(120)
  implicitHeight: Math.max(Style.space(26), contentHeight + topPadding + bottomPadding)
  renderType: Text.NativeRendering
  font.family: theme.fontFamily
  font.pixelSize: Style.font.bodySmall
  placeholderTextColor: theme.faint
  background: Shapes.Shape {
    id: fieldBorder
    width: field.width
    height: field.height
    implicitWidth: 0
    implicitHeight: 0
    readonly property real r: field.theme.controlRadius
    Shapes.ShapePath {
      fillColor: "transparent"
      strokeColor: field.activeFocus || field.highlighted || field.hasCursor ? field.theme.accent : field.theme.outline
      strokeWidth: 1
      strokeStyle: field.activeFocus || field.highlighted || field.hasCursor ? Shapes.ShapePath.SolidLine : Shapes.ShapePath.DashLine
      dashPattern: [3, 2]
      PathSvg {
        path: "M " + fieldBorder.r + " 0.5 H " + (fieldBorder.width - fieldBorder.r)
          + " Q " + (fieldBorder.width - 0.5) + " 0.5 " + (fieldBorder.width - 0.5) + " " + fieldBorder.r
          + " V " + (fieldBorder.height - fieldBorder.r)
          + " Q " + (fieldBorder.width - 0.5) + " " + (fieldBorder.height - 0.5) + " " + (fieldBorder.width - fieldBorder.r) + " " + (fieldBorder.height - 0.5)
          + " H " + fieldBorder.r + " Q 0.5 " + (fieldBorder.height - 0.5) + " 0.5 " + (fieldBorder.height - fieldBorder.r)
          + " V " + fieldBorder.r + " Q 0.5 0.5 " + fieldBorder.r + " 0.5 Z"
      }
    }
  }
}
