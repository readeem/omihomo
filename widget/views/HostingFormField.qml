import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"

RowLayout {
  id: row

  required property var panel
  required property var owner
  readonly property Theme theme: panel.theme
  property int index: 0
  property string label: ""
  property string placeholder: ""
  property bool secret: false
  readonly property alias field: input

  Layout.fillWidth: true
  spacing: Style.space(8)

  PanelText {
    theme: row.theme
    Layout.preferredWidth: Style.space(86)
    text: row.label
    color: row.theme.dim
    elide: Text.ElideRight
  }

  PanelField {
    id: input
    panel: row.panel
    theme: row.theme
    Layout.fillWidth: true
    foreground: row.theme.foreground
    placeholderText: row.placeholder
    echoMode: row.secret ? TextInput.Password : TextInput.Normal
    inputMethodHints: row.secret ? Qt.ImhSensitiveData | Qt.ImhNoPredictiveText | Qt.ImhNoAutoUppercase : Qt.ImhNoAutoUppercase
    hasCursor: row.panel.at("hostField", row.index)
    verticalPadding: Style.space(4)
    Accessible.name: row.label
    onHoveredChanged: if (hovered) row.panel.focusRow("hostField", row.index)
    onAccepted: row.owner.submit()
    onActiveFocusChanged: row.panel.fieldFocusChanged(input, activeFocus)
    Keys.onEscapePressed: row.panel.handleEscape()
    Keys.onDownPressed: row.owner.step(row.index, 1)
    Keys.onUpPressed: row.owner.step(row.index, -1)
  }
}
