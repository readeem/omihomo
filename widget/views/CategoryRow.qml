import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"

NavRow {
  id: row
  property var entry: ({})
  readonly property var category: entry.category || null
  readonly property int unreachable: category ? category.rules.filter(function(r) { return r.unreachable }).length : 0
  section: "category"
  rowIndex: entry.i === undefined ? -1 : entry.i
  implicitHeight: content.implicitHeight + Style.space(12)
  onActivated: if (category) panel.expandCategory(category.id)

  ColumnLayout {
    id: content
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    anchors.margins: Style.space(4)
    spacing: Style.space(4)

    RowLayout {
      Layout.fillWidth: true
      spacing: Style.space(6)
      Chevron {
        theme: row.theme
        color: row.theme.dim
        expanded: row.category && row.panel.expandedCategories[row.category.id] === true
      }
      PanelText {
        theme: row.theme
        Layout.fillWidth: true
        text: row.category ? row.category.name : ""
        font.bold: true
        elide: Text.ElideRight
        color: row.category && row.category.disabled ? row.theme.dim : row.theme.foreground
      }
      PanelText {
        theme: row.theme
        text: row.category ? String(row.category.rules.length) : ""
        color: row.theme.faint
      }
      RuleToggle {
        objectName: row.category ? "categoryToggle-" + row.category.id : ""
        theme: row.theme
        checked: row.category && !row.category.disabled
        busy: row.panel.service.ruleSaving
        enabled: row.panel.editingRule === 0 && row.panel.categoryEditing === ""
        onToggled: if (row.category) row.panel.toggleCategory(row.category)
      }
    }

    RowLayout {
      Layout.fillWidth: true
      spacing: Style.space(4)
      PanelText {
        theme: row.theme
        visible: row.category && !row.category.own
        Layout.fillWidth: true
        text: "Read-only · fixed order" + (row.category && row.category.disabled ? " · off" : "")
        color: row.theme.faint
      }
      NameButton {
        objectName: row.category ? "categoryTarget-" + row.category.id : ""
        theme: row.theme
        visible: row.category && row.category.own
        label: row.category ? row.panel.categorySummary(row.category) + " ▾" : ""
        Layout.maximumWidth: row.width * 0.48
        enabled: row.category && row.category.rules.some(function(r) { return r.type !== "SUB-RULE" })
          && !row.panel.service.ruleSaving && row.panel.editingRule === 0 && row.panel.categoryEditing === ""
        onClicked: row.panel.retargetCategory(row.category)
      }
      Item { Layout.fillWidth: true; visible: row.category && row.category.own }
      InlineAction {
        theme: row.theme
        visible: row.category && row.category.own
        objectName: row.category ? "categoryRename-" + row.category.id : ""
        iconText: "✎"
        tooltipText: "Rename category"
        onClicked: row.panel.editCategory(row.category.id)
      }
      Repeater {
        model: row.category && row.category.own ? [-1, 1] : []
        InlineAction {
          required property int modelData
          theme: row.theme
          iconText: modelData < 0 ? "↑" : "↓"
          tooltipText: modelData < 0 ? "Higher priority · Ctrl+↑" : "Lower priority · Ctrl+↓"
          enabled: row.panel.editingRule === 0 && !row.panel.service.ruleSaving && row.category.top + modelData > 0
            && row.category.top + modelData <= row.panel.service.ruleDocument.length
          onClicked: row.panel.moveCategory(row.category, modelData)
        }
      }
      InlineAction {
        theme: row.theme
        visible: row.category && row.category.own
        iconText: "×"
        tooltipText: "Ungroup · keeps the rules"
        enabled: !row.panel.service.ruleSaving && row.panel.editingRule === 0
        onClicked: row.panel.service.categoryAction("remove", row.category.id)
      }
    }
    PanelText {
      theme: row.theme
      visible: row.unreachable > 0
      Layout.fillWidth: true
      wrapMode: Text.WordWrap
      text: row.unreachable + " unreachable · an earlier MATCH catches everything"
      color: row.theme.urgent
    }
  }
}
