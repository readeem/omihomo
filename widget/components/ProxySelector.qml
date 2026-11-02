import QtQuick
import QtQuick.Layouts
import qs.Commons

ColumnLayout {
  id: selector

  required property var panel
  required property string section
  required property var choices
  required property string value
  readonly property Theme theme: panel.theme
  property bool picking: false
  readonly property alias field: search
  readonly property alias list: options
  readonly property string label: {
    var choice = choices.find(function(choice) { return choice.value === value })
    return choice ? choice.label : value
  }
  readonly property var filtered: {
    var needle = search.text.trim().toLowerCase()
    return choices.filter(function(choice) { return choice.label.toLowerCase().indexOf(needle) !== -1 })
  }

  signal selected(string destination)

  function openPicker() {
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
    cancel()
    selected(choice.value)
  }

  spacing: Style.space(6)

  SmallButton {
    theme: selector.theme
    Layout.fillWidth: true
    text: "→ " + selector.label + " ▾"
    hasCursor: selector.panel.at(selector.section)
    onHovered: function(on) { if (on) selector.panel.focusRow(selector.section) }
    onClicked: selector.picking ? selector.cancel() : selector.openPicker()
  }

  PanelField {
    id: search
    panel: selector.panel
    theme: selector.theme
    visible: selector.picking
    Layout.fillWidth: true
    placeholderText: "Filter destinations"
    onAccepted: selector.pick(0)
    onActiveFocusChanged: selector.panel.fieldFocusChanged(search, activeFocus)
    Keys.onEscapePressed: selector.cancel()
    Keys.onDownPressed: {
      selector.panel.endEdit()
      selector.panel.focusRow("destination", 0)
    }
  }

  ScrollList {
    id: options
    theme: selector.theme
    visible: selector.picking
    Layout.fillWidth: true
    Layout.preferredHeight: Math.min(contentHeight, Style.space(160))
    spacing: Style.space(1)
    model: selector.filtered

    delegate: NavRow {
      id: option
      required property var modelData
      required property int index
      panel: selector.panel
      section: "destination"
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
    visible: selector.picking && selector.filtered.length === 0
    text: "No matching destinations"
    color: selector.theme.dim
  }
}
