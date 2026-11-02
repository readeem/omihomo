import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"
import "../Model.js" as Model

NavRow {
  id: row

  required property var owner
  required property var share
  required property int index
  readonly property bool armed: owner.armed === "share:" + share.id
  readonly property string endpoint: Model.publicEndpoint(panel.service.hosting.relay, share)

  section: "hostShare"
  rowIndex: index
  current: owner.form === "share" && owner.shareId === share.id
  implicitHeight: shareColumn.implicitHeight + Style.space(12)
  onActivated: owner.activate({ s: "hostShare", i: index })

  RowLayout {
    anchors.fill: parent
    anchors.leftMargin: Style.space(8)
    anchors.rightMargin: Style.space(6)
    spacing: Style.space(8)

    ColumnLayout {
      id: shareColumn
      Layout.fillWidth: true
      spacing: Style.space(1)

      NameText {
        theme: row.theme
        Layout.fillWidth: true
        text: row.share.label
        bold: row.share.enabled
      }

      PanelText {
        theme: row.theme
        Layout.fillWidth: true
        text: row.armed ? "x again removes it · esc cancels"
          : Model.shareSummary(row.share) + (row.share.enabled ? "" : " · off")
        color: row.armed ? row.theme.urgent : row.theme.faint
        elide: Text.ElideRight
      }
    }

    Tag {
      theme: row.theme
      visible: row.share.enabled && row.share.active > 0
      text: row.share.active + " active"
      tint: row.theme.accent
    }

    InlineAction {
      theme: row.theme
      visible: row.endpoint !== ""
      iconText: "󰆏"
      tooltipText: "Copy " + row.endpoint
      Accessible.name: "Copy public address and port of " + row.share.label
      onClicked: row.panel.copyText(row.endpoint)
    }

    PanelSwitch {
      theme: row.theme
      checked: row.share.enabled
      enabled: row.panel.service.streamsReady(["hosting"])
      Accessible.role: Accessible.CheckBox
      Accessible.name: "Expose " + row.share.label
      Accessible.checked: checked
      onToggled: row.owner.setEnabled(row.share, !row.share.enabled)
    }
  }
}
