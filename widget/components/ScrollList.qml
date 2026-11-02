import QtQuick
import QtQuick.Controls
import qs.Commons

ListView {
  id: scrollList

  required property Theme theme
  readonly property bool scrollable: contentHeight > height
  readonly property real rowWidth: width - (scrollable ? Style.space(10) : 0)

  property var keyedEntries: null
  property var entryKey: null
  model: stableRows

  ListModel { id: stableRows; dynamicRoles: true }

  onKeyedEntriesChanged: syncEntries()
  onEntryKeyChanged: syncEntries()

  function syncEntries() {
    if (keyedEntries === null || !entryKey) return
    var oldY = contentY
    var anchorIndex = indexAt(1, contentY + 1)
    var anchor = anchorIndex >= 0 ? itemAtIndex(anchorIndex) : null
    var anchorKey = anchorIndex >= 0 && anchorIndex < stableRows.count ? stableRows.get(anchorIndex).key : ""
    var offset = anchor ? contentY - anchor.y : 0
    var structural = false
    var occurrences = Object.create(null)
    for (var i = 0; i < keyedEntries.length; i++) {
      var base = entryKey(keyedEntries[i])
      var occurrence = occurrences[base] || 0
      occurrences[base] = occurrence + 1
      var key = base + ":" + occurrence
      if (i >= stableRows.count || stableRows.get(i).key !== key) {
        var found = -1
        for (var j = i + 1; j < stableRows.count; j++) if (stableRows.get(j).key === key) { found = j; break }
        if (found >= 0) stableRows.move(found, i, 1)
        else stableRows.insert(i, { key: key, entry: keyedEntries[i] })
        structural = true
      }
      stableRows.set(i, { key: key, entry: keyedEntries[i] })
    }
    if (stableRows.count > keyedEntries.length) {
      stableRows.remove(keyedEntries.length, stableRows.count - keyedEntries.length)
      structural = true
    }
    // Delegate heights settle after the model signals; retain the visible row and its offset.
    if (structural && anchorKey !== "") Qt.callLater(function() {
      scrollList.forceLayout()
      var at = -1
      for (var i = 0; i < stableRows.count; i++) if (stableRows.get(i).key === anchorKey) { at = i; break }
      if (at >= 0) {
        scrollList.positionViewAtIndex(at, ListView.Beginning)
        oldY = scrollList.itemAtIndex(at).y + offset
      }
      scrollList.contentY = Math.max(scrollList.originY, Math.min(oldY, scrollList.originY + Math.max(0, scrollList.contentHeight - scrollList.height)))
    })
  }

  clip: true
  boundsBehavior: Flickable.StopAtBounds
  interactive: scrollable
  ScrollBar.vertical: ScrollBar { policy: scrollList.scrollable ? ScrollBar.AlwaysOn : ScrollBar.AsNeeded }

  Rectangle {
    visible: scrollList.scrollable && !scrollList.atYBeginning
    parent: scrollList
    anchors.top: parent.top
    anchors.left: parent.left
    width: scrollList.rowWidth
    height: Style.space(18)
    z: 2
    gradient: Gradient {
      GradientStop { position: 0; color: scrollList.theme.surface }
      GradientStop { position: 1; color: Util.alpha(scrollList.theme.surface, 0) }
    }
  }

  Rectangle {
    visible: scrollList.scrollable && !scrollList.atYEnd
    parent: scrollList
    anchors.bottom: parent.bottom
    anchors.left: parent.left
    width: scrollList.rowWidth
    height: Style.space(18)
    z: 2
    gradient: Gradient {
      GradientStop { position: 0; color: Util.alpha(scrollList.theme.surface, 0) }
      GradientStop { position: 1; color: scrollList.theme.surface }
    }
  }
}
