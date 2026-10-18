import QtQuick
import Quickshell
import Quickshell.Io
import "../Model.js" as Model

// Emoji in names are drawn from images, not font glyphs. Qt 6.11 loses colour
// glyphs once the panel's window has been hidden and shown again, so a flag
// drawn as text is there on the first open and blank on every one after it.
// Each emoji is rendered once by pango, outside Qt, into the cache; without
// pango-view the panel stays on its glyphs.
QtObject {
  id: emoji

  readonly property string dir: (Quickshell.env("XDG_CACHE_HOME") || Quickshell.env("HOME") + "/.cache") + "/omihomo/emoji"
  property var images: ({})
  property var _requested: ({})
  property var _queue: []

  function image(e) {
    return images[e] || ""
  }

  function request(e) {
    if (!e || _requested[e]) return
    _requested[e] = true
    _queue.push(e)
    Qt.callLater(render)
  }

  function render() {
    if (process.running || _queue.length === 0) return
    var args = ["bash", "-c", script, "omihomo-emoji", dir]
    for (var i = 0; i < _queue.length; i++) args.push(Model.emojiKey(_queue[i]), _queue[i])
    _queue = []
    process.command = args
    process.running = true
  }

  // Arguments: the cache directory, then key and emoji pairs. It prints the
  // keys whose images exist. An image is written beside its final name and
  // moved into place, so a render cut short never leaves a truncated file.
  readonly property string script: `
    dir=$1; shift
    mkdir -p -- "$dir" || exit 1
    while (( $# >= 2 )); do
      file=$dir/$1.png
      if [[ ! -s $file ]]; then
        pango-view --no-display --font="emoji 48" --background=transparent --margin=0 \\
          --text="$2" -o "$file.$$.png" >/dev/null 2>&1 && mv -f -- "$file.$$.png" "$file"
        rm -f -- "$file.$$.png"
      fi
      [[ -s $file ]] && printf "%s\\n" "$1"
      shift 2
    done
    exit 0
  `

  property Process process: Process {
    stdout: StdioCollector { id: out }
    onExited: {
      var ready = String(out.text || "").split("\n")
      var images = Object.assign({}, emoji.images)
      for (var e in emoji._requested) {
        var key = Model.emojiKey(e)
        if (ready.indexOf(key) !== -1) images[e] = "file://" + emoji.dir + "/" + key + ".png"
      }
      emoji.images = images
      emoji.render()
    }
  }
}
