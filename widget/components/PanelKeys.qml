import QtQuick

Item {
  id: keys
  property bool blocked: false
  property bool reorderable: false
  signal moveRequested(int dx, int dy)
  signal reorderRequested(int dy)
  signal activateRequested()
  signal closeRequested()
  signal deleteRequested()
  signal tabRequested(int direction)
  signal textKey(string text)

  focus: true
  Keys.priority: Keys.BeforeItem
  Keys.onPressed: function(event) {
    if (blocked) return
    if (event.key === Qt.Key_Escape) { closeRequested(); event.accepted = true; return }
    if (event.key === Qt.Key_Tab || event.key === Qt.Key_Backtab) {
      tabRequested(event.modifiers & Qt.ShiftModifier || event.key === Qt.Key_Backtab ? -1 : 1)
      event.accepted = true
      return
    }
    if (reorderable && event.modifiers & Qt.ControlModifier
      && (event.key === Qt.Key_Up || event.key === Qt.Key_Down)) {
      reorderRequested(event.key === Qt.Key_Up ? -1 : 1)
      event.accepted = true
      return
    }
    var dx = 0, dy = 0
    if (event.key === Qt.Key_Down || event.text === "j") dy = 1
    else if (event.key === Qt.Key_Up || event.text === "k") dy = -1
    else if (event.key === Qt.Key_Right || event.text === "l") dx = 1
    else if (event.key === Qt.Key_Left || event.text === "h") dx = -1
    if (dx || dy) { moveRequested(dx, dy); event.accepted = true; return }
    if ([Qt.Key_Return, Qt.Key_Enter, Qt.Key_Space].indexOf(event.key) !== -1) {
      activateRequested(); event.accepted = true; return
    }
    if (event.text === "x" || event.text === "X") { deleteRequested(); event.accepted = true; return }
    if (event.text && event.text.length === 1) textKey(event.text)
  }
}
