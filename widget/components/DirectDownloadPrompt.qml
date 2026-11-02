import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Commons as Commons

FocusScope {
  id: root
  objectName: "directDownloadPrompt"
  required property Theme theme
  property var download: null
  readonly property color foreground: Commons.Color.popups.text
  readonly property color surface: Qt.rgba(theme.surface.r, theme.surface.g, theme.surface.b, 1)
  property bool allowSelected: false
  property bool responding: false
  signal decided(bool allow)

  visible: download !== null
  onDownloadChanged: {
    allowSelected = false
    responding = false
  }

  function respond(allow) {
    if (responding) return
    responding = true
    decided(allow)
  }

  Keys.priority: Keys.BeforeItem
  Keys.onPressed: function(event) {
    if (event.key === Qt.Key_Escape || event.text === "n") respond(false)
    else if (event.text === "y") respond(true)
    else if (event.key === Qt.Key_Tab || event.key === Qt.Key_Backtab
      || event.key === Qt.Key_Left || event.key === Qt.Key_Right) allowSelected = !allowSelected
    else if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter || event.key === Qt.Key_Space) respond(allowSelected)
    event.accepted = true
  }

  Rectangle {
    anchors.fill: parent
    color: Util.alpha(root.theme.surface, 0.85)
    MouseArea { anchors.fill: parent }
  }

  Rectangle {
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.bottom: parent.bottom
    anchors.margins: Style.space(14)
    implicitHeight: content.implicitHeight + Style.space(28)
    radius: root.theme.cardRadius
    color: root.surface
    border.color: root.theme.urgent
    border.width: 1

    ColumnLayout {
      id: content
      anchors.fill: parent
      anchors.margins: Style.space(14)
      spacing: Style.space(12)

      PanelText {
        theme: root.theme
        Layout.fillWidth: true
        text: "Download without your proxy?"
        font.bold: true
        font.pixelSize: Style.font.subtitle
        color: root.theme.urgent
        wrapMode: Text.WordWrap
      }
      PanelText {
        theme: root.theme
        Layout.fillWidth: true
        text: root.download ? root.download.label : ""
        color: root.foreground
        wrapMode: Text.WrapAnywhere
      }
      PanelText {
        theme: root.theme
        Layout.fillWidth: true
        text: "Your proxy could not connect. A direct download exposes your IP to the server and the destination to your network provider. HTTPS still protects the contents."
        wrapMode: Text.WordWrap
        color: root.foreground
      }
      PanelText {
        theme: root.theme
        Layout.fillWidth: true
        text: "Allow only this download. Permission expires after use."
        wrapMode: Text.WordWrap
        color: root.foreground
      }
      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(10)
        SmallButton {
          theme: root.theme
          foreground: root.foreground
          text: "Cancel · esc"
          hasCursor: !root.allowSelected
          enabled: !root.responding
          onClicked: root.respond(false)
        }
        Item { Layout.fillWidth: true }
        SmallButton {
          theme: root.theme
          foreground: root.foreground
          text: "Allow once · y"
          danger: true
          hasCursor: root.allowSelected
          enabled: !root.responding
          onClicked: root.respond(true)
        }
      }
    }
  }
}
