import QtQuick
import QtQuick.Layouts
import Quickshell.Io
import qs.Commons
import qs.Ui
import "components"
import "views"
import "Model.js" as Model

Panel {
  id: root

  moduleName: "dev.readeem.omihomo"
  ipcTarget: "dev.readeem.omihomo"
  manageIpc: false

  property string view: "configs"
  readonly property var tabViews: ["configs", "subs", "rules", "conns"]

  property bool cursorActive: false
  property int cursor: 0

  property string browseGroup: ""
  property bool filterOpen: false
  readonly property string configFilter: filterOpen ? configsView.filterField.text : ""

  property string removeArmed: ""
  property bool uninstallArmed: false
  property int ruleTypeIndex: 0
  property int ruleTargetIndex: 0
  property bool targetPicking: false

  readonly property string subFormUrl: subscriptionsView.urlField.text.trim()
  readonly property string ruleFormValue: rulesView.valueField.text
  readonly property string ruleQuery: rulesView.testField.text

  property Item editing: null

  property double nowMs: Date.now()

  readonly property Theme theme: Theme { bar: root.bar }
  readonly property Service service: omihomo
  readonly property color barIconColor: omihomo.coreActive ? barForeground : Qt.darker(barForeground, 1.55)

  readonly property string installerPath: decodeURIComponent(String(Qt.resolvedUrl("../bin/omihomo-install")).replace(/^file:\/\//, ""))
  // Without a daemon to talk to, the panel is one card saying why, and the
  // command that fixes it, run in a terminal since it asks for a password.
  readonly property var setup: {
    switch (omihomo.link) {
    case "absent": return { text: "The Omihomo core is not installed. Installing it from the AUR and claiming it for this account runs in a terminal.",
      button: "Install core", command: installerPath }
    case "outdated": return { text: "The Omihomo core is older than this widget. Updating it runs in a terminal.",
      button: "Update core", command: "yay -S omihomo-bin" }
    case "newer": return { text: "The Omihomo core is newer than this widget. Update the Omihomo plugin.",
      button: "", command: "" }
    case "refused": return { text: "Another account owns the Omihomo core. Claiming it for this account runs in a terminal.",
      button: "Claim core", command: "sudo /usr/bin/omihomo claim" }
    }
    return { text: "Connecting to the Omihomo core…", button: "", command: "" }
  }
  readonly property bool coreAlert: omihomo.link === "outdated" || omihomo.link === "newer"
    || omihomo.link === "refused"

  readonly property bool liveReady: omihomo.apiReady
  readonly property bool tunRedirectBlocked: omihomo.coreState === "degraded"
    && omihomo.tunRedirectActive
  readonly property bool compactHeader: view === "rules" || view === "conns" || view === "settings"

  readonly property var visibleGroups: Model.userGroups(omihomo.groups)
  readonly property string browsedGroup: Model.groupByName(visibleGroups, browseGroup)
    ? browseGroup : omihomo.primaryGroup
  readonly property var browsedGroupEntry: Model.groupByName(visibleGroups, browsedGroup)
  readonly property var visibleConfigs: browsedGroupEntry
    ? Model.filterNames(browsedGroupEntry.all, configFilter) : []
  readonly property var delaySpan: Model.delaySpan(browsedGroupEntry
    ? browsedGroupEntry.all.map(function(name) { return omihomo.configDelay(name) }) : [])
  readonly property string testedText: {
    var at = omihomo.groupTestedAt[browsedGroup]
    return at ? "tested " + Model.relativeSince(at, nowMs) : "not tested yet"
  }

  readonly property var ruleTargets: Model.ruleTargets(visibleGroups)
  readonly property var visibleTargets: {
    var needle = targetPicking ? rulesView.targetField.text.trim().toLowerCase() : ""
    return ruleTargets.filter(function(target) { return target.value.toLowerCase().indexOf(needle) !== -1 })
  }
  readonly property string ruleType: Model.RULE_TYPES[Math.max(0, Math.min(ruleTypeIndex, Model.RULE_TYPES.length - 1))]
  readonly property string ruleTarget: ruleTargets[Math.max(0, Math.min(ruleTargetIndex, ruleTargets.length - 1))].value
  readonly property string ruleFormError: Model.validateRule(ruleType, ruleFormValue, ruleTarget)

  readonly property var ownHeadRules: omihomo.rules.filter(function(rule) { return rule.kind === "prepend" })
  readonly property var effectiveRules: ownHeadRules.concat(omihomo.injectedRules, omihomo.subscriptionRules)
  readonly property var ruleTest: Model.testRules(effectiveRules, ruleQuery, omihomo.routing)
  readonly property string ruleHitTarget: ruleTest.hit >= 0 ? effectiveRules[ruleTest.hit].target : ""
  readonly property var ruleTally: Model.ruleTargetTally(effectiveRules)
  readonly property var ruleEntries: {
    var out = [{ kind: "head", title: "YOURS · " + omihomo.rules.length, note: "checked first" }]
    var head = 0
    var i
    for (i = 0; i < omihomo.rules.length; i++) {
      var rule = omihomo.rules[i]
      out.push({ kind: "own", s: "rule", i: i, rule: rule, eff: rule.kind === "prepend" ? head++ : -1 })
    }
    if (omihomo.injectedRules.length > 0) {
      out.push({ kind: "head", title: "OMIHOMO · " + omihomo.injectedRules.length, note: "tailscale · read-only" })
      for (i = 0; i < omihomo.injectedRules.length; i++)
        out.push({ kind: "injected", s: "irule", i: i, rule: omihomo.injectedRules[i], eff: head++ })
    }
    out.push({
      kind: "head",
      title: (omihomo.activeSubscription !== "" ? omihomo.activeSubscription.toUpperCase() : "SUBSCRIPTION")
        + " · " + omihomo.subscriptionRules.length,
      note: "read-only"
    })
    for (i = 0; i < omihomo.subscriptionRules.length; i++)
      out.push({ kind: "sub", s: "srule", i: i, rule: omihomo.subscriptionRules[i], eff: head + i })
    return out
  }

  readonly property var flows: Model.connectionFlows(omihomo.connectionStacks, 6, 4)
  readonly property double largestStack: {
    var most = 0
    for (var i = 0; i < omihomo.connectionStacks.length; i++) {
      var stack = omihomo.connectionStacks[i]
      most = Math.max(most, stack.download + stack.upload)
    }
    return most
  }
  readonly property int closedLogged: omihomo.connectionLog.length - omihomo.openConnectionCount

  readonly property string statusText: {
    var parts = [Model.stateLabel(omihomo.coreState).toUpperCase()]
    if (omihomo.coreRunning) {
      if (omihomo.effectiveMode !== "") parts.push(omihomo.effectiveMode + " mode")
      if (omihomo.tunActive) parts.push("TUN")
    }
    if (omihomo.coreDetail !== "") parts.push(omihomo.coreDetail)
    return parts.join(" · ")
  }
  readonly property string uptimeText: omihomo.startedMs > 0 && omihomo.coreRunning
    ? Model.formatDuration(nowMs - omihomo.startedMs) : "—"
  readonly property string egressText: omihomo.traceTesting ? "testing…"
    : (omihomo.traceFailed ? "failed" : (omihomo.egressIp === "" ? "—" : omihomo.egressIp))
  readonly property string latencyText: omihomo.traceTesting || omihomo.traceFailed ? "—"
    : Model.formatDelay(omihomo.egressLatency)

  readonly property bool errorInView: omihomo.lastError !== "" && (view === "subs"
    ? omihomo.errorScope.indexOf("sub:") === 0 : omihomo.errorScope === view)
  readonly property bool headerError: omihomo.lastError !== "" && !errorInView
  readonly property string headerMessage: headerError ? omihomo.lastError
    : omihomo.actionStatus
  readonly property bool subscriptionFailed: omihomo.lastError !== "" && omihomo.errorScope.indexOf("sub:") === 0

  function errorAt(scope) {
    return omihomo.lastError !== "" && omihomo.errorScope === scope
  }

  function shortRuleType(type) {
    var t = String(type)
    if (t === "DOMAIN-SUFFIX") return "suffix"
    if (t === "DOMAIN-KEYWORD") return "keyword"
    if (t === "IP-CIDR" || t === "IP-CIDR6") return "cidr"
    if (t === "PROCESS-NAME") return "process"
    return t.toLowerCase()
  }

  function navRowsFor() {
    var rows = []
    var i
    if (!omihomo.installed) {
      if (setup.command !== "") rows.push({ s: "install" })
      return rows
    }
    switch (view) {
    case "configs":
      if (liveReady) {
        for (i = 0; i < visibleGroups.length; i++) rows.push({ s: "group", i: i })
        for (i = 0; i < visibleConfigs.length; i++) rows.push({ s: "config", i: i })
      }
      break
    case "subs":
      for (i = 0; i < omihomo.subscriptions.length; i++) rows.push({ s: "sub", i: i })
      rows.push({ s: "subUrl" }, { s: "subSubmit" })
      break
    case "rules":
      if (targetPicking) {
        for (i = 0; i < visibleTargets.length; i++) rows.push({ s: "target", i: i })
        break
      }
      rows.push({ s: "ruleTest" })
      for (i = 0; i < omihomo.rules.length; i++) rows.push({ s: "rule", i: i })
      for (i = 0; i < omihomo.injectedRules.length; i++) rows.push({ s: "irule", i: i })
      for (i = 0; i < omihomo.subscriptionRules.length; i++) rows.push({ s: "srule", i: i })
      rows.push({ s: "ruleType" }, { s: "ruleValue" }, { s: "ruleTarget" }, { s: "ruleSubmit" })
      break
    case "conns":
      for (i = 0; i < omihomo.connectionStacks.length; i++) rows.push({ s: "conn", i: i })
      break
    case "settings":
      rows.push({ s: "mode" }, { s: "tun" }, { s: "tunRedirect" })
      if (omihomo.tailscalePresent) rows.push({ s: "tailscale" }, { s: "tailnetProxy" })
      rows.push({ s: "uninstall" })
      break
    }
    return rows
  }

  readonly property var navRows: navRowsFor()
  readonly property var cursorRow: cursor >= 0 && cursor < navRows.length ? navRows[cursor] : null
  onCursorRowChanged: {
    if (uninstallArmed && (!cursorRow || cursorRow.s !== "uninstall")) uninstallArmed = false
    if (removeArmed !== "") {
      var sub = cursorRow && cursorRow.s === "sub" ? subscriptionAt(cursorRow.i) : null
      if (!sub || sub.name !== removeArmed) removeArmed = ""
    }
  }

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

  function adjustCursorRow(delta) {
    if (!cursorRow) return
    switch (cursorRow.s) {
    case "group":
    case "config": cycleGroup(delta); break
    case "mode": cycleMode(delta); break
    case "tun": omihomo.toggleTun(); break
    case "tunRedirect": omihomo.toggleTunRedirect(); break
    case "tailscale": omihomo.toggleTailscale(); break
    case "tailnetProxy": omihomo.toggleTailnetProxy(); break
    case "ruleType": cycleRuleType(delta); break
    case "ruleTarget": cycleRuleTarget(delta); break
    }
  }

  function activateCursor() {
    if (!cursorRow) return
    switch (cursorRow.s) {
    case "install": installCore(); break
    case "group": browseGroupAt(cursorRow.i); break
    case "config": chooseConfigAt(cursorRow.i); break
    case "sub": activateSubscriptionAt(cursorRow.i); break
    case "subUrl": beginEdit(subscriptionsView.urlField); break
    case "subSubmit": submitSubForm(); break
    case "ruleTest": beginEdit(rulesView.testField); break
    case "ruleType": cycleRuleType(1); break
    case "ruleValue": beginEdit(rulesView.valueField); break
    case "ruleTarget": openTargetPicker(); break
    case "target": pickTargetAt(cursorRow.i); break
    case "ruleSubmit": submitRuleForm(); break
    case "conn": activateConnectionAt(cursorRow.i); break
    case "mode": cycleMode(1); break
    case "tun": omihomo.toggleTun(); break
    case "tunRedirect": omihomo.toggleTunRedirect(); break
    case "tailscale": omihomo.toggleTailscale(); break
    case "tailnetProxy": omihomo.toggleTailnetProxy(); break
    case "uninstall": uninstallCore(); break
    }
  }

  function deleteCursorRow() {
    if (!cursorRow) return
    if (cursorRow.s === "sub") {
      var sub = subscriptionAt(cursorRow.i)
      if (sub) removeSubscription(sub.name)
    } else if (cursorRow.s === "rule") {
      var rule = omihomo.rules[cursorRow.i]
      if (rule) omihomo.removeRule(rule.index)
    } else if (cursorRow.s === "conn") {
      activateConnectionAt(cursorRow.i)
    }
  }

  function handleTextKey(key) {
    if (!omihomo.installed) {
      if (key.toLowerCase() === "i") installCore()
      return
    }
    var tab = ["1", "2", "3", "4"].indexOf(key)
    if (tab >= 0) {
      openView(tabViews[tab])
      return
    }
    switch (key) {
    case "M": openView(view === "settings" ? "configs" : "settings"); return
    case "s": omihomo.toggleCore(); return
    case "t": omihomo.toggleTun(); return
    case "m": cycleMode(1); return
    case "c": openView("conns"); return
    case "n": openRuleForm(); return
    case "r": omihomo.refresh(); omihomo.refreshTrace(); return
    }
    if (view === "configs") {
      if (key === "d") testSelectedConfig()
      else if (key === "D") omihomo.testGroup(browsedGroup)
      else if (key === "g") cycleGroup(1)
      else if (key === "p") omihomo.setPrimaryGroup(browsedGroup)
      else if (key === "/") openFilter()
    } else if (view === "subs") {
      if (key === "u") updateSelectedSubscription()
      else if (key === "a") { focusRow("subUrl"); beginEdit(subscriptionsView.urlField) }
    } else if (view === "rules") {
      if (key === "/" && targetPicking) beginEdit(rulesView.targetField)
      else if (key === "/") { focusRow("ruleTest"); beginEdit(rulesView.testField) }
    } else if (view === "conns") {
      if (key === "A") omihomo.closeAllConnections()
      else if (key === "L") omihomo.clearConnectionLog()
    } else if (view === "settings") {
      if (key === "A") omihomo.toggleTunRedirect()
      else if (key === "T") omihomo.toggleTailscale()
      else if (key === "P") omihomo.toggleTailnetProxy()
    }
  }

  function handleEscape() {
    if (editing) { endEdit(); return }
    if (removeArmed !== "") { removeArmed = ""; return }
    if (uninstallArmed) { uninstallArmed = false; return }
    if (filterOpen) { closeFilter(); return }
    if (targetPicking) { closeTargetPicker(); return }
    if (view !== "configs") { openView("configs"); return }
    close()
  }

  function runHint(key) {
    if (key === "enter") { cursorActive = true; activateCursor() }
    else if (key === "x") { cursorActive = true; deleteCursorRow() }
    else if (key === "esc") handleEscape()
    else if (key === "j") moveCursor(0, 1)
    else handleTextKey(key)
  }

  function hintsFor(name) {
    if (name === "rules" && targetPicking) return [
      { keys: "↵", label: "pick", key: "enter" }, { keys: "/", label: "filter", key: "/" },
      { keys: "j k", label: "move", key: "j" }, { keys: "esc", label: "cancel", key: "esc" }]
    switch (name) {
    case "configs": return [
      { keys: "↵", label: "select", key: "enter" }, { keys: "d", label: "test", key: "d" },
      { keys: "D", label: "test all", key: "D" }, { keys: "g", label: "group", key: "g" },
      { keys: "s", label: "core", key: "s" }]
    case "subs": return [
      { keys: "↵", label: "activate", key: "enter" }, { keys: "u", label: "update", key: "u" },
      { keys: "a", label: "add", key: "a" }, { keys: "x", label: "remove", key: "x" },
      { keys: "j k", label: "move", key: "j" }]
    case "rules": return [
      { keys: "/", label: "test", key: "/" }, { keys: "n", label: "new", key: "n" },
      { keys: "x", label: "delete", key: "x" }, { keys: "j k", label: "move", key: "j" },
      { keys: "esc", label: "back", key: "esc" }]
    case "conns": return [
      { keys: "↵", label: "close", key: "enter" }, { keys: "A", label: "close all", key: "A" },
      { keys: "L", label: "clear log", key: "L" }, { keys: "esc", label: "back", key: "esc" }]
    case "settings": return [
      { keys: "↵", label: "toggle", key: "enter" }, { keys: "m", label: "mode", key: "m" },
      { keys: "t", label: "TUN", key: "t" }, { keys: "A", label: "accel", key: "A" },
      { keys: "M", label: "back", key: "M" }]
    }
    return []
  }

  function openView(name) {
    if (editing) {
      editing = null
      endEdit()
    }
    view = name
    cursor = 0
    cursorActive = false
    removeArmed = ""
    uninstallArmed = false
    if (filterOpen) closeFilter()
    if (targetPicking) closeTargetPicker()
    if (name === "configs" && browsedGroupEntry) {
      var at = visibleConfigs.indexOf(browsedGroupEntry.now)
      if (at >= 0) {
        focusRow("config", at)
        cursorActive = false
        Qt.callLater(function() { configsView.list.positionViewAtIndex(at, ListView.Contain) })
      }
    }
  }

  function subscriptionAt(index) {
    var list = omihomo.subscriptions
    return list.length === 0 ? null : list[Math.max(0, Math.min(index, list.length - 1))]
  }

  function connectionAt(index) {
    var list = omihomo.connectionStacks
    return list.length === 0 ? null : list[Math.max(0, Math.min(index, list.length - 1))]
  }

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

  function removeSubscription(name) {
    if (removeArmed !== name) {
      removeArmed = name
      return
    }
    removeArmed = ""
    omihomo.removeSubscription(name)
  }

  function browseGroupAt(index) {
    var group = visibleGroups[index]
    if (!group) return
    browseGroup = group.name
    configsView.filterField.text = ""
  }

  function cycleGroup(delta) {
    if (visibleGroups.length === 0) return
    var index = -1
    for (var i = 0; i < visibleGroups.length; i++) if (visibleGroups[i].name === browsedGroup) index = i
    browseGroupAt((index + delta + visibleGroups.length) % visibleGroups.length)
  }

  function cycleMode(delta) {
    var index = Model.MODES.indexOf(omihomo.effectiveMode)
    var next = (index < 0 ? 0 : index + delta + Model.MODES.length) % Model.MODES.length
    omihomo.setMode(Model.MODES[next])
  }

  function cycleRuleType(delta) {
    ruleTypeIndex = (ruleTypeIndex + delta + Model.RULE_TYPES.length) % Model.RULE_TYPES.length
  }

  function cycleRuleTarget(delta) {
    ruleTargetIndex = (ruleTargetIndex + delta + ruleTargets.length) % ruleTargets.length
  }

  function openTargetPicker() {
    rulesView.targetField.text = ""
    targetPicking = true
    cursorActive = false
    beginEdit(rulesView.targetField)
  }

  function closeTargetPicker() {
    targetPicking = false
    rulesView.targetField.text = ""
    endEdit()
    focusRow("ruleTarget")
  }

  function pickTargetAt(index) {
    var target = visibleTargets[index]
    if (target) ruleTargetIndex = ruleTargets.indexOf(target)
    closeTargetPicker()
  }

  function chooseConfigAt(index) {
    var name = visibleConfigs[index]
    if (name && browsedGroupEntry && browsedGroupEntry.selectable) omihomo.selectConfig(browsedGroup, name)
  }

  function testSelectedConfig() {
    if (cursorRow && cursorRow.s === "config") omihomo.testConfig(visibleConfigs[cursorRow.i])
    else if (omihomo.currentConfig !== "") omihomo.testConfig(omihomo.currentConfig)
  }

  function installCore() {
    if (!bar || setup.command === "") return
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote(setup.command))
    close()
  }

  function uninstallCore() {
    if (!bar) return
    if (!uninstallArmed) {
      focusRow("uninstall")
      uninstallArmed = true
      return
    }
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote("sudo pacman -R omihomo-bin"))
    close()
  }

  function submitSubForm() {
    if (subFormUrl === "") {
      focusRow("subUrl")
      beginEdit(subscriptionsView.urlField)
      return
    }
    omihomo.addSubscription(subFormUrl)
    subscriptionsView.urlField.text = ""
    endEdit()
  }

  function openRuleForm() {
    if (view !== "rules") openView("rules")
    omihomo.clearMessages()
    Qt.callLater(function() {
      root.focusRow("ruleValue")
      root.beginEdit(rulesView.valueField)
    })
  }

  function submitRuleForm() {
    if (ruleFormError !== "") {
      focusRow("ruleValue")
      beginEdit(rulesView.valueField)
      return
    }
    omihomo.addRule(ruleType, ruleFormValue, ruleTarget)
  }

  function openFilter() {
    configsView.filterField.text = ""
    filterOpen = true
    beginEdit(configsView.filterField)
  }

  function closeFilter() {
    filterOpen = false
    configsView.filterField.text = ""
    endEdit()
  }

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

  function scrollCursorIntoView() {
    if (!cursorRow) return
    var list = null
    var index = cursorRow.i
    switch (cursorRow.s) {
    case "config": list = configsView.list; break
    case "sub": list = subscriptionsView.list; break
    case "rule":
    case "irule":
    case "srule":
      list = rulesView.ruleList
      index = ruleEntries.findIndex(function(entry) { return entry.s === cursorRow.s && entry.i === cursorRow.i })
      break
    case "conn": list = connectionsView.list; break
    case "target": list = rulesView.targetList; break
    }
    if (list) Qt.callLater(function() { list.positionViewAtIndex(index, ListView.Contain) })
    else if (view === "settings") settingsView.reveal(cursorRow.s)
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: {
    if (opened) {
      openView("configs")
      rulesView.valueField.text = ""
      editing = null
      omihomo.clearMessages()
      Qt.callLater(function() { keyCatcher.forceActiveFocus() })
    }
  }

  onNavRowsChanged: clampCursor()

  Connections {
    target: omihomo
    function onErrorScopeChanged() {
      var scope = omihomo.errorScope
      if (root.view !== "subs" || scope.indexOf("sub:") !== 0 || scope === "sub:") return
      for (var i = 0; i < omihomo.subscriptions.length; i++) {
        if ("sub:" + omihomo.subscriptions[i].name !== scope) continue
        var index = i
        Qt.callLater(function() { subscriptionsView.list.positionViewAtIndex(index, ListView.Contain) })
        return
      }
    }
  }

  Service {
    id: omihomo
    panelOpen: root.opened
    onConfigChanged: omihomo.refreshTrace()
    onRuleAdded: {
      rulesView.valueField.text = ""
      root.endEdit()
    }
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
    function start(): string { if (!omihomo.coreActive) omihomo.toggleCore(); return "ok" }
    function stop(): string { if (omihomo.coreActive) omihomo.toggleCore(); return "ok" }
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
          crossed: omihomo.installed && !omihomo.coreActive
          alertColor: root.coreAlert ? root.theme.urgent : "transparent"
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
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.cappedContentHeight(Style.space(720))
    padding: 0
    borderSpec: Border.flat(root.theme.outline, 1)

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

      ColumnLayout {
        anchors.fill: parent
        spacing: 0

        ColumnLayout {
          Layout.fillWidth: true
          Layout.margins: Style.space(14)
          Layout.topMargin: Style.space(root.compactHeader ? 10 : 14)
          Layout.bottomMargin: Style.space(10)
          spacing: Style.space(8)

          RowLayout {
            Layout.fillWidth: true
            spacing: Style.space(12)

            Rectangle {
              implicitWidth: Style.space(root.compactHeader ? 26 : 34)
              implicitHeight: implicitWidth
              radius: width / 2
              color: root.theme.soft

              OmihomoIcon {
                anchors.centerIn: parent
                iconSize: Style.space(root.compactHeader ? 16 : 18)
                color: omihomo.coreActive ? root.theme.accent : root.theme.faint
                crossed: omihomo.installed && !omihomo.coreActive
              }
            }

            ColumnLayout {
              Layout.fillWidth: true
              spacing: Style.space(2)

              PanelText {
                theme: root.theme
                Layout.fillWidth: true
                text: omihomo.activeSubscription !== "" ? omihomo.activeSubscription : "Omihomo"
                color: root.theme.foreground
                font.pixelSize: root.compactHeader ? Style.font.subtitle : Style.font.heading
                font.bold: true
                elide: Text.ElideRight
              }

              RowLayout {
                Layout.fillWidth: true
                spacing: Style.space(6)

                Rectangle {
                  implicitWidth: Style.space(6)
                  implicitHeight: Style.space(6)
                  radius: width / 2
                  color: omihomo.coreState === "degraded" ? root.theme.urgent
                    : (omihomo.coreRunning ? root.theme.accent : root.theme.faint)
                }

                PanelText {
                  theme: root.theme
                  Layout.fillWidth: true
                  text: root.compactHeader && omihomo.coreRunning
                    ? Model.stateLabel(omihomo.coreState).toLowerCase() + " · " + omihomo.effectiveMode
                    : root.statusText
                  color: omihomo.coreState === "degraded" ? root.theme.urgent : root.theme.dim
                  elide: Text.ElideRight
                }
              }
            }

            Column {
              opacity: omihomo.coreActive ? 1 : 0.35
              visible: root.compactHeader && omihomo.installed
              spacing: 0

              PanelText {
                theme: root.theme
                text: "↓ " + Model.formatRate(omihomo.downloadRate)
                color: root.theme.foreground
              }

              PanelText {
                theme: root.theme
                text: "↑ " + Model.formatRate(omihomo.uploadRate)
                color: root.theme.accent
              }
            }

            TrafficGraph {
              theme: root.theme
              samples: omihomo.trafficHistory
              capacity: omihomo.trafficHistoryCap
              opacity: omihomo.coreActive ? 1 : 0.35
              visible: root.compactHeader && omihomo.installed
              Layout.preferredWidth: Style.space(70)
              Layout.preferredHeight: Style.space(24)
            }

            PanelSwitch {
              id: powerSwitch
              theme: root.theme
              trackWidth: Style.space(root.compactHeader ? 36 : 44)
              trackHeight: Style.space(root.compactHeader ? 20 : 24)
              visible: omihomo.installed
              checked: omihomo.coreActive
              busy: omihomo.busy
              foreground: root.theme.foreground
              onToggled: omihomo.toggleCore()

              PanelToolTip {
                visible: powerSwitch.containsMouse
                text: (omihomo.coreActive ? "Stop mihomo" : "Start mihomo") + " · s"
                fontFamily: root.theme.fontFamily
              }
            }
          }

          RowLayout {
            visible: !root.compactHeader && omihomo.installed
            opacity: omihomo.coreActive ? 1 : 0.35
            Layout.fillWidth: true
            spacing: Style.space(8)
            Layout.topMargin: Style.space(4)

            PanelText {
              theme: root.theme
              text: "↓ " + Model.formatRate(omihomo.downloadRate)
              color: root.theme.foreground
              font.pixelSize: Style.font.body
            }

            PanelText {
              theme: root.theme
              Layout.fillWidth: true
              text: "↑ " + Model.formatRate(omihomo.uploadRate)
              color: root.theme.accent
              font.pixelSize: Style.font.body
            }
          }

          TrafficGraph {
            theme: root.theme
            samples: omihomo.trafficHistory
            capacity: omihomo.trafficHistoryCap
            opacity: omihomo.coreActive ? 1 : 0.35
            visible: !root.compactHeader && omihomo.installed
            Layout.fillWidth: true
            Layout.preferredHeight: Style.space(44)
            Layout.topMargin: -Style.space(4)
          }

          RowLayout {
            visible: !root.compactHeader && omihomo.installed
            Layout.fillWidth: true
            spacing: Style.space(8)

            Readout { theme: root.theme; live: omihomo.coreActive; Layout.horizontalStretchFactor: 1; label: "UPTIME"; value: root.uptimeText }
            Readout {
              theme: root.theme
              live: omihomo.coreActive
              Layout.horizontalStretchFactor: 1
              label: "EGRESS"
              value: root.egressText
              faded: omihomo.traceFailed && !omihomo.traceTesting
              tooltip: "Check again · r"
              onActivated: omihomo.refreshTrace()
            }
            Readout {
              theme: root.theme
              live: omihomo.coreActive
              Layout.horizontalStretchFactor: 1
              label: "LATENCY"
              value: root.latencyText
              faded: omihomo.traceFailed && !omihomo.traceTesting
              tooltip: "Check again · r"
              onActivated: omihomo.refreshTrace()
            }
          }
        }

        PanelText {
          theme: root.theme
          visible: root.headerMessage !== ""
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(14)
          Layout.rightMargin: Style.space(14)
          Layout.bottomMargin: Style.space(10)
          text: (root.headerError ? "▲ " : "") + root.headerMessage
          color: root.headerError ? root.theme.urgent : root.theme.dim
          wrapMode: Text.WordWrap
        }

        ColumnLayout {
          visible: !omihomo.installed
          Layout.fillWidth: true
          Layout.fillHeight: true
          Layout.margins: Style.space(14)
          spacing: Style.space(10)

          PanelText {
            theme: root.theme
            Layout.fillWidth: true
            text: root.setup.text
            color: root.theme.dim
            font.pixelSize: Style.font.body
            wrapMode: Text.WordWrap
          }

          SmallButton {
            theme: root.theme
            visible: root.setup.button !== ""
            text: root.setup.button + " · i"
            hasCursor: root.at("install")
            onHovered: function(on) { if (on) root.focusRow("install") }
            onClicked: root.installCore()
          }

          Item { Layout.fillHeight: true }
        }

        Rectangle {
          visible: root.compactHeader
          Layout.fillWidth: true
          implicitHeight: 1
          color: root.theme.line
        }

        Rectangle {
          visible: omihomo.installed
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(14)
          Layout.rightMargin: Style.space(14)
          Layout.topMargin: Style.space(2)
          implicitHeight: Style.space(54)
          radius: root.theme.cardRadius
          color: root.theme.fill
          border.width: 1
          border.color: root.theme.line

          RowLayout {
            anchors.fill: parent
            anchors.margins: Style.space(4)
            spacing: Style.space(4)

          ViewTab { panel: root; target: "configs"; number: 1; label: "Configs"; count: root.browsedGroupEntry ? String(root.browsedGroupEntry.all.length) : "" }
          ViewTab { panel: root; target: "subs"; number: 2; label: "Subs"; count: root.subscriptionFailed && root.view !== "subs" ? "!" : String(omihomo.subscriptions.length); alert: root.subscriptionFailed && root.view !== "subs" }
          ViewTab { panel: root; target: "rules"; number: 3; label: "Rules"; count: String(root.effectiveRules.length) }
          ViewTab { panel: root; target: "conns"; number: 4; label: "Conns"; count: String(omihomo.openConnectionCount) }

          ViewTab {
            panel: root
            target: "settings"
            Layout.fillWidth: false
            Layout.preferredWidth: Style.space(40)
            label: "󰒓"
            gear: true
          }
          }
        }

        Item {
          visible: omihomo.installed
          Layout.fillWidth: true
          Layout.fillHeight: true

          Layout.leftMargin: Style.space(14)
          Layout.rightMargin: Style.space(14)
          Layout.topMargin: Style.space(12)
          Layout.bottomMargin: Style.space(10)

          ConfigsView {
            id: configsView
            panel: root
            anchors.fill: parent
            visible: root.view === "configs"
          }

          SubscriptionsView {
            id: subscriptionsView
            panel: root
            anchors.fill: parent
            visible: root.view === "subs"
          }

          RulesView {
            id: rulesView
            panel: root
            anchors.fill: parent
            visible: root.view === "rules"
          }

          ConnectionsView {
            id: connectionsView
            panel: root
            anchors.fill: parent
            visible: root.view === "conns"
          }

          SettingsView {
            id: settingsView
            panel: root
            anchors.fill: parent
            visible: root.view === "settings"
          }
        }

        Rectangle {
          visible: omihomo.installed
          Layout.fillWidth: true
          implicitHeight: Style.space(36)
          color: "transparent"
          clip: true

          Rectangle {
            anchors.top: parent.top
            width: parent.width
            height: 1
            color: root.theme.line
          }

          Row {
            anchors.verticalCenter: parent.verticalCenter
            anchors.left: parent.left
            anchors.leftMargin: Style.space(14)
            spacing: Style.space(11)

            Repeater {
              model: root.hintsFor(root.view)
              HintButton {
                required property var modelData
                theme: root.theme
                keys: modelData.keys
                label: modelData.label
                onClicked: root.runHint(modelData.key)
              }
            }
          }
        }
      }
    }
  }
}
