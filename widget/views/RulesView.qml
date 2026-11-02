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
  readonly property alias typeField: typeSearch
  readonly property alias typeList: typeListView
  readonly property alias testField: ruleTestField
  readonly property alias valueField: ruleValueField
  readonly property alias selector: destination
  readonly property alias ruleList: ruleListView

  spacing: Style.space(8)

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

  PanelText {
    theme: view.theme
    Layout.fillWidth: true
    text: "First match wins · select your rule to edit"
    color: view.theme.dim
  }

  ScrollList {
    id: ruleListView
    theme: view.theme
    visible: !view.panel.typePicking
    Layout.fillWidth: true
    Layout.fillHeight: true
    model: view.panel.ruleEntries

    delegate: Item {
      id: ruleEntry
      required property var modelData
      width: ruleListView.rowWidth
      height: modelData.kind === "head" ? ruleHead.implicitHeight + Style.space(8) : ruleBody.implicitHeight

      SectionLine {
        id: ruleHead
        theme: view.theme
        visible: ruleEntry.modelData.kind === "head"
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.bottomMargin: Style.space(2)
        title: ruleEntry.modelData.title || ""
        note: ruleEntry.modelData.note || ""
      }

      RuleRow {
        id: ruleBody
        panel: view.panel
        visible: ruleEntry.modelData.kind !== "head"
        width: parent.width
        entry: ruleEntry.modelData
      }
    }
  }

  ColumnLayout {
    visible: view.panel.typePicking
    Layout.fillWidth: true
    Layout.fillHeight: true
    PanelField {
      panel: view.panel
      id: typeSearch
      theme: view.theme
      Layout.fillWidth: true
      placeholderText: "Find a rule type"
      onAccepted: view.panel.chooseRuleType(0)
      onActiveFocusChanged: view.panel.fieldFocusChanged(typeSearch, activeFocus)
      Keys.onEscapePressed: view.panel.handleEscape()
      Keys.onDownPressed: { view.panel.endEdit(); view.panel.focusRow("ruleTypeChoice", 0) }
    }
    ScrollList {
      id: typeListView
      theme: view.theme
      Layout.fillWidth: true
      Layout.fillHeight: true
      Layout.minimumHeight: Style.space(120)
      model: view.panel.visibleRuleTypes
      delegate: SmallButton {
        required property string modelData
        theme: view.theme
        text: Model.ruleCategory(modelData) + " · " + modelData
        required property int index
        hasCursor: view.panel.at("ruleTypeChoice", index)
        onClicked: view.panel.chooseRuleType(index)
      }
    }
  }

  PanelText {
    theme: view.theme
    Layout.fillWidth: true
    text: Model.ruleHint(view.panel.ruleType)
    color: view.theme.dim
    wrapMode: Text.WordWrap
  }

  Flow {
    Layout.fillWidth: true
    spacing: Style.space(4)
    visible: view.panel.ruleType === "RULE-SET" || view.panel.ruleType === "GEOSITE" || view.panel.ruleType.indexOf("GEOIP") !== -1
    Repeater {
      model: view.panel.ruleType === "RULE-SET" ? view.service.ruleSets
        : view.panel.ruleType === "GEOSITE" ? ["youtube", "google", "category-ads-all", "geolocation-!cn", "cn"]
        : ["private", "CN", "US", "DE", "GB", "RU"]
      SmallButton {
        required property string modelData
        theme: view.theme
        text: modelData
        onClicked: view.valueField.text = modelData
      }
    }
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

  ProxySelector {
    id: destination
    panel: view.panel
    section: "ruleTarget"
    choices: view.panel.ruleTargets
    value: view.panel.ruleTarget
    Layout.fillWidth: true
    heading: Component {
      Item {
        implicitHeight: typeButton.implicitHeight
        SmallButton {
          id: typeButton
          theme: view.theme
          text: view.panel.ruleType + " ▾"
          tooltipText: "Rule type · h l"
          hasCursor: view.panel.at("ruleType")
          onHovered: function(on) { if (on) view.panel.focusRow("ruleType") }
          onClicked: view.panel.openTypePicker()
        }
      }
    }
    onSelected: function(value) {
      view.panel.ruleTargetIndex = view.panel.ruleTargets.findIndex(function(choice) { return choice.value === value })
      view.panel.openRuleForm()
    }
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
