import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import qs.Commons
import qs.Commons as Commons

FocusScope {
  id: root
  required property Theme theme
  property string title: ""
  property var messages: []
  property string acceptText: ""
  readonly property color foreground: Commons.Color.popups.text
  readonly property color surface: Qt.rgba(theme.surface.r, theme.surface.g, theme.surface.b, 1)
  property bool acceptSelected: false
  property bool responding: false
  signal decided(bool accept)

  visible: messages.length > 0
  onMessagesChanged: {
    acceptSelected = false
    responding = false
    messageScroll.contentY = 0
  }

  function respond(accept) {
    if (responding) return
    responding = true
    decided(accept)
  }

  Keys.priority: Keys.BeforeItem
  Keys.onPressed: function(event) {
    if (event.key === Qt.Key_Escape || event.text === "n") respond(false)
    else if (event.text === "y" && acceptText !== "") respond(true)
    else if (event.key === Qt.Key_Tab || event.key === Qt.Key_Backtab
      || event.key === Qt.Key_Left || event.key === Qt.Key_Right) acceptSelected = acceptText !== "" && !acceptSelected
    else if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter || event.key === Qt.Key_Space) respond(acceptSelected)
    else if (event.key === Qt.Key_Up || event.text === "k") messageScroll.scrollBy(-Style.space(32))
    else if (event.key === Qt.Key_Down || event.text === "j") messageScroll.scrollBy(Style.space(32))
    else if (event.key === Qt.Key_PageUp) messageScroll.scrollBy(-messageScroll.height)
    else if (event.key === Qt.Key_PageDown) messageScroll.scrollBy(messageScroll.height)
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
        text: root.title
        font.bold: true
        font.pixelSize: Style.font.subtitle
        color: root.theme.urgent
        wrapMode: Text.WordWrap
      }

      Flickable {
        id: messageScroll
        Layout.fillWidth: true
        Layout.preferredHeight: Math.min(messageColumn.implicitHeight, root.height / 2)
        contentHeight: messageColumn.implicitHeight
        contentWidth: width
        clip: true
        interactive: contentHeight > height
        boundsBehavior: Flickable.StopAtBounds
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        function scrollBy(offset) {
          contentY = Math.max(0, Math.min(contentY + offset, contentHeight - height))
        }

        ColumnLayout {
          id: messageColumn
          width: messageScroll.width - (messageScroll.interactive ? Style.space(10) : 0)
          spacing: Style.space(8)

          Repeater {
            model: root.messages

            PanelText {
              required property string modelData
              theme: root.theme
              Layout.fillWidth: true
              text: modelData
              color: root.foreground
              wrapMode: Text.Wrap
            }
          }
        }
      }

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(10)
        SmallButton {
          theme: root.theme
          foreground: root.foreground
          text: root.acceptText === "" ? "Close · esc" : "Cancel · esc"
          hasCursor: !root.acceptSelected
          enabled: !root.responding
          onClicked: root.respond(false)
        }
        Item { Layout.fillWidth: true }
        SmallButton {
          theme: root.theme
          foreground: root.foreground
          text: root.acceptText
          visible: text !== ""
          danger: true
          hasCursor: root.acceptSelected
          enabled: !root.responding
          onClicked: root.respond(true)
        }
      }
    }
  }
}
