import QtQuick
import Quickshell
import Quickshell.Io
import "Model.js" as Model

// State for the Omihomo panel. Reads use one connection to the daemon's socket
// (docs/daemon.md): requests carry an id and get one reply
// each, and the daemon pushes every change on the streams this panel
// subscribed to, whoever made it. Settings edits use the CLI to save the
// owner's files. Nothing here polls.
//
// The `status` stream is always on, since the bar icon needs it. The panel's
// streams — groups, rules, subscriptions, traffic, and connections — run only
// while the panel is open, and cost the daemon nothing otherwise.
Item {
  id: root

  // `OMIHOMO_SOCKET` points the panel at another daemon, as it does the CLI.
  property string socketPath: Quickshell.env("OMIHOMO_SOCKET") || "/run/omihomo/omihomo.sock"
  property string cliPath: "/usr/bin/omihomo"
  property bool panelOpen: false

  // The protocol this widget speaks. A daemon that greets with another one is
  // never sent a request.
  readonly property int protocolVersion: 2

  // "connecting" until the daemon greets this panel, then "ready". The rest
  // are why there is nothing to talk to:
  //   "absent"    no daemon answers on the socket: the core is not installed
  //   "refused"   the daemon serves another account
  //   "busy"      the daemon has reached its client limit; reconnect later
  //   "outdated"  the daemon speaks an older protocol than this widget
  //   "newer"     the daemon speaks a newer protocol than this widget
  property string link: "connecting"
  property string daemonVersion: ""
  readonly property bool ready: link === "ready"

  property string coreState: "unknown"
  property string coreDetail: ""
  property string activeSubscription: ""
  property string configuredPrimaryGroup: ""
  property string mode: ""
  property bool tunEnabled: false
  // Whether TUN uses the nftables-backed fast pairing. Off is the fallback that
  // needs no firewall rules, which is the repair when sing-tun cannot install
  // its nftables rules.
  property bool tunRedirect: false
  property bool tailscaleEnabled: false
  // Whether tailscaled is on this machine at all. The panel hides the row
  // rather than offering a toggle that can only fail.
  property bool tailscalePresent: false
  property bool tailnetProxy: false
  property double startedMs: 0
  // What the rule tester needs to know about TUN routing.
  property var routing: ({})

  // Confirmed state follows the status stream. While a change is in flight,
  // these desired values override it for rendering, so a push that left the
  // daemon before the change landed cannot repaint over the user's click. -1
  // means there is no pending boolean. The change's own reply clears them.
  property int _desiredCoreRunning: -1
  property int _desiredTunEnabled: -1
  property int _desiredTunRedirect: -1
  property int _desiredTailscaleEnabled: -1
  property int _desiredTailnetProxy: -1
  property string _desiredMode: ""

  // The panel is usable once the daemon is greeted and has said what it is
  // doing. A connection that drops keeps the last status, so a daemon
  // restarting under socket activation does not flash the install card.
  readonly property bool installed: coreState !== "unknown" && coreState !== "not-installed"
    && link !== "absent" && link !== "refused" && link !== "outdated" && link !== "newer"
  readonly property bool coreRunning: coreState === "on" || coreState === "degraded"
  readonly property bool coreActive: _desiredCoreRunning === -1 ? coreRunning : _desiredCoreRunning === 1
  readonly property bool tunActive: _desiredTunEnabled === -1 ? tunEnabled : _desiredTunEnabled === 1
  readonly property bool tunRedirectActive: _desiredTunRedirect === -1
    ? tunRedirect : _desiredTunRedirect === 1
  readonly property bool tailscaleActive: _desiredTailscaleEnabled === -1
    ? tailscaleEnabled : _desiredTailscaleEnabled === 1
  readonly property bool tailnetProxyActive: _desiredTailnetProxy === -1
    ? tailnetProxy : _desiredTailnetProxy === 1
  readonly property string effectiveMode: _desiredMode !== "" ? _desiredMode : mode
  // Whether live state — groups, traffic, connections — can be read now.
  readonly property bool apiReady: streamsReady(["status"]) && coreRunning
  readonly property bool groupsReady: apiReady && streamsReady(["groups"])
  // One run of one subscription. Anything read from the core belongs to the
  // runtime it was read from, and is dropped when this changes.
  readonly property string runtimeKey: activeSubscription + "|" + startedMs

  property var subscriptions: []
  property var rules: []               // the owner's, editable
  property var injectedRules: []       // Omihomo's, read-only
  property var subscriptionRules: []   // the subscription's, read-only
  property var effectiveRules: []
  signal ruleAdded()
  property var groups: []
  property var configEntries: ({})     // config name -> { name, type, alive, history }
  // Whether the groups stream has answered for this run of the core. An empty
  // `groups` means "not read yet" until it has, and "this subscription
  // declares none" after — two states the panel has to word differently.
  property bool proxiesLoaded: false
  // The `connections` stream is a snapshot of what is open right now, so the
  // panel keeps its own log of it: `connectionLog` holds every connection seen
  // since the core started, open or closed, and `connectionStacks` is what the
  // view draws — one row per process, destination, and state.
  property var connectionLog: []
  readonly property var connectionStacks: Model.stackConnections(connectionLog)
  readonly property int openConnectionCount: Model.openConnectionCount(connectionLog)
  readonly property int connectionLogCap: 200
  property double connectionsDownload: 0
  property double connectionsUpload: 0

  property double downloadRate: 0
  property double uploadRate: 0
  // The last minute of traffic, oldest first, one { down, up } per push.
  property var trafficHistory: []
  readonly property int trafficHistoryCap: 60
  property string egressIp: ""
  property int egressLatency: 0
  property bool traceTesting: false
  property bool traceFailed: false
  property bool _traceQueued: false

  // Manual results sit above the groups stream. A push may have left the
  // daemon before the click, so it must not repaint an old latency over the
  // test that the user is watching.
  property var configTestResults: ({}) // config name -> { state, delay }
  property var groupTestedAt: ({})     // group name -> ms of its last whole-group test
  property string pendingConfig: ""
  property string pendingSubscription: ""

  property var streamErrors: ({})

  property string actionStatus: ""
  property string lastError: ""
  property string lastErrorCode: ""
  // Where the failed action lives, so the panel can show the error next to it:
  // "core", "configs", "rules", "conns", "settings", or "sub:<name>" ("sub:"
  // for an add).
  property string errorScope: ""

  // The call in flight on each lane, 0 for none. A lane runs one call at a
  // time, the way a button cannot be pressed twice.
  property int _coreCall: 0
  property int _settingsCall: 0
  property int _subCall: 0
  property int _ruleCall: 0
  property int _selectCall: 0
  property int _delayCall: 0
  property int _egressCall: 0
  property int _closeCall: 0

  readonly property string primaryGroup: Model.primaryGroupName(groups, configuredPrimaryGroup)
  readonly property var primaryGroupEntry: Model.groupByName(groups, primaryGroup)
  readonly property string currentConfig: primaryGroupEntry ? primaryGroupEntry.now : ""
  readonly property bool busy: _coreCall !== 0 || _settingsCall !== 0 || _subCall !== 0
    || _ruleCall !== 0 || _selectCall !== 0 || _closeCall !== 0
  readonly property bool ruleSaving: _ruleCall !== 0
  readonly property bool pingTestsRunning: _delayCall !== 0

  signal configChanged()

  function reportError(error, scope) {
    lastError = Model.daemonError(error)
    lastErrorCode = error && error.code ? String(error.code) : ""
    errorScope = scope || "core"
    actionStatus = ""
    statusClearTimer.restart()
  }

  function streamScope(stream) {
    var scopes = { status: "core", groups: "configs", rules: "rules",
      subscriptions: "sub:", traffic: "core", connections: "conns" }
    return scopes[stream] || "core"
  }

  function streamsReady(streams) {
    return ready && streams.every(function(stream) { return !root.streamErrors[stream] })
  }

  function reportDone(message) {
    var failed = Object.keys(streamErrors)
    if (failed.length > 0) return reportError(streamErrors[failed[0]], streamScope(failed[0]))
    lastError = ""
    lastErrorCode = ""
    errorScope = ""
    actionStatus = message || ""
    if (actionStatus !== "") statusClearTimer.restart()
  }

  function clearMessages() {
    reportDone("")
  }

  property int _nextId: 1
  // id -> function(result, error), for every call awaiting its reply.
  property var _pending: ({})

  // Sends one request. `done` gets the result, or null and the error; it is
  // always called exactly once, with a "disconnected" error if the connection
  // drops first. Returns the call's id, or 0 when there is no daemon to ask.
  function call(op, args, done) {
    if (!ready) return 0
    var id = _nextId++
    _pending[id] = done || null
    var request = { id: id, op: op }
    if (args !== undefined && args !== null) request.args = args
    socket.write(JSON.stringify(request) + "\n")
    socket.flush()
    return id
  }

  function failPending() {
    var pending = _pending
    _pending = ({})
    var error = { code: "disconnected", message: "The connection to the core dropped" }
    for (const id in pending) if (pending[id]) pending[id](null, error)
  }

  function receive(line) {
    var message = Model.parseJson(line, null)
    if (!message || typeof message !== "object") return
    if (message.type === "hello") return greet(message)
    if (message.type === "refused") return refuse(message.error)
    if (!ready) return
    if (message.type === "reply") {
      const done = _pending[message.id]
      if (done === undefined) return
      delete _pending[message.id]
      if (done) done(message.error ? null : message.result, message.error || null)
    } else if (message.type === "event") {
      const stream = String(message.stream)
      applyEvent(stream, message.data, message.error)
    }
  }

  function greet(hello) {
    helloTimer.stop()
    var version = Number(hello.protocol)
    daemonVersion = String(hello.version || "")
    if (version !== protocolVersion) {
      link = version < protocolVersion ? "outdated" : "newer"
      forgetAll()
      hangUp()
      return
    }
    if (link === "busy" || link === "refused") clearMessages()
    link = "ready"
    reconnectTimer.attempt = 0
    call("subscribe", { streams: wantedStreams() })
  }

  function refuse(error) {
    helloTimer.stop()
    link = error && error.code === "forbidden" ? "refused" : "busy"
    forgetAll()
    reportError(error, "core")
    hangUp()
  }

  readonly property var panelStreams: ["groups", "rules", "subscriptions", "traffic", "connections"]

  function wantedStreams() {
    return panelOpen ? ["status"].concat(panelStreams) : ["status"]
  }

  // Stream snapshots own displayed state; command replies only finish actions.
  function applyEvent(stream, data, error) {
    if (wantedStreams().indexOf(stream) < 0) return
    var previous = streamErrors[stream]
    if (error || previous) {
      const errors = Object.assign({}, streamErrors)
      if (error) errors[stream] = error
      else delete errors[stream]
      streamErrors = errors
    }
    if (error) data = null
    switch (stream) {
    case "status":
      if (error) clearDesired()
      else applyStatus(data)
      break
    case "groups": applyGroups(data); break
    case "rules": applyRules(data); break
    case "subscriptions": subscriptions = Model.readSubscriptions(data); break
    case "traffic": applyTraffic(data); break
    case "connections": applyConnections(data); break
    }
    if (error) reportError(error, streamScope(stream))
    else if (previous && errorScope === streamScope(stream)
      && lastError === Model.daemonError(previous)) reportDone("")
  }

  // Bindings land in no fixed order while the panel is created, so nothing
  // connects before it is complete and `socketPath` is final.
  property bool _completed: false

  function connect() {
    if (!_completed || socket.connected || socketPath === "") return
    reconnectTimer.stop()
    socket.path = socketPath
    socket.connected = true
    helloTimer.restart()
  }

  // A connection that ends fails what it was waiting for and comes back with
  // a backoff. A daemon that turned this panel away is asked again too, but
  // more slowly: the owner may claim the install, or update it, meanwhile.
  function disconnected() {
    helloTimer.stop()
    if (link === "ready" || link === "connecting") link = "connecting"
    failPending()
    downloadRate = 0
    uploadRate = 0
    reconnectTimer.restart()
  }

  // QLocalSocket::LocalSocketError: 0 refused, 2 not found, 3 access denied.
  // A socket nobody listens on, or none at all, is a core that is not
  // installed. One this account may not open belongs to another owner.
  function connectFailed(error) {
    if (error === 0 || error === 2) {
      link = "absent"
      forgetAll()
    } else if (error === 3) {
      link = "refused"
      forgetAll()
    }
    hangUp()
    reconnectTimer.restart()
  }

  // Quickshell's Socket crashes when it is closed from inside its own
  // signals, so a close asked for there waits until they return.
  function hangUp() { Qt.callLater(_hangUp) }
  function _hangUp() { socket.connected = false }

  Socket {
    id: socket
    parser: SplitParser {
      onRead: function(line) { root.receive(line) }
    }
    onConnectedChanged: if (!connected) root.disconnected()
    onError: function(error) { root.connectFailed(error) }
  }

  Timer {
    id: reconnectTimer
    property int attempt: 0
    interval: root.link === "absent" ? 5000 : Math.min(30000, 1000 * Math.pow(2, attempt))
    onTriggered: {
      attempt += 1
      root.connect()
    }
  }

  // A daemon that accepts the connection but never greets is as good as none.
  Timer {
    id: helloTimer
    interval: 5000
    onTriggered: socket.connected = false
  }

  Component.onCompleted: {
    _completed = true
    connect()
  }

  onSocketPathChanged: {
    if (!_completed) return
    socket.connected = false
    link = "connecting"
    forgetAll()
    Qt.callLater(connect)
  }

  // Opening the panel subscribes to what it shows; the daemon pushes each
  // stream's current value at once, so the first open renders without
  // waiting. Closing it drops them again.
  onPanelOpenChanged: {
    if (!ready) {
      if (panelOpen) connect()
      return
    }
    call(panelOpen ? "subscribe" : "unsubscribe", { streams: panelStreams })
    if (panelOpen) {
      refreshTrace()
    } else {
      downloadRate = 0
      uploadRate = 0
    }
  }

  // Only newly subscribed streams receive snapshots from the daemon.
  function refresh() {
    if (!ready) return connect()
    call("unsubscribe", { streams: ["status"].concat(panelStreams) }, function(result, error) {
      if (error) return root.reportError(error, "core")
      root.call("subscribe", { streams: root.wantedStreams() }, function(result, error) {
        if (error) root.reportError(error, "core")
      })
    })
  }

  // IP and latency are the only fields that cost a network round trip, so they
  // are event-driven: panel open, config or group change, or a click. A check
  // only measures the route once it is known: it waits for a pending
  // selection to be confirmed, and a newer check replaces one already running.
  function refreshTrace() {
    if (!apiReady) return
    traceTesting = true
    if (pendingConfig !== "" || _egressCall !== 0) {
      _traceQueued = true
      return
    }
    _traceQueued = false
    _egressCall = call("egress.check", null, function(result, error) {
      root._egressCall = 0
      if (root._traceQueued) return root.runQueuedTrace()
      root.traceTesting = false
      var egress = Model.readEgress(result)
      // Keep the last known values. The row says "failed" until the next
      // check, then a successful result replaces them.
      if (error || egress.ip === "" || egress.latency <= 0) {
        root.traceFailed = true
        return
      }
      root.traceFailed = false
      root.egressIp = egress.ip
      root.egressLatency = egress.latency
    })
    if (_egressCall === 0) traceTesting = false
  }

  function runQueuedTrace() {
    if (_traceQueued && pendingConfig === "") refreshTrace()
  }

  function applyStatus(data) {
    var status = Model.readStatus(data)
    var previousRuntime = runtimeKey
    var previousPrimaryGroup = configuredPrimaryGroup
    var wasRunning = coreRunning
    coreState = status.state
    coreDetail = status.detail
    activeSubscription = status.activeSubscription
    configuredPrimaryGroup = status.primaryGroup
    mode = status.mode
    tunEnabled = status.tunEnabled
    tunRedirect = status.tunRedirect
    tailscaleEnabled = status.tailscaleEnabled
    tailscalePresent = status.tailscalePresent
    tailnetProxy = status.tailnetProxy
    startedMs = status.startedMs
    routing = Model.statusRouting(status)
    if (_desiredCoreRunning !== -1 && coreRunning === (_desiredCoreRunning === 1))
      _desiredCoreRunning = -1
    if (_desiredTunEnabled !== -1 && tunEnabled === (_desiredTunEnabled === 1))
      _desiredTunEnabled = -1
    if (_desiredTunRedirect !== -1 && tunRedirect === (_desiredTunRedirect === 1))
      _desiredTunRedirect = -1
    if (_desiredTailscaleEnabled !== -1 && tailscaleEnabled === (_desiredTailscaleEnabled === 1))
      _desiredTailscaleEnabled = -1
    if (_desiredTailnetProxy !== -1 && tailnetProxy === (_desiredTailnetProxy === 1))
      _desiredTailnetProxy = -1
    if (_desiredMode !== "" && mode === _desiredMode) _desiredMode = ""
    // A proxy that is starting keeps its last start time, so a hiccup keeps the
    // last snapshot on screen. A stop, restart, or another subscription is a
    // different runtime and starts from nothing.
    var runtimeChanged = runtimeKey !== previousRuntime
    var primaryChanged = configuredPrimaryGroup !== previousPrimaryGroup
    if (runtimeChanged) forgetRuntime()
    if (primaryChanged) {
      egressIp = ""
      egressLatency = 0
      traceFailed = false
      if (_egressCall !== 0) _traceQueued = true
    }
    if (!coreRunning) {
      downloadRate = 0
      uploadRate = 0
      trafficHistory = []
      traceTesting = false
      _traceQueued = false
    }
    if (panelOpen && apiReady && (!wasRunning || runtimeChanged || primaryChanged)) refreshTrace()
  }

  function forgetRuntime() {
    applyGroups(null)
    applyConnections(null)
    applyTraffic(null)
  }

  // Everything the daemon said, for when there is no daemon to talk to.
  function forgetAll() {
    streamErrors = ({})
    coreState = link === "absent" ? "not-installed" : "unknown"
    coreDetail = ""
    activeSubscription = ""
    configuredPrimaryGroup = ""
    mode = ""
    tunEnabled = false
    tunRedirect = false
    tailscaleEnabled = false
    tailscalePresent = false
    tailnetProxy = false
    startedMs = 0
    routing = ({})
    subscriptions = []
    applyRules(null)
    forgetRuntime()
    traceTesting = false
    _traceQueued = false
    clearDesired()
  }

  function clearDesired() {
    _desiredCoreRunning = -1
    _desiredTunEnabled = -1
    _desiredTunRedirect = -1
    _desiredTailscaleEnabled = -1
    _desiredTailnetProxy = -1
    _desiredMode = ""
  }

  function applyGroups(data) {
    var parsed = Model.readGroups(data)
    groups = parsed.groups
    configEntries = parsed.configs
    proxiesLoaded = data !== null && coreRunning
    if (data === null) {
      configTestResults = ({})
      groupTestedAt = ({})
    }
  }

  function applyRules(data) {
    var parsed = Model.readRules(data)
    rules = parsed.own
    injectedRules = parsed.injected
    subscriptionRules = parsed.subscription
    effectiveRules = parsed.effective
  }

  function applyTraffic(data) {
    var traffic = Model.readTraffic(data)
    if (!traffic || !coreRunning) {
      downloadRate = 0
      uploadRate = 0
      trafficHistory = []
      return
    }
    downloadRate = traffic.down
    uploadRate = traffic.up
    var history = trafficHistory.slice(1 - trafficHistoryCap)
    history.push({ down: traffic.down, up: traffic.up })
    trafficHistory = history
  }

  function applyConnections(data) {
    if (data === null) {
      connectionLog = []
      connectionsDownload = 0
      connectionsUpload = 0
      return
    }
    var now = Date.now()
    var parsed = Model.readConnections(data, now)
    connectionLog = Model.mergeConnectionLog(connectionLog, parsed.items, now, connectionLogCap)
    connectionsDownload = parsed.downloadTotal
    connectionsUpload = parsed.uploadTotal
  }

  property var _editDone: null

  function edit(args, done) {
    if (editor.running || _editDone !== null) return 0
    _editDone = done
    editor.command = ["/usr/bin/env", "OMIHOMO_SOCKET=" + socketPath, cliPath].concat(args)
    editor.running = true
    return 1
  }

  Process {
    id: editor
    stdout: StdioCollector { id: editOutput; waitForEnd: true }
    stderr: StdioCollector { id: editError; waitForEnd: true }
    onExited: function(exitCode, exitStatus) {
      var done = root._editDone
      root._editDone = null
      if (!done) return
      if (exitCode !== 0 || exitStatus !== 0) {
        done(null, { code: "internal", message: editError.text.trim() || "Could not save settings" })
        return
      }
      let result
      try { result = JSON.parse(editOutput.text) } catch (error) {
        done(null, { code: "internal", message: "The CLI returned an invalid reply" })
        return
      }
      done(result, null)
    }
  }

  function toggleCore() {
    if (!streamsReady(["status"]) || !installed || _coreCall !== 0) return
    var desired = coreActive ? 0 : 1
    _desiredCoreRunning = desired
    reportDone(desired === 1 ? "Starting the proxy…" : "Stopping the proxy…")
    _coreCall = call(desired === 1 ? "start" : "stop", null, function(result, error) {
      root._coreCall = 0
      root._desiredCoreRunning = -1
      if (error) return root.reportError(error, "core")
      root.reportDone("")
    })
    if (_coreCall === 0) {
      _desiredCoreRunning = -1
      actionStatus = ""
    }
  }

  // Optimistic settings last until command completion; confirmed values arrive
  // on the status stream.
  function changeSetting(args, desired, pending, message, scope) {
    if (!streamsReady(["status"]) || _settingsCall !== 0 || _ruleCall !== 0) return false
    if (desired !== "") root[desired] = pending
    reportDone(message || "")
    var key = Object.keys(args)[0]
    var names = { primary_group: "group", tun_redirect: "tun-redirect", tailnet_proxy: "tailnet-proxy" }
    var value = typeof args[key] === "boolean" ? (args[key] ? "on" : "off") : args[key]
    _settingsCall = edit(["set", names[key] || key, "--", value], function(result, error) {
      root._settingsCall = 0
      if (desired !== "") root[desired] = typeof pending === "string" ? "" : -1
      if (error) return root.reportError(error, scope || "settings")
      root.reportDone("")
    })
    if (_settingsCall !== 0) return true
    if (desired !== "") root[desired] = typeof pending === "string" ? "" : -1
    actionStatus = ""
    return false
  }

  function setMode(next) {
    if (next === "" || next === effectiveMode) return
    changeSetting({ mode: next }, "_desiredMode", next)
  }

  function toggleTun() {
    var desired = tunActive ? 0 : 1
    changeSetting({ tun: desired === 1 }, "_desiredTunEnabled", desired,
      desired === 1 ? "Enabling TUN…" : "")
  }

  // The repair for a TUN that will not start, or that starts and carries
  // nothing: turning the redirect off drops to the stack that needs no firewall
  // rules.
  function toggleTunRedirect() {
    var desired = tunRedirectActive ? 0 : 1
    changeSetting({ tun_redirect: desired === 1 }, "_desiredTunRedirect", desired,
      desired === 1 ? "Enabling TUN acceleration…" : "Disabling TUN acceleration…")
  }

  function toggleTailscale() {
    if (!tailscalePresent) return
    var desired = tailscaleActive ? 0 : 1
    changeSetting({ tailscale: desired === 1 }, "_desiredTailscaleEnabled", desired,
      desired === 1 ? "Enabling Tailscale…" : "Disabling Tailscale…")
  }

  function toggleTailnetProxy() {
    if (!tailscalePresent) return
    var desired = tailnetProxyActive ? 0 : 1
    changeSetting({ tailnet_proxy: desired === 1 }, "_desiredTailnetProxy", desired,
      desired === 1 ? "Routing the tailnet through the proxy…" : "Routing the tailnet through Tailscale…")
  }

  function setPrimaryGroup(name) {
    if (!streamsReady(["groups"]) || name === "") return
    changeSetting({ primary_group: name }, "", 0, "", "configs")
  }

  // Returns whether the write started; the form waits for `ruleAdded`.
  function addRule(type, value, target) {
    if (!streamsReady(["rules", "groups"]) || _ruleCall !== 0 || _settingsCall !== 0) return false
    reportDone("")
    _ruleCall = edit(["rule", "add", "--", type, value, target], function(result, error) {
      root._ruleCall = 0
      if (error) return root.reportError(error, "rules")
      root.reportDone("")
      root.ruleAdded()
    })
    return _ruleCall !== 0
  }

  function removeRule(index) {
    if (!streamsReady(["rules", "groups"]) || _ruleCall !== 0 || _settingsCall !== 0 || index <= 0) return
    reportDone("")
    _ruleCall = edit(["rule", "remove", "--", String(index)], function(result, error) {
      root._ruleCall = 0
      if (error) return root.reportError(error, "rules")
    })
  }

  // One subscription action at a time. `target` is the subscription it acts
  // on, "" for an add, which names itself only once the download is back.
  function subscriptionCall(op, args, target, message) {
    if (!streamsReady(["subscriptions", "status"]) || _subCall !== 0) return
    pendingSubscription = target
    reportDone(message)
    _subCall = call(op, args, function(result, error) {
      root._subCall = 0
      root.pendingSubscription = ""
      if (error) return root.reportError(error, "sub:" + target)
      root.reportDone("")
    })
    if (_subCall === 0) {
      pendingSubscription = ""
      actionStatus = ""
    }
  }

  // The URL goes to the daemon and nowhere else: it usually carries a token,
  // and no list shows it again.
  function addSubscription(url) {
    subscriptionCall("subscriptions.add", { url: url }, "", "Fetching subscription…")
  }

  function updateSubscription(name) {
    if (name === "") return
    subscriptionCall("subscriptions.update", { name: name }, name, "Updating " + name + "…")
  }

  function activateSubscription(name) {
    if (name === "") return
    subscriptionCall("subscriptions.activate", { name: name }, name, "Activating " + name + "…")
  }

  function removeSubscription(name) {
    if (name === "") return
    subscriptionCall("subscriptions.remove", { name: name }, name, "")
  }

  // Only a selection spends the panel's one event-driven trace call.
  function selectConfig(group, config) {
    if (!groupsReady || _selectCall !== 0 || group === "" || config === "") return
    pendingConfig = config
    reportDone("")
    _selectCall = call("groups.select", { group: group, proxy: config }, function(result, error) {
      root._selectCall = 0
      root.pendingConfig = ""
      if (error) {
        root.reportError(error, "configs")
      } else {
        root.reportDone("")
        root.configChanged()
      }
      root.runQueuedTrace()
    })
    if (_selectCall === 0) pendingConfig = ""
  }

  function configTestState(name) {
    var result = configTestResults[String(name)]
    return result ? String(result.state || "") : ""
  }

  function configDelay(name) {
    var result = configTestResults[String(name)]
    if (result && result.state === "success") return Number(result.delay) || 0
    return Model.historyDelay(configEntries[String(name)])
  }

  function startConfigTests(names) {
    var next = Object.assign({}, configTestResults)
    for (let i = 0; i < names.length; i++)
      next[String(names[i])] = { state: "testing", delay: 0 }
    configTestResults = next
  }

  // Publish the whole batch once. Config rows then move straight from
  // "testing" to their final result without repainting one by one.
  function finishConfigTests(names, delays) {
    var next = Object.assign({}, configTestResults)
    for (let i = 0; i < names.length; i++) {
      const name = String(names[i])
      const delay = delays && delays[name] !== undefined ? Number(delays[name]) : 0
      next[name] = isFinite(delay) && delay > 0
        ? { state: "success", delay: delay } : { state: "failed", delay: 0 }
    }
    configTestResults = next
  }

  // `proxies` names the configs to test; empty tests the whole group, which
  // the core does concurrently.
  function runDelayTest(group, names, whole) {
    var runtime = runtimeKey
    startConfigTests(names)
    var args = whole ? { group: group } : { group: group, proxies: names }
    _delayCall = call("delay.test", args, function(result, error) {
      root._delayCall = 0
      if (runtime !== root.runtimeKey || !root.groupsReady) return
      root.finishConfigTests(names, error ? {} : Model.readDelays(result))
      if (!whole) return
      const tested = Object.assign({}, root.groupTestedAt)
      tested[group] = Date.now()
      root.groupTestedAt = tested
    })
    if (_delayCall === 0) finishConfigTests(names, {})
  }

  function testConfig(name) {
    if (!groupsReady || pingTestsRunning || name === "") return
    var group = Model.groupForConfig(groups, name)
    if (group !== "") runDelayTest(group, [name], false)
  }

  function testGroup(name) {
    if (!groupsReady || pingTestsRunning || name === "") return
    var group = Model.groupByName(groups, name)
    var names = group && group.all ? group.all.slice() : []
    if (names.length > 0) runDelayTest(name, names, true)
  }

  function closeConnections(args) {
    if (!apiReady || !streamsReady(["connections"]) || _closeCall !== 0) return
    _closeCall = call("connections.close", args, function(result, error) {
      root._closeCall = 0
      if (error) root.reportError(error, "conns")
    })
  }

  // A stack closes as one call. Half a stack is often already gone by the time
  // the row is activated, which the daemon does not count as a failure.
  function closeStack(stack) {
    var ids = stack ? stack.ids : null
    if (ids && ids.length > 0) closeConnections({ ids: ids.slice() })
  }

  function closeAllConnections() {
    closeConnections({ all: true })
  }

  // A closed connection exists only in the panel's log, so dropping one is a
  // local edit and needs no call at all.
  function forgetStack(stack) {
    var ids = stack ? stack.ids : null
    if (!ids || ids.length === 0) return
    var drop = {}
    var i
    for (i = 0; i < ids.length; i++) drop[ids[i]] = true
    var kept = []
    for (i = 0; i < connectionLog.length; i++) {
      const entry = connectionLog[i]
      if (entry.open || !drop[entry.id]) kept.push(entry)
    }
    connectionLog = kept
  }

  function clearConnectionLog() {
    var kept = []
    for (let i = 0; i < connectionLog.length; i++) {
      if (connectionLog[i].open) kept.push(connectionLog[i])
    }
    connectionLog = kept
  }

  Timer {
    id: statusClearTimer
    interval: 4000
    onTriggered: root.actionStatus = ""
  }
}
