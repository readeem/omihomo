import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"
import "../Model.js" as Model

NavRow {
  id: settingRow

  property string title: ""
  property string subtitle: ""
  property string keyHint: ""
  property bool urgentSubtitle: false
  property bool checked: false
  property bool modeControl: false
  // A row with a tag is an action, so it shows the tag instead of a switch.
  property string tag: ""
  property color tagTint: theme.dim
  property bool tagFilled: false

  implicitHeight: settingColumn.implicitHeight + Style.space(14)
  onActivated: if (settingRow.modeControl) panel.cycleMode(1)

  RowLayout {
    anchors.fill: parent
    anchors.leftMargin: Style.space(8)
    anchors.rightMargin: Style.space(6)
    spacing: Style.space(10)

    ColumnLayout {
      id: settingColumn
      Layout.fillWidth: true
      spacing: Style.space(2)

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(6)

        PanelText {
          theme: settingRow.theme
          Layout.fillWidth: true
          text: settingRow.title
          color: settingRow.theme.foreground
          font.pixelSize: Style.font.body
        }

        Kbd { theme: settingRow.theme; text: settingRow.keyHint; visible: text !== "" }
      }

      PanelText {
        theme: settingRow.theme
        Layout.fillWidth: true
        text: settingRow.subtitle
        color: settingRow.urgentSubtitle ? settingRow.theme.urgent : settingRow.theme.faint
        wrapMode: Text.WordWrap
      }
    }

    Tag {
      theme: settingRow.theme
      visible: text !== ""
      text: settingRow.tag
      tint: settingRow.tagTint
      strong: settingRow.tagFilled
      filled: settingRow.tagFilled
    }

    PanelSwitch {
      theme: settingRow.theme
      visible: !settingRow.modeControl && settingRow.tag === ""
      interactive: false
      checked: settingRow.checked
    }

    Rectangle {
      visible: settingRow.modeControl
      implicitWidth: modes.implicitWidth + 2
      implicitHeight: Style.space(24)
      radius: settingRow.theme.controlRadius
      color: "transparent"
      border.width: 1
      border.color: settingRow.theme.outline

      Row {
        id: modes
        anchors.fill: parent
        anchors.margins: 1

        Repeater {
          model: Model.MODES

          Item {
            id: segment
            required property var modelData
            required property int index
            readonly property bool on: settingRow.panel.service.effectiveMode === modelData

            implicitWidth: segmentText.implicitWidth + Style.space(18)
            height: modes.height

            Rectangle {
              visible: segment.on
              anchors.fill: parent
              radius: Math.max(0, settingRow.theme.controlRadius - 1)
              color: settingRow.theme.foreground
            }

            Rectangle {
              visible: segment.on
              anchors.fill: parent
              anchors.leftMargin: segment.index === 0 ? parent.width / 2 : 0
              anchors.rightMargin: segment.index === Model.MODES.length - 1 ? parent.width / 2 : 0
              color: settingRow.theme.foreground
            }

            PanelText {
              id: segmentText
              theme: settingRow.theme
              anchors.centerIn: parent
              text: String(segment.modelData)
              color: segment.on ? settingRow.theme.surface : (segmentMouse.containsMouse ? settingRow.theme.foreground : settingRow.theme.dim)
            }

            MouseArea {
              id: segmentMouse
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onClicked: settingRow.panel.service.setMode(String(segment.modelData))
            }
          }
        }
      }
    }
  }
}
