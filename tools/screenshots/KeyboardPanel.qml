import QtQuick
import qs.Commons
import qs.Commons as Commons

// Stands in for Omarchy's layer-shell KeyboardPanel, which needs a Wayland
// compositor. It draws the same card in place, so the capture can grab it
// offscreen. Only the API that Panel.qml uses is kept.
Item {
  id: root

  property Item anchorItem: null
  property QtObject bar: null
  property var owner: null
  property int padding: Style.spacing.popupPadding
  property int contentWidth: Style.space(280)
  property int contentHeight: Style.space(200)
  property var borderSpec: Border.surfaceSpec("popups", "border", Commons.Color.popups.border, Math.max(1, Style.space(2)))
  property bool open: false
  property Item focusTarget: null
  readonly property alias card: card

  default property alias contentItem: contentHolder.children

  function fittedContentWidth(width) { return Math.round(Math.max(1, Number(width) || 1)) }
  function cappedContentHeight(height) { return Math.round(Math.max(padding * 2, Number(height) || padding * 2)) }

  width: contentWidth
  height: contentHeight
  visible: open

  onOpenChanged: if (open && focusTarget) Qt.callLater(function() { root.focusTarget.forceActiveFocus() })

  BorderSurface {
    id: card
    anchors.fill: parent
    color: Commons.Color.popups.background
    borderSpec: root.borderSpec
    padding: root.padding
    radius: Style.cornerRadius

    Item {
      id: contentHolder
      anchors.fill: parent
      anchors.leftMargin: card.contentLeftInset
      anchors.rightMargin: card.contentRightInset
      anchors.topMargin: card.contentTopInset
      anchors.bottomMargin: card.contentBottomInset
    }
  }
}
