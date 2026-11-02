import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"
import "../Model.js" as Model

ColumnLayout {
  id: view

  required property var panel
  readonly property var service: panel.service
  readonly property Theme theme: panel.theme
  readonly property alias typeSelector: ruleTypeSelector
  readonly property alias categorySelector: categorySelectorControl
  readonly property alias bulkSelector: categoryDestination
  readonly property alias categoryField: categoryNameField
  readonly property alias testField: ruleTestField
  readonly property alias valueField: ruleValueField
  readonly property alias selector: destination
  readonly property alias ruleList: ruleListView

  spacing: Style.space(8)

  PanelField {
    id: categoryNameField
    panel: view.panel
    theme: view.theme
    visible: view.panel.categoryEditing !== ""
    Layout.fillWidth: true
    placeholderText: "Category name"
    onActiveFocusChanged: view.panel.fieldFocusChanged(categoryNameField, activeFocus)
    onAccepted: view.panel.saveCategory()
    Keys.onEscapePressed: view.panel.handleEscape()
  }

  RowLayout {
    Layout.fillWidth: true
    spacing: Style.space(8)

    PanelText {
      theme: view.theme
      text: "test"
      color: view.theme.dim
    }

    PanelField {
      panel: view.panel
      id: ruleTestField
      theme: view.theme
      highlighted: text !== ""
      Layout.fillWidth: true
      foreground: view.theme.foreground
      placeholderText: "domain, URL, or IP address"
      hasCursor: view.panel.at("ruleTest")
      verticalPadding: Style.space(4)
      onHoveredChanged: if (hovered) view.panel.focusRow("ruleTest")
      onAccepted: view.panel.endEdit()
      onActiveFocusChanged: view.panel.fieldFocusChanged(ruleTestField, activeFocus)
      Keys.onEscapePressed: view.panel.handleEscape()
    }
  }

  RowLayout {
    Layout.fillWidth: true
    spacing: Style.space(6)

    PanelText {
      theme: view.theme
      visible: view.panel.ruleTest.hit >= 0 || view.panel.ruleTest.bypassed
      text: view.panel.ruleTest.bypassed ? "Outside VPN"
        : (view.panel.ruleTest.certain ? "→ " : "possibly → ") + view.panel.ruleHitTarget
      color: view.theme.accent
      font.pixelSize: Style.font.body
      font.bold: true
    }

    PanelText {
      theme: view.theme
      Layout.fillWidth: true
      text: view.panel.ruleTest.host === "" ? "Type a domain or an IP to see which rule catches it."
        : (view.panel.ruleTest.bypassed ? "Uses the system route · excluded by " + view.panel.ruleTest.bypassCidr
          : view.panel.ruleTest.hit < 0 ? "No rule matches."
          : "rule " + (view.panel.ruleTest.hit + 1) + " of " + view.panel.effectiveRules.length
            + (view.panel.ruleTest.unknown > 0 ? " · " + view.panel.ruleTest.unknown + " above depend on DNS, rule sets, or the app" : ""))
      color: view.theme.dim
      elide: Text.ElideRight
    }
  }
  Row {
    id: tallyBar
    visible: view.panel.ruleTally.length > 0
    Layout.fillWidth: true
    Layout.preferredHeight: Style.space(8)
    spacing: Style.space(2)

    Repeater {
      model: view.panel.ruleTally
      Rectangle {
        required property var modelData
        required property int index
        width: Math.max(Style.space(2), (tallyBar.width - tallyBar.spacing * (view.panel.ruleTally.length - 1))
          * modelData.count / view.panel.effectiveRules.length)
        height: tallyBar.height
        radius: Math.min(Style.cornerRadius, 2)
        color: modelData.target === view.panel.ruleHitTarget ? view.theme.accent
          : Util.alpha(view.theme.foreground, [0.45, 0.32, 0.22, 0.15][index] || 0.1)
      }
    }
  }

  Flow {
    visible: view.panel.ruleTally.length > 0
    Layout.fillWidth: true
    spacing: Style.space(10)

    Repeater {
      model: view.panel.ruleTally.slice(0, 6)
      PanelText {
        required property var modelData
        theme: view.theme
        text: modelData.count + " " + modelData.target
        color: modelData.target === view.panel.ruleHitTarget ? view.theme.accent : view.theme.dim
      }
    }
  }

  RowLayout {
    Layout.fillWidth: true
    spacing: Style.space(6)
    InlineAction {
      theme: view.theme
      objectName: "toggleCategories"
      Chevron {
        anchors.centerIn: parent
        color: view.theme.foreground
        doubleArrow: true
        expanded: view.panel.allCategoriesExpanded
      }
      tooltipText: view.panel.allCategoriesExpanded ? "Collapse all categories" : "Expand all categories"
      hasCursor: view.panel.at("toggleCategories")
      onHovered: function(on) { if (on) view.panel.focusRow("toggleCategories") }
      onClicked: view.panel.toggleAllCategories()
    }
    PanelText {
      theme: view.theme
      Layout.fillWidth: true
      text: "First match wins · categories run in order"
      color: view.theme.dim
      wrapMode: Text.WordWrap
    }
  }

  ScrollList {
    objectName: "ruleList"
    id: ruleListView
    theme: view.theme
    Layout.fillWidth: true
    Layout.fillHeight: true
    keyedEntries: view.panel.ruleEntries
    entryKey: function(e) { return e.kind === "category" ? "category:" + e.category.id : e.kind + ":" + (e.rule.index || e.rule.type + ":" + e.rule.value) }

    delegate: Item {
      id: ruleEntry
      required property var entry
      width: ruleListView.rowWidth
      height: entry.kind === "category" ? categoryHead.implicitHeight : ruleBody.implicitHeight

      CategoryRow {
        id: categoryHead
        panel: view.panel
        visible: ruleEntry.entry.kind === "category"
        width: parent.width
        entry: ruleEntry.entry
      }

      RuleRow {
        id: ruleBody
        panel: view.panel
        visible: ruleEntry.entry.kind !== "category"
        width: parent.width
        entry: ruleEntry.entry
      }
    }
  }

  RowLayout {
    Layout.fillWidth: true
    PanelText {
      theme: view.theme
      Layout.fillWidth: true
      text: view.panel.editingRule > 0 ? "Edit rule " + view.panel.editingRule : "New rule"
      color: view.theme.dim
    }
    PanelText { theme: view.theme; text: view.panel.ruleEnabled ? "Enabled" : "Disabled"; color: view.theme.dim }
    RuleToggle {
      objectName: "ruleEnabled"
      theme: view.theme
      checked: view.panel.ruleEnabled
      enabled: !view.service.ruleSaving
      onToggled: { view.panel.ruleEnabled = !view.panel.ruleEnabled; view.panel.openRuleForm() }
    }
  }

  PanelText {
    theme: view.theme
    Layout.fillWidth: true
    text: Model.ruleHint(view.panel.ruleType)
    color: view.theme.dim
    wrapMode: Text.WordWrap
  }

  RowLayout {
    Layout.fillWidth: true
    visible: ["GEOIP", "IP-ASN", "IP-CIDR", "IP-CIDR6", "IP-SUFFIX", "RULE-SET"].indexOf(view.panel.ruleType) !== -1
    Repeater {
      model: ["no-resolve", "src"]
      SmallButton {
        required property string modelData
        theme: view.theme
        text: modelData === "src" ? "Match source IP" : "Skip DNS lookup"
        primary: view.panel.ruleParams.indexOf(modelData) !== -1
        onClicked: {
          var flags = view.panel.ruleParams.split(" ").filter(function(p) { return p !== "" && p !== modelData })
          if (!primary) flags.push(modelData)
          view.panel.ruleParams = flags.join(" ")
        }
      }
    }
  }

  PanelField {
    panel: view.panel
    id: ruleValueField
    theme: view.theme
    Layout.fillWidth: true
    foreground: view.theme.foreground
    placeholderText: view.panel.ruleType === "MATCH" ? "No value needed" : "Rule value"
    hasCursor: view.panel.at("ruleValue")
    verticalPadding: Style.space(4)
    onHoveredChanged: if (hovered) view.panel.focusRow("ruleValue")
    onAccepted: view.panel.submitRuleForm()
    onActiveFocusChanged: view.panel.fieldFocusChanged(ruleValueField, activeFocus)
    Keys.onEscapePressed: view.panel.handleEscape()
  }

  RowLayout {
    Layout.fillWidth: true
    spacing: Style.space(8)

    Selector {
      id: ruleTypeSelector
      panel: view.panel
      section: "ruleType"
      pickerHost: pickerDock
      placeholderText: "Filter rule types"
      choices: Model.RULE_TYPES.map(function(type) {
        return { value: type, label: type, description: Model.ruleCategory(type) }
      })
      value: view.panel.ruleType
      Layout.maximumWidth: (view.width - Style.space(16)) / 3
      onSelected: function(value) {
        view.panel.ruleTypeIndex = Model.RULE_TYPES.indexOf(value)
        view.panel.ruleParams = ""
        if (value === "MATCH") view.valueField.text = ""
        view.panel.openRuleForm()
      }
    }

    Item { Layout.fillWidth: true }

    Selector {
      id: categorySelectorControl
      panel: view.panel
      section: "ruleCategory"
      pickerHost: pickerDock
      placeholderText: "Filter or create a category"
      createLabel: "Create category"
      onCreated: function(name) {
        view.panel.newRuleCategory = name
        view.panel.ruleCategory = "new"
        view.panel.openRuleForm()
      }
      choices: view.panel.categoryChoices
      value: view.panel.ruleCategory
      Layout.maximumWidth: (view.width - Style.space(16)) / 3
      onSelected: function(value) {
        view.panel.ruleCategory = value
        view.panel.openRuleForm()
      }
    }

    Selector {
      id: destination
      panel: view.panel
      section: "ruleTarget"
      pickerHost: pickerDock
      placeholderText: "Filter destinations"
      choices: view.panel.ruleTargets
      value: view.panel.ruleTarget
      Layout.maximumWidth: (view.width - Style.space(16)) / 3
      onSelected: function(value) {
        view.panel.ruleTargetIndex = view.panel.ruleTargets.findIndex(function(choice) { return choice.value === value })
        view.panel.openRuleForm()
      }
    }
  }

  Selector {
    id: categoryDestination
    visible: false
    panel: view.panel
    section: "categoryTarget"
    pickerHost: pickerDock
    placeholderText: "Target for the whole category"
    choices: Model.ruleTargets(view.panel.visibleGroups)
    value: ""
    onSelected: function(value) { view.service.categoryAction("target", view.panel.categoryTarget, value) }
  }

  ColumnLayout {
    id: pickerDock
    Layout.fillWidth: true
    visible: ruleTypeSelector.picking || destination.picking || categorySelectorControl.picking || categoryDestination.picking
  }

  PanelText {
    readonly property string message: view.panel.ruleFormValue !== "" && view.panel.ruleFormError !== ""
      ? view.panel.ruleFormError : (view.panel.errorAt("rules") ? view.service.lastError : "")
    theme: view.theme
    visible: message !== ""
    Layout.fillWidth: true
    text: "▲ " + message
    color: view.theme.urgent
    wrapMode: Text.WordWrap
  }
}
