import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"

ColumnLayout {
  id: view

  required property var panel
  readonly property var service: panel.service
  readonly property Theme theme: panel.theme
  readonly property alias urlField: subUrlField
  readonly property alias list: subList

  spacing: Style.space(8)

  SectionLine {
    theme: view.theme
    Layout.fillWidth: true
    title: "SUBSCRIPTIONS"
    note: view.service.subscriptions.length + (view.service.activeSubscription !== "" ? " · active " + view.service.activeSubscription : "")
  }

  PanelText {
    theme: view.theme
    visible: view.service.subscriptions.length === 0
    Layout.fillWidth: true
    text: "No subscriptions yet. Paste a URL below."
    color: view.theme.dim
    font.pixelSize: Style.font.body
  }

  ScrollList {
    id: subList
    theme: view.theme
    Layout.preferredHeight: contentHeight
    Layout.minimumHeight: 0
    Layout.fillWidth: true
    Layout.fillHeight: true
    Layout.maximumHeight: contentHeight
    spacing: Style.space(7)
    model: view.service.subscriptions

    delegate: SubscriptionCard {
      required property var modelData
      required property int index
      panel: view.panel
      width: subList.rowWidth
      subscription: modelData
      rowIndex: index
    }
  }

  SectionLine { theme: view.theme; Layout.fillWidth: true; title: "ADD" }

  RowLayout {
    Layout.fillWidth: true
    spacing: Style.space(6)

    PanelField {
      panel: view.panel
      id: subUrlField
      theme: view.theme
      Layout.fillWidth: true
      foreground: view.theme.foreground
      placeholderText: "Paste a subscription URL"
      hasCursor: view.panel.at("subUrl")
      verticalPadding: Style.space(4)
      onHoveredChanged: if (hovered) view.panel.focusRow("subUrl")
      onAccepted: view.panel.submitSubForm()
      onActiveFocusChanged: view.panel.fieldFocusChanged(subUrlField, activeFocus)
      Keys.onEscapePressed: view.panel.handleEscape()
    }

    SmallButton {
      theme: view.theme
      text: "+ add"
      hasCursor: view.panel.at("subSubmit")
      onHovered: function(on) { if (on) view.panel.focusRow("subSubmit") }
      onClicked: view.panel.submitSubForm()
    }
  }

  PanelText {
    theme: view.theme
    visible: view.panel.errorAt("sub:")
    Layout.fillWidth: true
    text: "▲ " + view.service.lastError
    color: view.theme.urgent
    wrapMode: Text.WordWrap
  }
  Item { Layout.fillHeight: true }
}
