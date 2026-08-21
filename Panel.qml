import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell.Io
import qs.Commons
import qs.Ui
import "Model.js" as Model

// Omihomo's bar widget: a compact core-state icon, a main popup that carries
// the operations of a normal session, and three secondary views over the same
// surface for the ones that are not.
//
// Navigation is one flat cursor over `navRows` rather than a per-section state
// machine: every visible row — control, subscription, group, config, rule,
// form field — appears once in that list, so j/k walks the whole panel and a
// section appearing or disappearing costs nothing but a rebuild.
Panel {
  id: root

  moduleName: "omihomo"
  ipcTarget: "omihomo"
  manageIpc: false

  // "main", "connections", "rules", or "manage". Every secondary view is the
  // same popup at the same width, with its own rows and its own keys.
  property string view: "main"

  property bool cursorActive: false
  property int cursor: 0

  property string browseGroup: ""
  property bool filterOpen: false
  property string expandedConfig: ""
  readonly property string configFilter: filterOpen ? configFilterField.text : ""

  // With no subscriptions the URL field is the section, so it is always open;
  // once there is one, the field hides behind the add row.
  property bool subFormExplicit: false
  readonly property bool subFormOpen: subFormExplicit || omihomo.subscriptions.length === 0
  property bool ruleFormOpen: false
  // Uninstall throws away subscriptions and the core, so the row asks twice.
  property bool uninstallArmed: false
  property int ruleTypeIndex: 0
  property int ruleTargetIndex: 0

  // Form values live in their fields: binding a TextField's `text` to panel
  // state breaks the moment the user types into it, so the field is the
  // source and the panel reads it.
  readonly property string subFormUrl: subUrlRow.field.text
  readonly property string ruleFormValue: ruleValueRow.field.text

  // The field currently taking keys, or null. While set, the key catcher is
  // blocked and the field owns Esc and Enter.
  property Item editing: null

  property double nowMs: Date.now()

  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property color hoverFill: bar ? Style.hoverFillFor(bar.foreground, Color.accent) : "transparent"
  readonly property color selectedFill: bar ? Style.selectedFillFor(bar.foreground, Color.accent) : "transparent"
  readonly property color barIconColor: omihomo.coreActive ? barForeground : Qt.darker(barForeground, 1.55)

  // The plugin invokes its own CLI by absolute path, derived from where this
  // file sits — no PATH lookup, so a stale copy can never be picked up.
  readonly property string pluginDir: decodeURIComponent(String(Qt.resolvedUrl(".")).replace(/^file:\/\//, ""))
  readonly property string cliPath: pluginDir + "bin/omihomo"

  readonly property bool liveReady: omihomo.apiReady
  readonly property var visibleGroups: Model.userGroups(omihomo.groups)
  readonly property string browsedGroup: Model.groupByName(visibleGroups, browseGroup)
    ? browseGroup : omihomo.primaryGroup
  readonly property var browsedGroupEntry: Model.groupByName(visibleGroups, browsedGroup)
  readonly property var visibleConfigs: browsedGroupEntry
    ? Model.filterNames(browsedGroupEntry.all, configFilter) : []
  readonly property var ruleTargets: {
    var targets = ["DIRECT", "REJECT"]
    for (var i = 0; i < visibleGroups.length; i++) targets.push(visibleGroups[i].name)
    return targets
  }
  readonly property string ruleType: Model.RULE_TYPES[Math.max(0, Math.min(ruleTypeIndex, Model.RULE_TYPES.length - 1))]
  readonly property string ruleTarget: ruleTargets[Math.max(0, Math.min(ruleTargetIndex, ruleTargets.length - 1))]
  readonly property string ruleFormError: Model.validateRule(ruleType, ruleFormValue, ruleTarget)

  readonly property string uptimeText: omihomo.startedMs > 0 && omihomo.coreRunning
    ? Model.formatDuration(nowMs - omihomo.startedMs) : "—"
  readonly property string configText: omihomo.primaryGroup === "" ? "—"
    : omihomo.primaryGroup + " › " + (omihomo.currentConfig !== "" ? omihomo.currentConfig : "—")
  readonly property string egressText: omihomo.traceTesting ? "testing…"
    : (omihomo.traceFailed ? "failed" : (omihomo.egressIp === "" ? "—"
      : omihomo.egressIp + " · " + Model.formatDelay(omihomo.egressLatency)))
  readonly property string throughputText: "↓ " + Model.formatRate(omihomo.downloadRate)
    + "   ↑ " + Model.formatRate(omihomo.uploadRate)

  // ---------------------------------------------------------------- cursor

  function navRowsFor() {
    var rows = []
    var i
    if (!omihomo.installed) {
      rows.push({ s: "install" })
      return rows
    }
    if (view === "connections") {
      for (i = 0; i < omihomo.connectionStacks.length; i++) rows.push({ s: "conn", i: i })
      return rows
    }
    if (view === "manage") {
      rows.push({ s: "autostart" })
      if (!omihomo.permissionsOk) rows.push({ s: "repair" })
      rows.push({ s: "uninstall" })
      return rows
    }
    if (view === "rules") {
      for (i = 0; i < omihomo.rules.length; i++) rows.push({ s: "rule", i: i })
      if (ruleFormOpen) {
        rows.push({ s: "ruleType" })
        rows.push({ s: "ruleValue" })
        rows.push({ s: "ruleTarget" })
        rows.push({ s: "ruleSubmit" })
      } else {
        rows.push({ s: "ruleAdd" })
      }
      return rows
    }
    rows.push({ s: "power" })
    rows.push({ s: "trace" })
    // Configs come before their groups: picking a config in the primary group
    // is the one thing done every session, so it sits closest to the readout.
    if (liveReady) {
      for (i = 0; i < visibleConfigs.length; i++) rows.push({ s: "config", i: i })
      for (i = 0; i < visibleGroups.length; i++) rows.push({ s: "group", i: i })
    }
    for (i = 0; i < omihomo.subscriptions.length; i++) rows.push({ s: "sub", i: i })
    if (subFormOpen) {
      rows.push({ s: "subUrl" })
      rows.push({ s: "subSubmit" })
    } else {
      rows.push({ s: "subAdd" })
    }
    rows.push({ s: "mode" })
    rows.push({ s: "tun" })
    rows.push({ s: "rules" })
    rows.push({ s: "connections" })
    rows.push({ s: "manage" })
    return rows
  }

  readonly property var navRows: navRowsFor()
  readonly property var cursorRow: cursor >= 0 && cursor < navRows.length ? navRows[cursor] : null
  // Leaving the row disarms it, so a confirm can never be inherited by whatever
  // the cursor lands on next.
  onCursorRowChanged: if (uninstallArmed && (!cursorRow || cursorRow.s !== "uninstall")) uninstallArmed = false

  // True when the keyboard cursor is on this exact row. Rows bind their
  // `hasCursor` to it, so mouse hover and j/k paint the same single highlight.
  function at(section, index) {
    if (!cursorActive || !cursorRow) return false
    if (cursorRow.s !== section) return false
    return index === undefined || cursorRow.i === index
  }

  function clampCursor() {
    if (navRows.length === 0) {
      cursor = 0
      return
    }
    if (cursor < 0) cursor = 0
    if (cursor > navRows.length - 1) cursor = navRows.length - 1
  }

  function moveCursor(dx, dy) {
    cursorActive = true
    if (dy !== 0) {
      cursor = Math.max(0, Math.min(navRows.length - 1, cursor + dy))
      scrollCursorIntoView()
    } else if (dx !== 0) {
      adjustCursorRow(dx)
    }
  }

  function focusRow(section, index) {
    for (var i = 0; i < navRows.length; i++) {
      if (navRows[i].s === section && (index === undefined || navRows[i].i === index)) {
        cursorActive = true
        cursor = i
        return
      }
    }
  }

  // h / l changes the value of a row that has one, instead of moving.
  function adjustCursorRow(delta) {
    if (!cursorRow) return
    if (cursorRow.s === "mode") {
      var index = Model.MODES.indexOf(omihomo.effectiveMode)
      var next = (index < 0 ? 0 : index + delta + Model.MODES.length) % Model.MODES.length
      omihomo.setMode(Model.MODES[next])
    } else if (cursorRow.s === "tun") {
      omihomo.toggleTun()
    } else if (cursorRow.s === "autostart") {
      omihomo.toggleAutostart()
    } else if (cursorRow.s === "ruleType") {
      ruleTypeIndex = (ruleTypeIndex + delta + Model.RULE_TYPES.length) % Model.RULE_TYPES.length
    } else if (cursorRow.s === "ruleTarget") {
      ruleTargetIndex = (ruleTargetIndex + delta + ruleTargets.length) % ruleTargets.length
    }
  }

  function activateCursor() {
    if (!cursorRow) return
    switch (cursorRow.s) {
    case "install": installCore(); break
    case "uninstall": uninstallCore(); break
    case "power": omihomo.toggleCore(); break
    case "trace": omihomo.refreshTrace(); break
    case "mode": omihomo.setMode(Model.nextMode(omihomo.effectiveMode)); break
    case "tun": omihomo.toggleTun(); break
    case "rules": openRules(false); break
    case "connections": openConnections(); break
    case "conn": activateConnectionAt(cursorRow.i); break
    case "manage": openManage(); break
    case "autostart": omihomo.toggleAutostart(); break
    case "repair": omihomo.repairCore(); break
    case "sub": activateSubscriptionAt(cursorRow.i); break
    case "subAdd": openSubForm(); break
    case "subUrl": beginEdit(subUrlRow.field); break
    case "subSubmit": submitSubForm(); break
    case "group": browseGroupAt(cursorRow.i); break
    case "config": chooseConfigAt(cursorRow.i); break
    case "ruleAdd": openRuleForm(); break
    case "ruleType": ruleTypeIndex = (ruleTypeIndex + 1) % Model.RULE_TYPES.length; break
    case "ruleValue": beginEdit(ruleValueRow.field); break
    case "ruleTarget": ruleTargetIndex = (ruleTargetIndex + 1) % ruleTargets.length; break
    case "ruleSubmit": submitRuleForm(); break
    }
  }

  function deleteCursorRow() {
    if (!cursorRow) return
    var target = null
    if (cursorRow.s === "sub") {
      target = subscriptionAt(cursorRow.i)
      if (target) omihomo.removeSubscription(target.name)
    } else if (cursorRow.s === "rule") {
      target = ruleAt(cursorRow.i)
      if (target) omihomo.removeRule(target.index)
    } else if (cursorRow.s === "conn") {
      activateConnectionAt(cursorRow.i)
    }
  }

  function handleTextKey(key) {
    var lower = key.toLowerCase()
    if (view === "connections") {
      // Not "X": PanelKeyCatcher takes both cases of x as its delete key, so
      // an uppercase one never gets here.
      if (key === "A") omihomo.closeAllConnections()
      else if (key === "L") omihomo.clearConnectionLog()
      else if (lower === "r") omihomo.refreshConnections()
      else if (lower === "c") closeConnections()
      return
    }
    if (view === "rules") {
      if (lower === "n") openRuleForm()
      else if (lower === "r") omihomo.refresh()
      return
    }
    if (view === "manage") {
      if (key === "M") closeManage()
      else if (key === "R") omihomo.repairCore()
      else if (lower === "b") omihomo.toggleAutostart()
      return
    }
    if (!omihomo.installed) {
      if (lower === "i") installCore()
      return
    }
    switch (key) {
    case "s": omihomo.toggleCore(); return
    case "t": omihomo.toggleTun(); return
    case "m": omihomo.setMode(Model.nextMode(omihomo.effectiveMode)); return
    case "c": openConnections(); return
    case "M": openManage(); return
    case "r": omihomo.refresh(); omihomo.refreshLive(); omihomo.refreshTrace(); return
    case "a": openSubForm(); return
    // `n` still means "new rule": it opens the rules view with the form
    // already up, so the key costs the same two steps it always did.
    case "n": openRules(true); return
    case "R": omihomo.repairCore(); return
    case "/": openFilter(); return
    }
    if (lower === "u") updateSelectedSubscription()
    else if (lower === "p") makeSelectedGroupPrimary()
    else if (key === "d") testSelectedConfig()
    else if (key === "D") omihomo.testGroup(browsedGroup)
  }

  function handleEscape() {
    if (editing) { endEdit(); return }
    if (uninstallArmed) { uninstallArmed = false; return }
    if (filterOpen) { closeFilter(); return }
    if (subFormExplicit) { subFormExplicit = false; return }
    if (ruleFormOpen) { ruleFormOpen = false; return }
    if (view === "connections") { closeConnections(); return }
    if (view === "rules") { closeRules(); return }
    if (view === "manage") { closeManage(); return }
    close()
  }

  // ---------------------------------------------------------------- actions

  function subscriptionAt(index) {
    var list = omihomo.subscriptions
    return list.length === 0 ? null : list[Math.max(0, Math.min(index, list.length - 1))]
  }

  function ruleAt(index) {
    var list = omihomo.rules
    return list.length === 0 ? null : list[Math.max(0, Math.min(index, list.length - 1))]
  }

  function connectionAt(index) {
    var list = omihomo.connectionStacks
    return list.length === 0 ? null : list[Math.max(0, Math.min(index, list.length - 1))]
  }

  // One key does both jobs, because a row means one of two things: an open
  // stack is closed at the core, and a closed one is only in the panel's log,
  // so it is dropped from it.
  function activateConnectionAt(index) {
    var stack = connectionAt(index)
    if (!stack) return
    if (stack.open) omihomo.closeStack(stack)
    else omihomo.forgetStack(stack)
  }

  function activateSubscriptionAt(index) {
    var sub = subscriptionAt(index)
    if (sub && !sub.active) omihomo.activateSubscription(sub.name)
  }

  function updateSelectedSubscription() {
    var sub = cursorRow && cursorRow.s === "sub" ? subscriptionAt(cursorRow.i) : null
    omihomo.updateSubscription(sub ? sub.name : omihomo.activeSubscription)
  }

  function browseGroupAt(index) {
    var group = visibleGroups[index]
    if (!group) return
    browseGroup = group.name
    configFilterField.text = ""
    focusRow("group", index)
  }

  function makeSelectedGroupPrimary() {
    if (!cursorRow || cursorRow.s !== "group") return
    var group = visibleGroups[cursorRow.i]
    if (group) omihomo.setPrimaryGroup(group.name)
  }

  // Enter on a config selects it; a group that picks for itself (URLTest,
  // Fallback) has nothing to select, so the row expands its parameters instead.
  function chooseConfigAt(index) {
    var name = visibleConfigs[index]
    if (!name) return
    if (browsedGroupEntry && browsedGroupEntry.selectable) {
      omihomo.selectConfig(browsedGroup, name)
      if (browsedGroup === omihomo.primaryGroup) omihomo.refreshTrace()
    } else {
      expandedConfig = expandedConfig === name ? "" : name
    }
  }

  function testSelectedConfig() {
    if (cursorRow && cursorRow.s === "config") omihomo.testConfig(visibleConfigs[cursorRow.i])
    else if (omihomo.currentConfig !== "") omihomo.testConfig(omihomo.currentConfig)
  }

  function toggleConfigParameters(name) {
    expandedConfig = expandedConfig === name ? "" : name
  }

  // The AUR build is interactive, so installation is handed to the first-party
  // floating terminal rather than run headless behind the panel.
  function installCore() {
    if (!bar) return
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote(cliPath + " core install"))
    close()
  }

  // Uninstall removes packages and the core's root permissions, so it takes the
  // same terminal as the install: pacman's output and the sudo prompt need
  // somewhere to go.
  // The first activation only arms the row.
  function uninstallCore() {
    if (!bar) return
    if (!uninstallArmed) { uninstallArmed = true; return }
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote(cliPath + " core uninstall"))
    close()
  }

  function openConnections() {
    view = "connections"
    cursor = 0
    cursorActive = false
    omihomo.refreshConnections()
  }

  function closeConnections() {
    view = "main"
    cursor = 0
    cursorActive = false
  }

  // Rules are their own view rather than a main-panel section: a subscription
  // ships hundreds of them, and none of them are read in a normal session.
  function openRules(withForm) {
    view = "rules"
    cursor = 0
    cursorActive = false
    ruleFormOpen = false
    if (rulesFlick) rulesFlick.contentY = 0
    if (withForm) openRuleForm()
  }

  function closeRules() {
    view = "main"
    cursor = 0
    cursorActive = false
    ruleFormOpen = false
  }

  // The manage view holds the operations that outlive a session: autostart,
  // permission repair, and uninstall.
  function openManage() {
    view = "manage"
    cursor = 0
    cursorActive = false
  }

  function closeManage() {
    view = "main"
    cursor = 0
    cursorActive = false
    uninstallArmed = false
  }

  function openSubForm() {
    subUrlRow.field.text = ""
    subFormExplicit = true
    Qt.callLater(function() { root.focusRow("subUrl"); root.beginEdit(subUrlRow.field) })
  }

  function submitSubForm() {
    if (subFormUrl === "") return
    omihomo.addSubscription(subFormUrl)
    subUrlRow.field.text = ""
    subFormExplicit = false
    endEdit()
  }

  function openRuleForm() {
    ruleValueRow.field.text = ""
    ruleFormOpen = true
    Qt.callLater(function() { root.focusRow("ruleValue"); root.beginEdit(ruleValueRow.field) })
  }

  function submitRuleForm() {
    if (ruleFormError !== "") return
    omihomo.addRule(ruleType, ruleFormValue, ruleTarget)
    ruleFormOpen = false
    endEdit()
  }

  function openFilter() {
    configFilterField.text = ""
    filterOpen = true
    Qt.callLater(function() { root.beginEdit(configFilterField) })
  }

  function closeFilter() {
    filterOpen = false
    endEdit()
  }

  // Focus is the single source of truth for who owns the keyboard: a field
  // taking focus — by cursor, by Enter, or by click — blocks the key catcher.
  function beginEdit(field) {
    if (field) Qt.callLater(function() { field.forceActiveFocus() })
  }

  function endEdit() {
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  function fieldFocusChanged(field, focused) {
    if (focused) editing = field
    else if (editing === field) editing = null
  }

  // ---------------------------------------------------------------- scrolling

  function scrollItemIntoView(item, flick) {
    if (!flick || !item) return
    Qt.callLater(function() {
      if (!item) return
      var margin = Style.space(6)
      var point = item.mapToItem(flick.contentItem, 0, 0)
      var top = point.y
      var bottom = top + item.height
      var viewTop = flick.contentY
      var viewBottom = viewTop + flick.height
      var maxY = Math.max(0, flick.contentHeight - flick.height)
      if (top < viewTop + margin) flick.contentY = Math.max(0, top - margin)
      else if (bottom > viewBottom - margin) flick.contentY = Math.min(maxY, bottom + margin - flick.height)
    })
  }

  // The control footer sits outside the main view's flickable, so its rows are
  // always on screen and never scrolled to.
  readonly property var pinnedSections: ["mode", "tun", "rules", "connections", "manage"]

  // Main and rules are the two flickable views: the connection list owns its
  // own scrolling and the manage view always fits. Within the main view the
  // config list is its own ListView, and everything else lives in the
  // view's flickable.
  function scrollCursorIntoView() {
    if (!cursorRow) return
    if (view === "rules") {
      scrollItemIntoView(rowAnchors[rowKey(cursorRow.s, cursorRow.i)] || null, rulesFlick)
      return
    }
    if (view !== "main") return
    if (pinnedSections.indexOf(cursorRow.s) >= 0) return
    if (cursorRow.s === "config") {
      configList.currentIndex = cursorRow.i
      scrollItemIntoView(configSection, panelFlick)
    } else {
      scrollItemIntoView(rowAnchors[rowKey(cursorRow.s, cursorRow.i)] || null, panelFlick)
    }
  }

  // Rows register themselves so the cursor can scroll the panel to them. A row
  // destroyed by a model change leaves a null behind, which reads as "nothing
  // to scroll to" rather than a dangling item.
  property var rowAnchors: ({})
  function rowKey(section, index) {
    return section + ":" + (index === undefined ? -1 : index)
  }
  function registerRowAnchor(key, item) {
    rowAnchors[key] = item
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: {
    if (opened) {
      cursorActive = false
      cursor = 0
      view = "main"
      subFormExplicit = false
      ruleFormOpen = false
      filterOpen = false
      uninstallArmed = false
      editing = null
      if (panelFlick) panelFlick.contentY = 0
      if (rulesFlick) rulesFlick.contentY = 0
      omihomo.clearMessages()
      // The reads that opening triggers live in Service.onPanelOpenChanged,
      // which runs after this handler and after `panelOpen` is actually true.
      Qt.callLater(function() { keyCatcher.forceActiveFocus() })
    } else {
      view = "main"
    }
  }

  onNavRowsChanged: clampCursor()

  Service {
    id: omihomo
    settings: root.settings
    cliPath: root.cliPath
    panelOpen: root.opened
    connectionsOpen: root.opened && root.view === "connections"
    onConfigChanged: root.omihomoConfigChanged()
  }

  // A new config means a new egress path, which is one of the three events
  // that are allowed to spend a network round trip on the IP and latency.
  function omihomoConfigChanged() {
    omihomo.refreshTrace()
  }

  Timer {
    interval: 1000
    repeat: true
    running: root.opened
    onTriggered: root.nowMs = Date.now()
  }

  IpcHandler {
    target: root.ipcTarget

    function open(): void { root.open() }
    function close(): void { root.close() }
    function show(): void { root.open() }
    function hide(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { omihomo.refresh(); return "ok" }
    function start(): string { omihomo.runCore(["core", "start"], ""); return "ok" }
    function stop(): string { omihomo.runCore(["core", "stop"], ""); return "ok" }
    function status(): string { return omihomo.coreState }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    iconComponent: Component {
      Item {
        OmihomoIcon {
          anchors.centerIn: parent
          iconSize: Style.space(11)
          color: root.barIconColor
          badgeColor: root.urgent
          crossed: omihomo.installed && !omihomo.coreActive
          warning: omihomo.coreState === "degraded" || !omihomo.installed
          tunnelled: omihomo.tunActive && omihomo.coreActive
        }
      }
    }
    onPressed: function(buttonCode) {
      if (buttonCode === Qt.RightButton) omihomo.toggleCore()
      else if (buttonCode === Qt.MiddleButton) omihomo.refresh()
      else root.toggle()
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    // Every view is the same 420px surface, so switching between them never
    // moves the popup out from under the pointer.
    contentWidth: panel.fittedContentWidth(Style.space(420))
    // The main view asks for its scrolling body plus the pinned footer, so a
    // short panel still ends right under the footer instead of padding to the cap.
    contentHeight: panel.fittedContentHeight(root.view === "connections" ? connectionsColumn.implicitHeight
      : root.view === "rules" ? rulesColumn.implicitHeight
      : root.view === "manage" ? manageColumn.implicitHeight
      : column.implicitHeight + (controlFooter.visible ? controlFooter.implicitHeight + Style.space(12) : 0),
      Style.space(680))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      blocked: root.editing !== null
      onMoveRequested: function(dx, dy) {
        if (!root.cursorActive) { root.cursorActive = true; return }
        root.moveCursor(dx, dy)
      }
      onActivateRequested: if (root.cursorActive) root.activateCursor()
      onDeleteRequested: if (root.cursorActive) root.deleteCursorRow()
      onCloseRequested: root.handleEscape()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) { root.handleTextKey(t) }

      // ------------------------------------------------------ connections view

      Column {
        id: connectionsColumn
        anchors.fill: parent
        spacing: Style.space(10)
        visible: root.view === "connections"

        Item {
          width: parent.width
          implicitHeight: Math.max(connectionsTitle.implicitHeight, connectionsTotals.implicitHeight)

          Text {
            id: connectionsTitle
            anchors.left: parent.left
            anchors.verticalCenter: parent.verticalCenter
            text: "Connections"
            color: root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.title
            font.bold: true
          }

          Text {
            id: connectionsTotals
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            horizontalAlignment: Text.AlignRight
            text: omihomo.openConnectionCount + " open · " + omihomo.connectionLog.length + " logged"
              + "\n↓ " + Model.formatBytes(omihomo.connectionsDownload)
              + "  ↑ " + Model.formatBytes(omihomo.connectionsUpload)
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
          }
        }

        PanelSeparator { foreground: root.foreground }

        Text {
          visible: omihomo.connectionStacks.length === 0
          width: parent.width
          text: root.liveReady ? "Nothing logged yet." : "mihomo is not running."
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          horizontalAlignment: Text.AlignHCenter
        }

        ListView {
          id: connectionList
          width: parent.width
          height: Math.min(contentHeight, Style.space(520))
          spacing: Style.space(4)
          clip: true
          boundsBehavior: Flickable.StopAtBounds
          interactive: contentHeight > height
          ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

          model: omihomo.connectionStacks
          currentIndex: root.cursorRow && root.cursorRow.s === "conn" ? root.cursorRow.i : -1
          onCurrentIndexChanged: if (currentIndex >= 0) Qt.callLater(keepCurrentVisible)
          function keepCurrentVisible() {
            if (currentIndex >= 0) positionViewAtIndex(currentIndex, ListView.Contain)
          }

          delegate: Item {
            required property var modelData
            required property int index

            width: ListView.view.width
            height: connectionRow.implicitHeight

            ConnectionRow {
              id: connectionRow
              width: parent.width
              stack: modelData
              rowIndex: index
            }
          }
        }

        Text {
          width: parent.width
          text: "enter close · x close · A close all · L clear log · esc back"
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
        }
      }

      // ----------------------------------------------------------- manage view

      Column {
        id: manageColumn
        anchors.fill: parent
        spacing: Style.space(10)
        visible: root.view === "manage"

        Item {
          width: parent.width
          implicitHeight: Math.max(manageTitle.implicitHeight, manageState.implicitHeight)

          Text {
            id: manageTitle
            anchors.left: parent.left
            anchors.verticalCenter: parent.verticalCenter
            text: "Manage"
            color: root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.title
            font.bold: true
          }

          Text {
            id: manageState
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            text: Model.stateLabel(omihomo.coreState)
            color: omihomo.coreState === "degraded" ? root.urgent : root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
          }
        }

        PanelSeparator { foreground: root.foreground }

        Column {
          width: parent.width
          spacing: Style.space(4)

          ActionRow {
            width: parent.width
            section: "autostart"
            title: "Autostart"
            subtitle: "Start mihomo when the session starts."
            trailing: omihomo.autostartActive ? "on" : "off"
            current: omihomo.autostartActive
            onActivated: omihomo.toggleAutostart()
          }

          ActionRow {
            width: parent.width
            visible: !omihomo.permissionsOk
            section: "repair"
            title: "Repair permissions"
            subtitle: "The mihomo binary is missing the root permissions TUN needs."
            trailing: "R"
            urgentTrailing: omihomo.coreState === "degraded"
            onActivated: omihomo.repairCore()
          }

          ActionRow {
            width: parent.width
            section: "uninstall"
            title: root.uninstallArmed ? "Confirm uninstall" : "Uninstall mihomo"
            subtitle: root.uninstallArmed
              ? "Activate again to remove it. Esc cancels."
              : "Removes the core, its permissions, the unit, and saved subscriptions."
            urgentTrailing: root.uninstallArmed
            trailing: root.uninstallArmed ? "confirm" : ""
            onActivated: root.uninstallCore()
          }
        }

        Text {
          width: parent.width
          text: omihomo.permissionsOk
            ? "enter activate · b autostart · esc back"
            : "enter activate · b autostart · R repair · esc back"
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
        }
      }

      // ------------------------------------------------------------ main view
      //
      // The main view is the only one split in two: everything scrolls except
      // the control footer, which is pinned to the bottom of the popup so mode,
      // TUN, and the three views are reachable from anywhere in a long panel.

      Item {
        id: mainView
        anchors.fill: parent
        visible: root.view === "main"

        Flickable {
          id: panelFlick
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.top: parent.top
          anchors.bottom: controlFooter.visible ? controlFooter.top : parent.bottom
          anchors.bottomMargin: controlFooter.visible ? Style.space(12) : 0
          contentWidth: width
          contentHeight: column.implicitHeight
          clip: true
          boundsBehavior: Flickable.StopAtBounds
          flickableDirection: Flickable.VerticalFlick
          interactive: contentHeight > height
          ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

          Column {
            id: column
            width: panelFlick.width
            spacing: Style.space(12)

            // ---- hero -------------------------------------------------------

            Item {
              id: header
              width: parent.width
              implicitHeight: hero.implicitHeight
              readonly property bool ringVisible: root.at("power")

              PanelHero {
                id: hero
                width: parent.width
                title: omihomo.activeSubscription !== "" ? omihomo.activeSubscription : "Omihomo"
                meta: omihomo.coreDetail !== "" ? Model.stateLabel(omihomo.coreState) + " · " + omihomo.coreDetail
                  : Model.stateLabel(omihomo.coreState)
                foreground: root.foreground
                fontFamily: root.fontFamily
                iconOpacity: omihomo.coreActive ? 1.0 : 0.5
                iconComponent: Component {
                  OmihomoIcon {
                    iconSize: Style.font.display
                    color: omihomo.coreActive ? root.foreground : root.dim
                    badgeColor: root.urgent
                    crossed: omihomo.installed && !omihomo.coreActive
                    warning: omihomo.coreState === "degraded" || !omihomo.installed
                    tunnelled: omihomo.tunActive && omihomo.coreActive
                  }
                }

                trailingControl: Component {
                  ToggleSwitch {
                    id: powerSwitch
                    visible: omihomo.installed
                    checked: omihomo.coreActive
                    busy: omihomo.busy
                    hasCursor: header.ringVisible
                    foreground: hero.foreground
                    onHovered: function(on) { if (on) root.focusRow("power") }
                    onToggled: omihomo.toggleCore()

                    PanelToolTip {
                      visible: powerSwitch.containsMouse
                      text: omihomo.coreActive ? "Stop mihomo" : "Start mihomo"
                      fontFamily: hero.fontFamily
                    }
                  }
                }
              }
            }

            Text {
              visible: omihomo.actionStatus !== "" || omihomo.lastError !== ""
              width: parent.width
              text: omihomo.actionStatus !== "" ? omihomo.actionStatus : omihomo.lastError
              color: omihomo.lastError !== "" && omihomo.actionStatus === "" ? root.urgent : root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }

            // ---- core is not installed --------------------------------------

            Column {
              visible: !omihomo.installed
              width: parent.width
              spacing: Style.space(10)

              Text {
                width: parent.width
                text: "The mihomo core is not installed. Installing builds it from the AUR, so it "
                  + "runs in a terminal."
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                wrapMode: Text.WordWrap
              }

              ActionRow {
                width: parent.width
                section: "install"
                title: "Install mihomo"
                trailing: "i"
                onActivated: root.installCore()
              }
            }

            // ---- status readout ---------------------------------------------

            Column {
              visible: omihomo.installed
              width: parent.width
              spacing: Style.space(2)

              StatRow { width: parent.width; label: "CONFIG"; value: root.configText }
              StatRow { width: parent.width; label: "TRAFFIC"; value: root.throughputText }
              StatRow { width: parent.width; label: "UPTIME"; value: root.uptimeText }
              StatRow {
                width: parent.width
                label: "EGRESS"
                section: "trace"
                value: root.egressText
                faded: omihomo.traceFailed && !omihomo.traceTesting
                onActivated: omihomo.refreshTrace()
              }
            }

            // ---- configs and groups -------------------------------------------

            PanelSeparator { visible: omihomo.installed; foreground: root.foreground }

            Column {
              id: configSection
              visible: omihomo.installed && root.liveReady && root.browsedGroupEntry !== null
              width: parent.width
              spacing: Style.space(4)

              Item {
                width: parent.width
                implicitHeight: configHeader.implicitHeight

                PanelSectionHeader {
                  id: configHeader
                  anchors.left: parent.left
                  text: root.browsedGroup.toUpperCase()
                  foreground: root.foreground
                  fontFamily: root.fontFamily
                }

                Text {
                  anchors.right: parent.right
                  anchors.verticalCenter: configHeader.verticalCenter
                  text: root.visibleConfigs.length + (root.browsedGroupEntry
                    && root.visibleConfigs.length !== root.browsedGroupEntry.all.length
                    ? " of " + root.browsedGroupEntry.all.length : "")
                  color: root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption
                }
              }

              TextField {
                id: configFilterField
                visible: root.filterOpen
                width: parent.width
                foreground: root.foreground
                placeholderText: "Filter configs"
                verticalPadding: Style.space(4)
                onAccepted: root.endEdit()
                onActiveFocusChanged: root.fieldFocusChanged(configFilterField, activeFocus)
                Keys.onEscapePressed: root.closeFilter()
              }

              ListView {
                id: configList
                width: parent.width
                height: Math.min(contentHeight, Style.space(300))
                spacing: Style.space(2)
                clip: true
                boundsBehavior: Flickable.StopAtBounds
                interactive: contentHeight > height
                ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

                model: root.visibleConfigs
                onCurrentIndexChanged: if (currentIndex >= 0) Qt.callLater(keepCurrentVisible)
                function keepCurrentVisible() {
                  if (currentIndex >= 0) positionViewAtIndex(currentIndex, ListView.Contain)
                }

                delegate: Item {
                  required property var modelData
                  required property int index

                  width: ListView.view.width
                  height: configRow.implicitHeight

                  ConfigRow {
                    id: configRow
                    width: parent.width
                    configName: String(modelData)
                    rowIndex: index
                  }
                }
              }

              Text {
                width: parent.width
                text: root.browsedGroupEntry && root.browsedGroupEntry.selectable
                  ? "enter select · d test · D test group · / filter"
                  : root.browsedGroup + " picks its own config · d test · D test group"
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
              }
            }

            Column {
              id: groupSection
              visible: omihomo.installed
              width: parent.width
              spacing: Style.space(4)

              PanelSectionHeader {
                text: "GROUPS"
                foreground: root.foreground
                fontFamily: root.fontFamily
              }

              Text {
                visible: !root.liveReady
                width: parent.width
                text: omihomo.activeSubscription === "" ? "Activate a subscription to browse groups."
                  : (omihomo.coreRunning ? "mihomo controller is unreachable." : "Start mihomo to browse groups.")
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
              }

              Repeater {
                model: root.liveReady ? root.visibleGroups : []
                GroupRow {
                  required property var modelData
                  required property int index
                  width: groupSection.width
                  group: modelData
                  rowIndex: index
                }
              }
            }

            // ---- subscriptions ------------------------------------------------

            PanelSeparator { visible: omihomo.installed; foreground: root.foreground }

            Column {
              id: subscriptionSection
              visible: omihomo.installed
              width: parent.width
              spacing: Style.space(4)

              PanelSectionHeader {
                text: "SUBSCRIPTIONS"
                foreground: root.foreground
                fontFamily: root.fontFamily
              }

              Repeater {
                model: omihomo.subscriptions
                SubscriptionRow {
                  required property var modelData
                  required property int index
                  width: subscriptionSection.width
                  subscription: modelData
                  rowIndex: index
                }
              }

              ActionRow {
                visible: !root.subFormOpen
                width: parent.width
                section: "subAdd"
                title: "Add subscription"
                trailing: "a"
                onActivated: root.openSubForm()
              }

              Column {
                visible: root.subFormOpen
                width: parent.width
                spacing: Style.space(4)

                FieldRow {
                  id: subUrlRow
                  width: parent.width
                  section: "subUrl"
                  label: "URL"
                  placeholder: "https://…"
                  onSubmitted: root.submitSubForm()
                }

                ActionRow {
                  width: parent.width
                  section: "subSubmit"
                  title: "Fetch and add"
                  trailing: root.subFormUrl === "" ? "incomplete" : "enter"
                  onActivated: root.submitSubForm()
                }
              }
            }
          }
        }

        // ---- controls -----------------------------------------------------
        //
        // The panel ends on one line: the two pieces of state that are changed
        // in place on the left, the three views that own everything else on the
        // right. Repair lives in the manage view, so a degraded core is surfaced
        // on the cell that leads there.

        Column {
          id: controlFooter
          visible: omihomo.installed
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          spacing: Style.space(8)

          PanelSeparator { foreground: root.foreground }

          RowLayout {
            width: parent.width
            spacing: 0

            ControlCell {
              section: "mode"
              label: "MODE"
              value: omihomo.effectiveMode === "" ? "—" : omihomo.effectiveMode
              tooltip: "Cycle mode · m"
              onActivated: omihomo.setMode(Model.nextMode(omihomo.effectiveMode))
            }

            ControlCell {
              section: "tun"
              label: "TUN"
              value: omihomo.tunActive ? "on" : "off"
              dimValue: !omihomo.tunActive
              tooltip: "Toggle TUN · t"
              onActivated: omihomo.toggleTun()
            }

            Item { Layout.fillWidth: true; Layout.preferredHeight: 1 }

            ControlCell {
              section: "rules"
              value: "rules"
              label: String(omihomo.rules.length)
              labelFirst: false
              tooltip: "Your rules · n"
              onActivated: root.openRules(false)
            }

            ControlCell {
              section: "connections"
              value: "conns"
              label: String(omihomo.openConnectionCount)
              labelFirst: false
              tooltip: "Connections · c"
              onActivated: root.openConnections()
            }

            ControlCell {
              section: "manage"
              value: "manage"
              labelFirst: false
              urgentValue: omihomo.coreState === "degraded"
              tooltip: omihomo.coreState === "degraded" ? "Needs repair · M" : "Manage · M"
              onActivated: root.openManage()
            }
          }
        }
      }

      // ------------------------------------------------------------ rules view

      Flickable {
        id: rulesFlick
        anchors.fill: parent
        contentWidth: width
        contentHeight: rulesColumn.implicitHeight
        clip: true
        visible: root.view === "rules"
        boundsBehavior: Flickable.StopAtBounds
        flickableDirection: Flickable.VerticalFlick
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: rulesColumn
          width: rulesFlick.width
          spacing: Style.space(10)

          Item {
            width: parent.width
            implicitHeight: Math.max(rulesTitle.implicitHeight, rulesTotals.implicitHeight)

            Text {
              id: rulesTitle
              anchors.left: parent.left
              anchors.verticalCenter: parent.verticalCenter
              text: "Rules"
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.title
              font.bold: true
            }

            Text {
              id: rulesTotals
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              text: omihomo.rules.length + " yours · " + omihomo.subscriptionRules.length + " inherited"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
          }

          PanelSeparator { foreground: root.foreground }

          Column {
            id: ruleSection
            width: parent.width
            spacing: Style.space(4)

            PanelSectionHeader {
              text: "YOUR RULES"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }

            Text {
              width: parent.width
              text: "Checked before the subscription's."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }

            Repeater {
              model: omihomo.rules
              RuleRow {
                required property var modelData
                required property int index
                width: ruleSection.width
                rule: modelData
                rowIndex: index
              }
            }

            ActionRow {
              visible: !root.ruleFormOpen
              width: parent.width
              section: "ruleAdd"
              title: "Add rule"
              trailing: "n"
              onActivated: root.openRuleForm()
            }

            Column {
              visible: root.ruleFormOpen
              width: parent.width
              spacing: Style.space(4)

              ActionRow {
                width: parent.width
                section: "ruleType"
                title: "Type"
                trailing: root.ruleType
                current: true
                onActivated: root.ruleTypeIndex = (root.ruleTypeIndex + 1) % Model.RULE_TYPES.length
              }

              FieldRow {
                id: ruleValueRow
                width: parent.width
                section: "ruleValue"
                label: "VALUE"
                placeholder: root.ruleType === "IP-CIDR" ? "10.0.0.0/8" : "example.com"
                onSubmitted: root.submitRuleForm()
              }

              ActionRow {
                width: parent.width
                section: "ruleTarget"
                title: "Target"
                trailing: root.ruleTarget
                current: true
                onActivated: root.ruleTargetIndex = (root.ruleTargetIndex + 1) % root.ruleTargets.length
              }

              ActionRow {
                width: parent.width
                section: "ruleSubmit"
                title: "Add rule"
                trailing: root.ruleFormError !== "" ? root.ruleFormError : "enter"
                urgentTrailing: root.ruleFormError !== ""
                onActivated: root.submitRuleForm()
              }
            }
          }

          Column {
            id: effectiveSection
            visible: omihomo.subscriptionRules.length > 0
            width: parent.width
            spacing: Style.space(4)

            PanelSectionHeader {
              text: "SUBSCRIPTION RULES"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }

            Repeater {
              model: omihomo.subscriptionRules.slice(0, 40)
              Text {
                required property var modelData
                width: effectiveSection.width
                text: modelData.type + "  " + modelData.value + (modelData.value === "" ? "" : "  ") + "→ " + modelData.target
                color: Qt.darker(root.dim, 1.25)
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                elide: Text.ElideRight
              }
            }

            Text {
              visible: omihomo.subscriptionRules.length > 40
              width: parent.width
              text: "+" + (omihomo.subscriptionRules.length - 40) + " more"
              color: Qt.darker(root.dim, 1.25)
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
          }

          Text {
            width: parent.width
            text: "enter activate · x delete · n new rule · esc back"
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
          }
        }
      }
    }
  }

  // ---------------------------------------------------------------- rows

  // One row shape for everything the panel navigates: a title on the left, a
  // value or key hint on the right, and the shared cursor chrome.
  component ActionRow: CursorSurface {
    id: actionRow

    property string section: ""
    property int rowIndex: -1
    property string title: ""
    property string subtitle: ""
    property string trailing: ""
    property bool urgentTrailing: false
    property bool faded: false

    signal activated()

    hasCursor: root.at(actionRow.section, actionRow.rowIndex < 0 ? undefined : actionRow.rowIndex)
    foreground: root.foreground
    fill: root.hoverFill
    currentFill: root.selectedFill
    implicitHeight: rowLabels.implicitHeight + Style.spacing.lg

    Component.onCompleted: if (actionRow.section !== "")
      root.registerRowAnchor(root.rowKey(actionRow.section, actionRow.rowIndex < 0 ? undefined : actionRow.rowIndex), actionRow)

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow(actionRow.section,
        actionRow.rowIndex < 0 ? undefined : actionRow.rowIndex)
      onClicked: actionRow.activated()
    }

    RowLayout {
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(8)
      spacing: Style.space(8)

      ColumnLayout {
        id: rowLabels
        Layout.fillWidth: true
        spacing: 0

        Text {
          Layout.fillWidth: true
          text: actionRow.title
          color: actionRow.faded ? root.dim : root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          elide: Text.ElideRight
        }

        Text {
          Layout.fillWidth: true
          visible: actionRow.subtitle !== ""
          text: actionRow.subtitle
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }

      Text {
        visible: actionRow.trailing !== ""
        text: actionRow.trailing
        color: actionRow.urgentTrailing ? root.urgent : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }

  // One cell of the control footer. State cells lead with their label
  // ("MODE rule"); the cells that open a view lead with the value and trail a
  // count ("rules 4"), so the two halves of the footer stay tellable apart.
  component ControlCell: CursorSurface {
    id: controlCell

    property string section: ""
    property string label: ""
    property string value: ""
    property bool labelFirst: true
    property bool dimValue: false
    property bool urgentValue: false
    property string tooltip: ""

    signal activated()

    hasCursor: root.at(controlCell.section)
    foreground: root.foreground
    fill: root.hoverFill
    implicitWidth: cellRow.implicitWidth + Style.space(16)
    implicitHeight: cellRow.implicitHeight + Style.spacing.md

    // No row anchor: the footer is pinned, so a cell is never scrolled to.

    MouseArea {
      id: cellMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow(controlCell.section)
      onClicked: controlCell.activated()
    }

    Row {
      id: cellRow
      anchors.centerIn: parent
      spacing: Style.space(6)
      layoutDirection: controlCell.labelFirst ? Qt.LeftToRight : Qt.RightToLeft

      Text {
        visible: controlCell.label !== ""
        anchors.verticalCenter: parent.verticalCenter
        text: controlCell.label
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        font.bold: true
        font.letterSpacing: 1.2
      }

      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: controlCell.value
        color: controlCell.urgentValue ? root.urgent : (controlCell.dimValue ? root.dim : root.foreground)
        font.family: root.fontFamily
        font.pixelSize: Style.font.body
      }
    }

    PanelToolTip {
      visible: controlCell.tooltip !== "" && cellMouse.containsMouse
      text: controlCell.tooltip
      fontFamily: root.fontFamily
    }
  }

  // Label-and-value line of the status readout. `section` makes it a cursor
  // target; the egress line is the only one that has an action behind it.
  component StatRow: CursorSurface {
    id: statRow

    property string label: ""
    property string value: ""
    property string section: ""
    property bool faded: false

    signal activated()

    hasCursor: statRow.section !== "" && root.at(statRow.section)
    foreground: root.foreground
    fill: root.hoverFill
    implicitHeight: statValue.implicitHeight + Style.spacing.sm

    Component.onCompleted: if (statRow.section !== "") root.registerRowAnchor(root.rowKey(statRow.section), statRow)

    MouseArea {
      anchors.fill: parent
      enabled: statRow.section !== ""
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow(statRow.section)
      onClicked: statRow.activated()
    }

    Text {
      id: statLabel
      anchors.left: parent.left
      anchors.leftMargin: Style.space(8)
      anchors.verticalCenter: parent.verticalCenter
      width: Style.space(64)
      text: statRow.label
      color: root.dim
      font.family: root.fontFamily
      font.pixelSize: Style.font.caption
      font.bold: true
      font.letterSpacing: 1.2
    }

    Text {
      id: statValue
      anchors.left: statLabel.right
      anchors.right: parent.right
      anchors.rightMargin: Style.space(8)
      anchors.verticalCenter: parent.verticalCenter
      text: statRow.value
      color: statRow.faded ? root.dim : root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.body
      elide: Text.ElideRight
    }
  }

  // A labelled text input the panel cursor can land on. The field owns its own
  // text — Enter submits, Esc hands the keyboard back to the panel — and the
  // cursor highlight follows the panel exactly like any other row.
  component FieldRow: Item {
    id: fieldRow

    property string section: ""
    property string label: ""
    property string placeholder: ""
    property alias field: input

    signal submitted()

    implicitHeight: input.implicitHeight

    Component.onCompleted: root.registerRowAnchor(root.rowKey(fieldRow.section), fieldRow)

    Text {
      id: fieldLabel
      anchors.left: parent.left
      anchors.leftMargin: Style.space(8)
      anchors.verticalCenter: parent.verticalCenter
      width: Style.space(64)
      text: fieldRow.label
      color: root.dim
      font.family: root.fontFamily
      font.pixelSize: Style.font.caption
      font.bold: true
      font.letterSpacing: 1.2
    }

    TextField {
      id: input
      anchors.left: fieldLabel.right
      anchors.right: parent.right
      anchors.rightMargin: Style.space(8)
      anchors.verticalCenter: parent.verticalCenter
      foreground: root.foreground
      placeholderText: fieldRow.placeholder
      hasCursor: root.at(fieldRow.section)
      verticalPadding: Style.space(4)
      onAccepted: fieldRow.submitted()
      onActiveFocusChanged: root.fieldFocusChanged(input, activeFocus)
      Keys.onEscapePressed: root.endEdit()
    }
  }

  component SubscriptionRow: CursorSurface {
    id: subRow

    property var subscription: null
    property int rowIndex: 0
    readonly property string subName: subscription ? String(subscription.name) : ""
    readonly property bool pending: subName !== "" && omihomo.pendingSubscription === subName

    hasCursor: root.at("sub", subRow.rowIndex)
    current: subscription && subscription.active === true
    foreground: root.foreground
    fill: root.hoverFill
    currentFill: root.selectedFill
    implicitHeight: Math.max(subLabels.implicitHeight, subUpdate.implicitHeight) + Style.spacing.lg

    Component.onCompleted: root.registerRowAnchor(root.rowKey("sub", subRow.rowIndex), subRow)

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow("sub", subRow.rowIndex)
      onClicked: root.activateSubscriptionAt(subRow.rowIndex)
    }

    RowLayout {
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(8)

      ColumnLayout {
        id: subLabels
        Layout.fillWidth: true
        spacing: 0

        Text {
          Layout.fillWidth: true
          text: subRow.subName
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          font.bold: subRow.current
          elide: Text.ElideRight
        }

        Text {
          Layout.fillWidth: true
          text: subRow.pending ? "working…" : Model.subscriptionDetail(subRow.subscription, root.nowMs)
          visible: text !== ""
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }

      PanelActionButton {
        id: subUpdate
        iconText: "󰑐"
        tooltipText: "Update"
        foreground: root.foreground
        fontFamily: root.fontFamily
        enabled: !subRow.pending
        Layout.alignment: Qt.AlignVCenter
        onClicked: omihomo.updateSubscription(subRow.subName)
      }

      PanelActionButton {
        iconText: "󰅙"
        tooltipText: "Remove"
        foreground: root.foreground
        hoverColor: root.urgent
        fontFamily: root.fontFamily
        Layout.alignment: Qt.AlignVCenter
        onClicked: omihomo.removeSubscription(subRow.subName)
      }
    }
  }

  component GroupRow: CursorSurface {
    id: groupRow

    property var group: null
    property int rowIndex: 0
    readonly property string groupName: group ? String(group.name) : ""
    readonly property bool primary: groupName !== "" && groupName === omihomo.primaryGroup
    readonly property bool browsed: groupName === root.browsedGroup

    hasCursor: root.at("group", groupRow.rowIndex)
    current: browsed
    foreground: root.foreground
    fill: root.hoverFill
    currentFill: root.selectedFill
    implicitHeight: groupLabels.implicitHeight + Style.spacing.lg

    Component.onCompleted: root.registerRowAnchor(root.rowKey("group", groupRow.rowIndex), groupRow)

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow("group", groupRow.rowIndex)
      onClicked: root.browseGroupAt(groupRow.rowIndex)
    }

    RowLayout {
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(8)

      ColumnLayout {
        id: groupLabels
        Layout.fillWidth: true
        spacing: 0

        Text {
          Layout.fillWidth: true
          text: groupRow.groupName
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          font.bold: groupRow.primary
          elide: Text.ElideRight
        }

        Text {
          Layout.fillWidth: true
          text: (groupRow.group ? groupRow.group.type : "") + " · " + (groupRow.group ? groupRow.group.now : "")
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }

      Text {
        visible: groupRow.primary
        text: "primary"
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }

      PanelActionButton {
        visible: !groupRow.primary
        iconText: "󰄬"
        tooltipText: "Make primary"
        foreground: root.foreground
        fontFamily: root.fontFamily
        Layout.alignment: Qt.AlignVCenter
        onClicked: omihomo.setPrimaryGroup(groupRow.groupName)
      }
    }
  }

  component ConfigRow: CursorSurface {
    id: configRow

    property string configName: ""
    property int rowIndex: 0
    readonly property var entry: omihomo.configEntries[configRow.configName] || null
    readonly property bool selected: root.browsedGroupEntry && root.browsedGroupEntry.now === configRow.configName
    readonly property bool pending: omihomo.pendingConfig === configRow.configName
    readonly property string testState: omihomo.configTestState(configRow.configName)
    readonly property int delay: omihomo.configDelay(configRow.configName)
    readonly property bool expanded: root.expandedConfig === configRow.configName

    hasCursor: root.at("config", configRow.rowIndex)
    current: selected || pending
    foreground: root.foreground
    fill: root.hoverFill
    currentFill: root.selectedFill
    implicitHeight: configColumn.implicitHeight + Style.spacing.md

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow("config", configRow.rowIndex)
      onClicked: root.chooseConfigAt(configRow.rowIndex)
    }

    Column {
      id: configColumn
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(2)

      RowLayout {
        width: parent.width
        spacing: Style.space(8)

        Text {
          Layout.fillWidth: true
          text: configRow.configName
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          font.bold: configRow.selected
          elide: Text.ElideRight
        }

        Text {
          text: configRow.testState === "testing" ? "testing…"
            : (configRow.testState === "failed" ? "failed" : Model.formatDelay(configRow.delay))
          color: configRow.testState === "failed" ? root.urgent
            : (configRow.delay > 0 && configRow.testState !== "testing" ? root.foreground : root.dim)
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
        }

        PanelActionButton {
          iconText: "󰓅"
          tooltipText: "Test latency"
          foreground: root.foreground
          fontFamily: root.fontFamily
          size: Style.space(18)
          enabled: !omihomo.pingTestsRunning
          Layout.alignment: Qt.AlignVCenter
          onClicked: omihomo.testConfig(configRow.configName)
        }

        PanelActionButton {
          iconText: "󰋼"
          tooltipText: "Parameters"
          foreground: root.foreground
          fontFamily: root.fontFamily
          size: Style.space(18)
          Layout.alignment: Qt.AlignVCenter
          onClicked: root.toggleConfigParameters(configRow.configName)
        }
      }

      // Best effort by design: mihomo exposes what it knows about a running
      // config, and research ticket #4 ruled out mapping a name back to its
      // provider entry, so address and credentials are never guessed at.
      Text {
        visible: configRow.expanded
        width: parent.width
        text: {
          var parameters = Model.configParameters(configRow.entry)
          if (parameters.length === 0) return "No parameters available."
          var parts = []
          for (var i = 0; i < parameters.length; i++) parts.push(parameters[i].label + " " + parameters[i].value)
          return parts.join(" · ")
        }
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
      }
    }
  }

  component RuleRow: CursorSurface {
    id: ruleRow

    property var rule: null
    property int rowIndex: 0

    hasCursor: root.at("rule", ruleRow.rowIndex)
    foreground: root.foreground
    fill: root.hoverFill
    implicitHeight: Math.max(ruleText.implicitHeight, ruleDelete.implicitHeight) + Style.spacing.md

    Component.onCompleted: root.registerRowAnchor(root.rowKey("rule", ruleRow.rowIndex), ruleRow)

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.ArrowCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow("rule", ruleRow.rowIndex)
    }

    RowLayout {
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(8)

      Text {
        id: ruleText
        Layout.fillWidth: true
        text: ruleRow.rule
          ? ruleRow.rule.type + "  " + ruleRow.rule.value + "  → " + ruleRow.rule.target
          : ""
        color: root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        elide: Text.ElideRight
      }

      Text {
        visible: ruleRow.rule && ruleRow.rule.kind !== "prepend"
        text: ruleRow.rule ? ruleRow.rule.kind : ""
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }

      PanelActionButton {
        id: ruleDelete
        iconText: "󰅙"
        tooltipText: "Delete rule"
        foreground: root.foreground
        hoverColor: root.urgent
        fontFamily: root.fontFamily
        size: Style.space(18)
        Layout.alignment: Qt.AlignVCenter
        onClicked: if (ruleRow.rule) omihomo.removeRule(ruleRow.rule.index)
      }
    }
  }

  // At 420px a stack cannot state everything on one line, so the destination
  // takes the first and its route, transfer, and age share the second: the
  // route elides, the numbers never do. The leading mark and the brightness
  // are what say open or closed, because the panel has a foreground, a dim,
  // and an urgent, and no colour to spare for a third state.
  component ConnectionRow: CursorSurface {
    id: connectionRow

    property var stack: null
    property int rowIndex: 0

    readonly property bool live: !!stack && stack.open
    readonly property color labelColor: connectionRow.live ? root.foreground : root.dim

    hasCursor: root.at("conn", connectionRow.rowIndex)
    foreground: root.foreground
    fill: root.hoverFill
    implicitHeight: connectionLabels.implicitHeight + Style.spacing.md

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow("conn", connectionRow.rowIndex)
      onClicked: root.activateConnectionAt(connectionRow.rowIndex)
    }

    RowLayout {
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(8)

      Text {
        text: connectionRow.live ? "●" : "○"
        color: connectionRow.labelColor
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        Layout.alignment: Qt.AlignVCenter
      }

      ColumnLayout {
        id: connectionLabels
        Layout.fillWidth: true
        spacing: 0

        Text {
          Layout.fillWidth: true
          text: {
            if (!connectionRow.stack) return ""
            var stack = connectionRow.stack
            return stack.process === "" ? stack.host : stack.process + " → " + stack.host
          }
          color: connectionRow.labelColor
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          elide: Text.ElideRight
        }

        RowLayout {
          Layout.fillWidth: true
          spacing: Style.space(8)

          // A closed stack spends the slot the rule would have taken on when
          // it closed, which is the thing you read a log for.
          Text {
            Layout.fillWidth: true
            text: {
              if (!connectionRow.stack) return ""
              var stack = connectionRow.stack
              var parts = []
              if (stack.network !== "") parts.push(stack.network)
              if (stack.chain !== "") parts.push(stack.chain)
              if (connectionRow.live) {
                if (stack.rule !== "") parts.push(stack.rule)
              } else {
                parts.push("closed " + Model.relativeSince(stack.closedAtMs, root.nowMs))
              }
              return parts.join(" · ")
            }
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            elide: Text.ElideRight
          }

          Text {
            text: {
              if (!connectionRow.stack) return ""
              var stack = connectionRow.stack
              var transfer = "↓ " + Model.formatBytes(stack.download)
                + "  ↑ " + Model.formatBytes(stack.upload)
              return connectionRow.live
                ? transfer + " · " + Model.formatDuration(root.nowMs - stack.startMs)
                : transfer
            }
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
          }
        }
      }

      Text {
        text: connectionRow.stack ? "×" + connectionRow.stack.count : ""
        color: connectionRow.labelColor
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        Layout.alignment: Qt.AlignVCenter
      }

      PanelActionButton {
        iconText: connectionRow.live ? "󰅙" : "󰩹"
        tooltipText: connectionRow.live ? "Close connections" : "Remove from log"
        foreground: root.foreground
        hoverColor: root.urgent
        fontFamily: root.fontFamily
        size: Style.space(18)
        Layout.alignment: Qt.AlignVCenter
        onClicked: root.activateConnectionAt(connectionRow.rowIndex)
      }
    }
  }
}
