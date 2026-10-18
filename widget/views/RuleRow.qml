import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../components"

NavRow {
  id: ruleRow

  property var entry: ({})
  readonly property var rule: entry.rule || null
  readonly property bool own: entry.kind === "own"
  readonly property bool testing: panel.ruleTest.host !== ""
  readonly property string verdict: entry.eff >= 0 && entry.eff < panel.ruleTest.verdicts.length
    ? panel.ruleTest.verdicts[entry.eff] : ""
  readonly property bool hit: testing && panel.ruleTest.hit >= 0 && entry.eff === panel.ruleTest.hit
  readonly property bool below: testing && panel.ruleTest.hit >= 0 && (entry.eff < 0 || entry.eff > panel.ruleTest.hit)

  section: entry.s || ""
  rowIndex: entry.i === undefined ? -1 : entry.i
  current: hit
  implicitHeight: Style.space(22)
  opacity: below || (rule && rule.inactive) ? 0.5 : 1

  RowLayout {
    anchors.fill: parent
    anchors.leftMargin: Style.space(4)
    anchors.rightMargin: Style.space(2)
    spacing: Style.space(8)

    Item {
      id: rail

      readonly property real knot: Style.space(8)
      readonly property real knotTop: Math.round((height - knot) / 2)
      readonly property real stroke: Style.space(2)
      readonly property real lineX: Math.round((width - stroke) / 2)
      readonly property color tint: ruleRow.testing && !ruleRow.below ? ruleRow.theme.accent : ruleRow.theme.outline

      Layout.fillHeight: true
      implicitWidth: Style.space(14)

      Rectangle {
        visible: !ruleRow.below && ruleRow.entry.eff !== 0
        x: rail.lineX
        width: rail.stroke
        height: rail.knotTop
        color: rail.tint
      }

      Rectangle {
        visible: !ruleRow.below && !ruleRow.hit
        x: rail.lineX
        y: rail.knotTop + rail.knot
        width: rail.stroke
        height: parent.height - y
        color: rail.tint
      }

      Repeater {
        model: ruleRow.below ? [0, 2 * rail.stroke, rail.knotTop + rail.knot + rail.stroke] : []
        Rectangle {
          required property real modelData
          x: rail.lineX
          y: modelData
          width: rail.stroke
          height: rail.stroke
          color: ruleRow.theme.outline
        }
      }

      Rectangle {
        x: Math.round((parent.width - width) / 2)
        y: rail.knotTop
        width: rail.knot
        height: width
        radius: width / 2
        color: ruleRow.hit ? ruleRow.theme.accent : "transparent"
        border.width: rail.stroke
        border.color: rail.tint
      }
    }

    PanelText {
      theme: ruleRow.theme
      Layout.preferredWidth: Style.space(56)
      text: ruleRow.panel.shortRuleType(ruleRow.rule ? ruleRow.rule.type : "")
      color: ruleRow.theme.dim
      elide: Text.ElideRight
    }

    PanelText {
      theme: ruleRow.theme
      Layout.fillWidth: true
      text: ruleRow.rule ? (ruleRow.rule.value !== "" ? ruleRow.rule.value : "everything else") : ""
      color: ruleRow.own || ruleRow.hit ? ruleRow.theme.foreground : ruleRow.theme.dim
      elide: Text.ElideRight
    }

    Tag {
      theme: ruleRow.theme
      text: ruleRow.rule ? ruleRow.rule.target : ""
      tint: ruleRow.hit ? ruleRow.theme.accent : ruleRow.theme.dim
      strong: ruleRow.hit
      filled: ruleRow.hit
      maxWidth: Style.space(84)
    }

    PanelText {
      theme: ruleRow.theme
      Layout.preferredWidth: Style.space(22)
      text: ruleRow.rule && ruleRow.rule.inactive ? "off"
        : (ruleRow.hit ? "hit" : (ruleRow.verdict === "miss" ? "no" : (ruleRow.verdict === "unknown" ? "?" : "")))
      color: ruleRow.hit ? ruleRow.theme.accent : ruleRow.theme.faint
    }

    Item {
      implicitWidth: Style.space(18)
      implicitHeight: Style.space(18)

      InlineAction {
        theme: ruleRow.theme
        visible: ruleRow.own && ruleRow.hasCursor
        anchors.fill: parent
        iconText: "×"
        tooltipText: "Delete · x"
        foreground: ruleRow.theme.foreground
        hoverColor: ruleRow.theme.urgent
        size: Style.space(18)
        onClicked: if (ruleRow.rule) ruleRow.panel.service.removeRule(ruleRow.rule.index)
      }
    }
  }

  PanelToolTip {
    visible: ruleRow.hovered && ((ruleRow.rule && ruleRow.rule.inactive) || ruleRow.verdict === "unknown")
    text: ruleRow.rule && ruleRow.rule.inactive ? "This subscription has no target named " + ruleRow.rule.target
      : "Depends on a DNS answer, a rule set, or the app making the connection"
    fontFamily: ruleRow.theme.fontFamily
  }
}
