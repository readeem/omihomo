import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import "Model.js" as Model

// State for the Omihomo panel, split exactly the way ADR-0001 splits the
// plugin: the CLI at `cliPath` owns disk and systemd, and everything live —
// proxies, selection, latency, traffic, connections — is a direct call to
// mihomo's external controller.
//
// Polling is scoped to what the user can actually see. `panelOpen` gates the
// proxy/rule/subscription reads and the traffic stream; `connectionsOpen`
// gates the connections poll. The bar icon only needs `omihomo status`, which
// runs on the shared refresh timer whether the panel is open or not.
Item {
  id: root

  property var settings: ({})
  property string cliPath: ""
  property bool panelOpen: false
  property bool connectionsOpen: false

  // ---- core status, from `omihomo status` (always exits 0) ----------------
  property string coreState: "unknown"
  property string coreDetail: ""
  property string activeSubscription: ""
  property string configuredPrimaryGroup: ""
  property bool tunEnabled: false
  property bool autostartEnabled: false
  property double startedMs: 0

  // Confirmed state keeps following status reads. While an action is settling,
  // these desired values override it for rendering so an older read cannot
  // repaint over the user's click. -1 means there is no pending boolean.
  property int _desiredCoreRunning: -1
  property int _desiredTunEnabled: -1
  property int _desiredAutostartEnabled: -1

  readonly property bool installed: coreState !== "not-installed" && coreState !== "unknown"
  readonly property bool coreRunning: coreState === "on" || coreState === "degraded"
  readonly property bool coreActive: _desiredCoreRunning === -1 ? coreRunning : _desiredCoreRunning === 1
  readonly property bool tunActive: _desiredTunEnabled === -1 ? tunEnabled : _desiredTunEnabled === 1
  readonly property bool autostartActive: _desiredAutostartEnabled === -1
    ? autostartEnabled : _desiredAutostartEnabled === 1
  readonly property bool apiReady: coreRunning && apiAddress !== ""

  // ---- controller ---------------------------------------------------------
  property string apiAddress: ""
  property string apiSecret: ""
  property int mixedPort: 0
  property string mode: ""
  property string _desiredMode: ""
  readonly property string effectiveMode: _desiredMode !== "" ? _desiredMode : mode

  // ---- data ---------------------------------------------------------------
  property var subscriptions: []
  property var rules: []               // ours, from `omihomo rule list`
  property var subscriptionRules: []   // the subscription's, read-only
  property var groups: []
  property var configEntries: ({})     // config name -> `GET /proxies` entry
  property var connections: []
  property double connectionsDownload: 0
  property double connectionsUpload: 0

  property double downloadRate: 0
  property double uploadRate: 0
  property string egressIp: ""
  property int egressLatency: 0
  property bool traceFailed: false

  property string testingConfig: ""
  property string testingGroup: ""
  property string pendingConfig: ""
  property string pendingSubscription: ""

  property string actionStatus: ""
  property string lastError: ""
  property int lastErrorCode: 0
  property string _coreAction: ""
  property string _overrideAction: ""

  readonly property string primaryGroup: Model.primaryGroupName(groups, configuredPrimaryGroup)
  readonly property var primaryGroupEntry: Model.groupByName(groups, primaryGroup)
  readonly property string currentConfig: primaryGroupEntry ? primaryGroupEntry.now : ""
  readonly property bool busy: coreCmd.running || subActionCmd.running || overrideCmd.running || apiActionCmd.running

  readonly property int refreshIntervalSec: {
    var n = parseInt(String(setting("refreshIntervalSec", 10)), 10)
    if (!isFinite(n)) n = 10
    return Math.max(2, Math.min(3600, n))
  }

  signal configChanged()

  function setting(name, fallback) {
    var value = settings ? settings[name] : undefined
    return value === undefined || value === null ? fallback : value
  }

  // ---- plumbing -----------------------------------------------------------

  function cli(args) {
    return [cliPath].concat(args)
  }

  function apiArgs(method, path, body) {
    var args = ["curl", "-fsS", "--max-time", "6"]
    if (method !== "GET") args.push("-X", method)
    if (apiSecret !== "") args.push("-H", "Authorization: Bearer " + apiSecret)
    if (body !== undefined && body !== "") args.push("-H", "Content-Type: application/json", "--data", body)
    args.push("http://" + apiAddress + path)
    return args
  }

  function reportError(code, stderr) {
    var error = Model.cliError(stderr, code)
    lastError = error.message
    lastErrorCode = error.code
    actionStatus = ""
    statusClearTimer.restart()
  }

  function reportDone(message) {
    lastError = ""
    lastErrorCode = 0
    actionStatus = message || ""
    if (actionStatus !== "") statusClearTimer.restart()
  }

  function clearMessages() {
    actionStatus = ""
    lastError = ""
    lastErrorCode = 0
  }

  // ---- reads --------------------------------------------------------------

  function refresh() {
    if (cliPath === "") return
    statusCmd.launch(cli(["status"]))
    if (!panelOpen) return
    subsCmd.launch(cli(["sub", "list"]))
    rulesCmd.launch(cli(["rule", "list"]))
  }

  function refreshLive() {
    if (!apiReady) return
    proxiesCmd.launch(apiArgs("GET", "/proxies"))
    configsCmd.launch(apiArgs("GET", "/configs"))
    apiRulesCmd.launch(apiArgs("GET", "/rules"))
  }

  function refreshConnections() {
    if (!apiReady || !connectionsOpen) return
    connectionsCmd.launch(apiArgs("GET", "/connections"))
  }

  // IP and latency are the only fields that cost a network round trip, so they
  // are event-driven: panel open, config or group change, or a click.
  function refreshTrace() {
    if (!coreRunning || traceCmd.running) return
    var args = ["curl", "-fsS", "--max-time", "8", "-w", "\n%{time_total}"]
    if (mixedPort > 0) args.push("-x", "http://127.0.0.1:" + mixedPort)
    args.push("https://cloudflare.com/cdn-cgi/trace")
    traceCmd.launch(args)
  }

  function applyStatus(raw) {
    var status = Model.parseStatus(raw)
    var wasReady = apiReady
    var confirmedCoreRunning = status.state === "on" || status.state === "degraded"
    coreState = status.state
    coreDetail = status.detail
    activeSubscription = status.activeSubscription
    configuredPrimaryGroup = status.primaryGroup
    tunEnabled = status.tunEnabled
    autostartEnabled = status.autostartEnabled
    startedMs = status.startedMs
    if (_desiredCoreRunning !== -1 && confirmedCoreRunning === (_desiredCoreRunning === 1))
      _desiredCoreRunning = -1
    if (_desiredTunEnabled !== -1 && tunEnabled === (_desiredTunEnabled === 1))
      _desiredTunEnabled = -1
    if (_desiredAutostartEnabled !== -1 && autostartEnabled === (_desiredAutostartEnabled === 1))
      _desiredAutostartEnabled = -1
    if (!coreRunning) {
      downloadRate = 0
      uploadRate = 0
      groups = []
      configEntries = ({})
      connections = []
    }
    if (installed && apiAddress === "" && !apiInfoCmd.running) apiInfoCmd.launch(cli(["api-info"]))
    if (panelOpen && apiReady) {
      refreshLive()
      if (!wasReady) refreshTrace()
    }
  }

  function applyConfigs(raw) {
    var parsed = Model.parseConfigs(raw)
    mode = parsed.mode
    mixedPort = parsed.mixedPort > 0 ? parsed.mixedPort : parsed.port
    if (_desiredMode !== "" && mode === _desiredMode) _desiredMode = ""
  }

  // ---- writes -------------------------------------------------------------

  function runCore(args, message, action) {
    if (coreCmd.running) return false
    _coreAction = action || ""
    reportDone(message)
    if (coreCmd.launch(cli(args))) return true
    _coreAction = ""
    return false
  }

  function toggleCore() {
    if (!installed || coreCmd.running) return
    var desired = coreActive ? 0 : 1
    _desiredCoreRunning = desired
    if (!runCore(desired === 1 ? ["core", "start"] : ["core", "stop"],
      desired === 1 ? "Starting mihomo…" : "Stopping mihomo…", "core"))
      _desiredCoreRunning = -1
  }

  function repairCore() {
    runCore(["core", "repair"], "Repairing capabilities…", "repair")
  }

  // Autostart is systemd's `enable`, so it goes through the CLI like the rest
  // of the unit's lifecycle; the next status read reports what stuck.
  function toggleAutostart() {
    if (!installed || coreCmd.running) return
    var desired = autostartActive ? 0 : 1
    _desiredAutostartEnabled = desired
    if (!runCore(["core", "autostart", desired === 1 ? "on" : "off"],
      desired === 1 ? "Enabling autostart…" : "Disabling autostart…", "autostart"))
      _desiredAutostartEnabled = -1
  }

  function setMode(next) {
    if (overrideCmd.running || next === "" || next === effectiveMode) return
    reportDone("")
    _desiredMode = next
    _overrideAction = "mode"
    if (!overrideCmd.launch(cli(["set", "mode", next]))) {
      _desiredMode = ""
      _overrideAction = ""
    }
  }

  function toggleTun() {
    if (overrideCmd.running) return
    var desired = tunActive ? 0 : 1
    reportDone(desired === 1 ? "Enabling TUN…" : "")
    _desiredTunEnabled = desired
    _overrideAction = "tun"
    if (!overrideCmd.launch(cli(["set", "tun", desired === 1 ? "on" : "off"]))) {
      _desiredTunEnabled = -1
      _overrideAction = ""
    }
  }

  function setPrimaryGroup(name) {
    if (overrideCmd.running || name === "") return
    reportDone("")
    _overrideAction = "group"
    if (!overrideCmd.launch(cli(["set", "group", name]))) _overrideAction = ""
  }

  function addRule(type, value, target) {
    if (overrideCmd.running) return
    reportDone("")
    _overrideAction = "rule"
    if (!overrideCmd.launch(cli(["rule", "add", type, value, target]))) _overrideAction = ""
  }

  function removeRule(index) {
    if (overrideCmd.running || index <= 0) return
    reportDone("")
    _overrideAction = "rule"
    if (!overrideCmd.launch(cli(["rule", "remove", String(index)]))) _overrideAction = ""
  }

  // The subscription names itself from its own headers, so there is nothing to
  // mark pending: the name only exists once the fetch has come back.
  function addSubscription(url) {
    if (subActionCmd.running) return
    reportDone("Fetching subscription…")
    subActionCmd.launch(cli(["sub", "add", url]))
  }

  function updateSubscription(name) {
    if (subActionCmd.running || name === "") return
    pendingSubscription = name
    reportDone("Updating " + name + "…")
    subActionCmd.launch(cli(["sub", "update", name]))
  }

  function activateSubscription(name) {
    if (subActionCmd.running || name === "") return
    pendingSubscription = name
    reportDone("Activating " + name + "…")
    subActionCmd.launch(cli(["sub", "activate", name]))
  }

  function removeSubscription(name) {
    if (subActionCmd.running || name === "") return
    pendingSubscription = name
    reportDone("")
    subActionCmd.launch(cli(["sub", "remove", name]))
  }

  // Selection is a direct API call: it is live state, not something the CLI
  // persists (mihomo's `store-selected` does that for us).
  function selectConfig(group, config) {
    if (!apiReady || apiActionCmd.running || group === "" || config === "") return
    pendingConfig = config
    reportDone("")
    apiActionCmd.launch(apiArgs("PUT", "/proxies/" + encodeURIComponent(group),
      JSON.stringify({ name: config })))
  }

  function testConfig(name) {
    if (!apiReady || delayCmd.running || name === "") return
    testingConfig = name
    delayCmd.launch(apiArgs("GET", "/proxies/" + encodeURIComponent(name)
      + "/delay?timeout=5000&url=" + encodeURIComponent("http://www.gstatic.com/generate_204")))
  }

  function testGroup(name) {
    if (!apiReady || groupDelayCmd.running || name === "") return
    testingGroup = name
    groupDelayCmd.launch(apiArgs("GET", "/group/" + encodeURIComponent(name)
      + "/delay?timeout=5000&url=" + encodeURIComponent("http://www.gstatic.com/generate_204")))
  }

  function closeConnection(id) {
    if (!apiReady || id === "") return
    apiActionCmd.launch(apiArgs("DELETE", "/connections/" + encodeURIComponent(id)))
  }

  function closeAllConnections() {
    if (!apiReady) return
    apiActionCmd.launch(apiArgs("DELETE", "/connections"))
  }

  // ---- processes ----------------------------------------------------------

  // Every read and write is one short-lived process; `launch` refuses to
  // overlap a call with itself, which is what keeps a slow curl from queueing
  // a second copy behind it on the next tick.
  component Cmd: Process {
    id: cmd
    property string outText: ""
    property string errText: ""
    signal finished(int code, string out, string err)

    running: false
    command: []
    stdout: StdioCollector { waitForEnd: true; onStreamFinished: cmd.outText = text }
    stderr: StdioCollector { waitForEnd: true; onStreamFinished: cmd.errText = text }
    onExited: function(exitCode) { cmd.finished(exitCode, cmd.outText, cmd.errText) }

    function launch(args) {
      if (running || !args || args.length === 0 || args[0] === "") return false
      outText = ""
      errText = ""
      command = args
      running = true
      return true
    }
  }

  Cmd {
    id: statusCmd
    onFinished: function(code, out) { root.applyStatus(out) }
  }

  Cmd {
    id: apiInfoCmd
    onFinished: function(code, out) {
      if (code !== 0) return
      var info = Model.parseApiInfo(out)
      root.apiAddress = info.address
      root.apiSecret = info.secret
      if (root.panelOpen && root.apiReady) root.refreshLive()
    }
  }

  Cmd {
    id: subsCmd
    onFinished: function(code, out) { if (code === 0) root.subscriptions = Model.parseSubscriptions(out) }
  }

  Cmd {
    id: rulesCmd
    onFinished: function(code, out) {
      if (code !== 0) return
      root.rules = Model.parseRules(out)
      if (root.apiReady) apiRulesCmd.launch(root.apiArgs("GET", "/rules"))
    }
  }

  Cmd {
    id: apiRulesCmd
    onFinished: function(code, out) {
      root.subscriptionRules = code === 0 ? Model.stripOwnRules(Model.parseApiRules(out), root.rules) : []
    }
  }

  Cmd {
    id: proxiesCmd
    onFinished: function(code, out) {
      if (code !== 0) return
      var parsed = Model.parseProxies(out)
      root.groups = parsed.groups
      root.configEntries = parsed.configs
      root.pendingConfig = ""
    }
  }

  Cmd {
    id: configsCmd
    onFinished: function(code, out) {
      if (code !== 0) return
      root.applyConfigs(out)
    }
  }

  Cmd {
    id: connectionsCmd
    onFinished: function(code, out) {
      if (code !== 0) return
      var parsed = Model.parseConnections(out, Date.now())
      root.connections = parsed.items
      root.connectionsDownload = parsed.downloadTotal
      root.connectionsUpload = parsed.uploadTotal
    }
  }

  Cmd {
    id: traceCmd
    onFinished: function(code, out) {
      if (code !== 0) {
        // Keep the last known values; the panel dims them instead of blanking.
        root.traceFailed = true
        return
      }
      var trace = Model.parseTrace(out)
      root.traceFailed = false
      root.egressIp = trace.ip
      root.egressLatency = trace.latency
    }
  }

  Cmd {
    id: delayCmd
    onFinished: function(code, out) {
      var name = root.testingConfig
      root.testingConfig = ""
      if (code !== 0 || name === "") return
      var delay = Model.parseDelay(out)
      if (delay === null) return
      root.applyDelay(name, delay)
    }
  }

  Cmd {
    id: groupDelayCmd
    onFinished: function(code, out) {
      root.testingGroup = ""
      if (code !== 0) return
      var delays = Model.parseGroupDelay(out)
      for (var name in delays) root.applyDelay(name, delays[name])
    }
  }

  Cmd {
    id: coreCmd
    onFinished: function(code, out, err) {
      var action = root._coreAction
      root._coreAction = ""
      if (code !== 0) {
        if (action === "core") root._desiredCoreRunning = -1
        else if (action === "autostart") root._desiredAutostartEnabled = -1
        root.reportError(code, err)
      }
      else root.reportDone("")
      delayedRefresh.restart()
    }
  }

  Cmd {
    id: subActionCmd
    onFinished: function(code, out, err) {
      root.pendingSubscription = ""
      if (code !== 0) root.reportError(code, err)
      else root.reportDone("")
      delayedRefresh.restart()
    }
  }

  Cmd {
    id: overrideCmd
    onFinished: function(code, out, err) {
      var action = root._overrideAction
      root._overrideAction = ""
      if (code !== 0) {
        if (action === "mode") root._desiredMode = ""
        else if (action === "tun") root._desiredTunEnabled = -1
        root.reportError(code, err)
      }
      else root.reportDone("")
      delayedRefresh.restart()
    }
  }

  Cmd {
    id: apiActionCmd
    onFinished: function(code, out, err) {
      // Only a selection changes the egress path, so only a selection is
      // allowed to spend the panel's one event-driven trace call.
      var wasSelection = root.pendingConfig !== ""
      root.pendingConfig = ""
      if (code !== 0) root.reportError(12, err)
      else {
        root.reportDone("")
        if (wasSelection) root.configChanged()
      }
      if (root.connectionsOpen) root.refreshConnections()
      if (root.apiReady) proxiesCmd.launch(root.apiArgs("GET", "/proxies"))
    }
  }

  // Record a fresh delay on the config entry so the list and the parameters
  // view read from one place.
  function applyDelay(name, delay) {
    var entry = configEntries[name]
    if (!entry) return
    if (!entry.history || typeof entry.history.length !== "number") entry.history = []
    entry.history.push({ delay: delay })
    entry.alive = delay > 0
    configEntriesChanged()
  }

  // `/traffic` is a long-lived stream, so it runs only while the panel is on
  // screen and dies with it rather than costing a socket all session.
  Process {
    id: trafficProcess
    running: root.panelOpen && root.apiReady && root.apiAddress !== ""
    // Not apiArgs(): that carries a --max-time, which would sever the stream
    // on a timer instead of keeping it open for as long as the panel is.
    command: ["curl", "-fsS", "-N", "-H", "Authorization: Bearer " + root.apiSecret,
      "http://" + root.apiAddress + "/traffic"]
    stdout: SplitParser {
      onRead: function(line) {
        var traffic = Model.parseTraffic(line)
        if (!traffic) return
        root.downloadRate = traffic.down
        root.uploadRate = traffic.up
      }
    }
  }

  Timer {
    id: refreshTimer
    interval: root.refreshIntervalSec * 1000
    repeat: true
    running: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  Timer {
    id: connectionsTimer
    interval: 2000
    repeat: true
    running: root.connectionsOpen && root.apiReady
    triggeredOnStart: true
    onTriggered: root.refreshConnections()
  }

  // A write lands on disk before the state that reflects it, so re-read a beat
  // after every action rather than trusting the command's own output.
  Timer {
    id: delayedRefresh
    interval: 500
    onTriggered: {
      root.refresh()
      root.refreshLive()
    }
  }

  Timer {
    id: statusClearTimer
    interval: 4000
    onTriggered: root.actionStatus = ""
  }
}
