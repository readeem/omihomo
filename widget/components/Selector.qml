import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../Model.js" as Model

Item {
  id: selector

  required property var panel
  required property string section
  required property var choices
  required property string value
  readonly property Theme theme: panel.theme
  required property Item pickerHost
  property string placeholderText: "Filter choices"
  property bool picking: false
  property string createLabel: ""
  readonly property string createName: search.text.trim()
  readonly property bool canCreate: createLabel !== "" && createName !== ""
    && !choices.some(function(choice) { return choice.label.toLowerCase() === createName.toLowerCase() })
  readonly property alias field: search
  readonly property alias list: options
  readonly property string label: {
    var choice = choices.find(function(choice) { return choice.value === value })
    return choice ? choice.label : value
  }
  readonly property var filtered: Model.filterChoices(choices, search.text).concat(canCreate
    ? [{ value: null, label: createLabel + " “" + createName + "”", description: "+" }] : [])

  signal selected(string value)
  signal created(string name)

  function openPicker() {
    if (panel.activeSelector && panel.activeSelector !== selector) panel.activeSelector.cancel()
    picking = true
    search.text = ""
    panel.cursorActive = false
    panel.beginEdit(search)
  }

  function cancel() {
    picking = false
    search.text = ""
    panel.endEdit()
    panel.focusRow(section)
  }

  function pick(index) {
    var choice = filtered[index]
    if (!choice) return
    var name = createName
    cancel()
    if (choice.value === null) created(name)
    else selected(choice.value)
  }

  implicitWidth: trigger.implicitWidth
  implicitHeight: trigger.implicitHeight

  NameButton {
    id: trigger
    anchors.fill: parent
    theme: selector.theme
    label: selector.label + " ▾"
    hasCursor: selector.panel.at(selector.section)
    onHovered: function(on) { if (on) selector.panel.focusRow(selector.section) }
    onClicked: selector.picking ? selector.cancel() : selector.openPicker()
  }

  ColumnLayout {
    parent: selector.pickerHost
    visible: selector.picking
    Layout.fillWidth: true
    spacing: Style.space(6)

    PanelField {
      id: search
      panel: selector.panel
      theme: selector.theme
      Layout.fillWidth: true
      placeholderText: selector.placeholderText
      onAccepted: selector.pick(0)
      onActiveFocusChanged: selector.panel.fieldFocusChanged(search, activeFocus)
      Keys.onEscapePressed: selector.cancel()
      Keys.onDownPressed: {
        selector.panel.endEdit()
        selector.panel.focusRow("choice", 0)
      }
    }

    ScrollList {
      id: options
      theme: selector.theme
      Layout.fillWidth: true
      Layout.preferredHeight: Math.min(contentHeight, Style.space(160))
      spacing: Style.space(1)
      model: selector.filtered

      delegate: NavRow {
        id: option
        required property var modelData
        required property int index
        panel: selector.panel
        section: "choice"
        rowIndex: index
        current: modelData.value === selector.value
        width: options.rowWidth
        implicitHeight: Style.space(28)
        onActivated: selector.pick(index)

        RowLayout {
          anchors.fill: parent
          anchors.leftMargin: Style.space(8)
          anchors.rightMargin: Style.space(8)
          spacing: Style.space(8)
          NameText {
            theme: selector.theme
            Layout.fillWidth: true
            text: option.modelData.label
            bold: option.current
          }
          PanelText {
            theme: selector.theme
            text: option.modelData.description || ""
            color: selector.theme.faint
          }
        }
      }
    }

    PanelText {
      theme: selector.theme
      visible: selector.filtered.length === 0
      text: "No matches"
      color: selector.theme.dim
    }
  }
}
