import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"

ColumnLayout {
  id: view

  required property var panel
  readonly property var service: panel.service
  readonly property Theme theme: panel.theme
  readonly property alias filterField: configFilterField
  readonly property alias list: configList
  readonly property real protocolLabelWidth: Math.ceil(Math.max(groupTypeMetrics.advanceWidth, proxyTypeMetrics.advanceWidth))
  readonly property real latencyColumnWidth: Math.max(Style.space(52),
    Math.ceil(Math.max(delayMetrics.advanceWidth, timeoutMetrics.advanceWidth)) + Style.space(12))

  spacing: Style.space(8)

  TextMetrics {
    id: groupTypeMetrics
    font.family: view.theme.fontFamily
    font.pixelSize: Style.font.bodySmall
    renderType: Text.NativeRendering
    text: "Load Balance"
  }

  TextMetrics {
    id: proxyTypeMetrics
    font: groupTypeMetrics.font
    renderType: groupTypeMetrics.renderType
    text: "Shadowsocks"
  }

  TextMetrics {
    id: delayMetrics
    font: groupTypeMetrics.font
    renderType: groupTypeMetrics.renderType
    text: "00000 ms"
  }

  TextMetrics {
    id: timeoutMetrics
    font: groupTypeMetrics.font
    renderType: groupTypeMetrics.renderType
    text: "timeout"
  }

  PanelText {
    theme: view.theme
    visible: !view.panel.liveReady || (view.service.proxiesLoaded && view.panel.visibleGroups.length === 0)
    Layout.fillWidth: true
    text: !view.panel.liveReady
      ? (view.service.activeSubscription === "" ? "Add and activate a subscription in Subs to browse its configs."
        : (view.service.coreRunning ? "Reconnecting to the core…" : "Start the proxy to browse configs."))
      : "This subscription declares no groups."
    color: view.theme.dim
    font.pixelSize: Style.font.body
    wrapMode: Text.WordWrap
  }

  Flow {
    visible: view.panel.liveReady
    Layout.fillWidth: true
    spacing: Style.space(6)

    Repeater {
      model: view.panel.liveReady ? view.panel.visibleGroups : []
      GroupChip {
        required property var modelData
        required property int index
        panel: view.panel
        group: modelData
        rowIndex: index
      }
    }
  }

  RowLayout {
    visible: view.panel.browsedGroupEntry !== null
    Layout.fillWidth: true
    Layout.topMargin: Style.space(4)
    spacing: Style.space(8)

    SectionLine {
      theme: view.theme
      Layout.fillWidth: true
      title: view.panel.browsedGroup.toUpperCase() + (view.panel.browsedGroupEntry ? " · " + view.panel.browsedGroupEntry.type.toUpperCase() : "")
      note: view.panel.browsedGroupEntry && !view.panel.browsedGroupEntry.selectable ? "picks its own config"
        : view.panel.visibleConfigs.length + (view.panel.browsedGroupEntry && view.panel.visibleConfigs.length !== view.panel.browsedGroupEntry.all.length
          ? " of " + view.panel.browsedGroupEntry.all.length : "") + " configs"
    }

    SmallButton {
      theme: view.theme
      visible: view.panel.browsedGroup !== view.service.primaryGroup
      text: "make primary"
      tooltipText: "The primary group's config is the current config · p"
      onClicked: view.service.setPrimaryGroup(view.panel.browsedGroup)
    }
  }

  PanelField {
      panel: view.panel
    id: configFilterField
    theme: view.theme
    visible: view.panel.filterOpen
    Layout.fillWidth: true
    foreground: view.theme.foreground
    placeholderText: "Filter configs"
    verticalPadding: Style.space(4)
    onAccepted: view.panel.endEdit()
    onActiveFocusChanged: view.panel.fieldFocusChanged(configFilterField, activeFocus)
    Keys.onEscapePressed: view.panel.closeFilter()
  }

  ScrollList {
    id: configList
    theme: view.theme
    Layout.fillWidth: true
    Layout.fillHeight: true
    Layout.maximumWidth: view.width
    spacing: Style.space(1)
    model: view.panel.liveReady ? view.panel.visibleConfigs : []

    delegate: ConfigRow {
      required property var modelData
      required property int index
      panel: view.panel
      width: configList.rowWidth
      configName: String(modelData)
      protocolLabelWidth: view.protocolLabelWidth
      latencyColumnWidth: view.latencyColumnWidth
      rowIndex: index
    }
  }

  PanelText {
    theme: view.theme
    visible: view.panel.errorAt("configs")
    Layout.fillWidth: true
    text: "▲ " + view.service.lastError
    color: view.theme.urgent
    wrapMode: Text.WordWrap
  }

  RowLayout {
    visible: view.panel.browsedGroupEntry !== null
    Layout.fillWidth: true
    spacing: Style.space(6)

    PanelText {
      theme: view.theme
      visible: !view.service.pingTestsRunning
      text: view.panel.testedText
      color: view.theme.dim
    }

    SmallButton {
      theme: view.theme
      visible: view.service.pingTestsRunning
      text: "cancel test"
      tooltipText: "Cancel latency test · D"
      onClicked: view.service.cancelDelayTest()
    }

    Item { Layout.fillWidth: true }

    PanelText {
      theme: view.theme
      text: "fast"
      color: view.theme.faint
    }

    Repeater {
      model: [0, 1 / 3, 2 / 3, 1]
      Rectangle {
        required property var modelData
        implicitWidth: Style.space(16)
        implicitHeight: Style.space(8)
        radius: Math.min(Style.cornerRadius, 2)
        color: view.theme.latencyFill(modelData)
      }
    }

    Rectangle {
      implicitWidth: Style.space(14)
      implicitHeight: Style.space(8)
      radius: Math.min(Style.cornerRadius, 2)
      color: view.theme.urgent
    }

    PanelText {
      theme: view.theme
      text: "slow"
      color: view.theme.faint
    }
  }
}
