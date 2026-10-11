import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import QtQuick.Shapes as Shapes
import Quickshell.Io
import qs.Commons
import qs.Commons as Commons
import qs.Ui
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
  readonly property string configFilter: filterOpen ? configFilterField.text : ""

  property string removeArmed: ""
  property bool uninstallArmed: false
  property int ruleTypeIndex: 0
  property int ruleTargetIndex: 0
  property bool targetPicking: false

  readonly property string subFormUrl: subUrlField.text.trim()
  readonly property string ruleFormValue: ruleValueField.text
  readonly property string ruleQuery: ruleTestField.text

  property Item editing: null

  property double nowMs: Date.now()

  readonly property color foreground: bar ? bar.foreground : Commons.Color.foreground
  readonly property color urgent: bar ? bar.urgent : Commons.Color.urgent
  readonly property color accent: Commons.Color.accent
  readonly property color dim: Util.alpha(foreground, 0.58)
  readonly property color faint: Util.alpha(foreground, 0.34)
  readonly property color line: Util.alpha(foreground, 0.11)
  readonly property color surface: Commons.Color.popups.background
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property color hoverFill: Style.hoverFillFor(foreground, accent)
  readonly property color soft: Util.alpha(accent, 0.16)
  readonly property color fill: Util.alpha(foreground, 0.05)
  readonly property color outline: Util.alpha(foreground, 0.22)
  readonly property real controlRadius: Math.min(Style.cornerRadius, Style.space(7))
  readonly property real cardRadius: Math.min(Style.cornerRadius, Style.space(10))
  readonly property bool needsRepair: omihomo.installed && (!omihomo.permissionsOk || omihomo.hardeningPending)
  readonly property string repairLabel: omihomo.permissionsOk ? "apply security update" : "repair permissions"
  readonly property color barIconColor: omihomo.coreActive ? barForeground : Qt.darker(barForeground, 1.55)

  readonly property string pluginDir: decodeURIComponent(String(Qt.resolvedUrl(".")).replace(/^file:\/\//, ""))
  readonly property string cliPath: pluginDir + "bin/omihomo"

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
    var needle = targetPicking ? targetFilterField.text.trim().toLowerCase() : ""
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
  readonly property bool repairNotice: needsRepair && view !== "settings" && !headerError && omihomo.actionStatus === ""
  readonly property string headerMessage: headerError ? omihomo.lastError
    : (repairNotice ? (omihomo.permissionsOk ? "mihomo needs a security update · R" : "mihomo's root permissions need repair · R") : omihomo.actionStatus)
  readonly property bool subscriptionFailed: omihomo.lastError !== "" && omihomo.errorScope.indexOf("sub:") === 0

  function css(c) {
    return "rgba(" + Math.round(c.r * 255) + "," + Math.round(c.g * 255) + "," + Math.round(c.b * 255) + "," + c.a + ")"
  }

  function errorAt(scope) {
    return omihomo.lastError !== "" && omihomo.errorScope === scope
  }

  function latencyFill(heat) {
    return Qt.tint(Util.alpha(foreground, 0.08), Util.alpha(urgent, 0.06 + 0.5 * heat))
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
      rows.push({ s: "install" })
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
      rows.push({ s: "mode" }, { s: "tun" }, { s: "tunRedirect" }, { s: "autostart" })
      if (omihomo.tailscalePresent) rows.push({ s: "tailscale" }, { s: "tailnetProxy" })
      if (root.needsRepair) rows.push({ s: "repair" })
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
    case "autostart": omihomo.toggleAutostart(); break
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
    case "subUrl": beginEdit(subUrlField); break
    case "subSubmit": submitSubForm(); break
    case "ruleTest": beginEdit(ruleTestField); break
    case "ruleType": cycleRuleType(1); break
    case "ruleValue": beginEdit(ruleValueField); break
    case "ruleTarget": openTargetPicker(); break
    case "target": pickTargetAt(cursorRow.i); break
    case "ruleSubmit": submitRuleForm(); break
    case "conn": activateConnectionAt(cursorRow.i); break
    case "mode": cycleMode(1); break
    case "tun": omihomo.toggleTun(); break
    case "tunRedirect": omihomo.toggleTunRedirect(); break
    case "autostart": omihomo.toggleAutostart(); break
    case "tailscale": omihomo.toggleTailscale(); break
    case "tailnetProxy": omihomo.toggleTailnetProxy(); break
    case "repair": omihomo.repairCore(); break
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
    case "R": omihomo.repairCore(); return
    case "r": omihomo.refresh(); omihomo.refreshLive(); omihomo.refreshTrace(); return
    }
    if (view === "configs") {
      if (key === "d") testSelectedConfig()
      else if (key === "D") omihomo.testGroup(browsedGroup)
      else if (key === "g") cycleGroup(1)
      else if (key === "p") omihomo.setPrimaryGroup(browsedGroup)
      else if (key === "/") openFilter()
    } else if (view === "subs") {
      if (key === "u") updateSelectedSubscription()
      else if (key === "a") { focusRow("subUrl"); beginEdit(subUrlField) }
    } else if (view === "rules") {
      if (key === "/" && targetPicking) beginEdit(targetFilterField)
      else if (key === "/") { focusRow("ruleTest"); beginEdit(ruleTestField) }
    } else if (view === "conns") {
      if (key === "A") omihomo.closeAllConnections()
      else if (key === "L") omihomo.clearConnectionLog()
    } else if (view === "settings") {
      if (key === "A") omihomo.toggleTunRedirect()
      else if (key === "b") omihomo.toggleAutostart()
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
      { keys: "t", label: "TUN", key: "t" }, { keys: "b", label: "login", key: "b" },
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
    if (name === "conns") omihomo.refreshConnections()
    if (name === "configs" && browsedGroupEntry) {
      var at = visibleConfigs.indexOf(browsedGroupEntry.now)
      if (at >= 0) {
        focusRow("config", at)
        cursorActive = false
        Qt.callLater(function() { configList.positionViewAtIndex(at, ListView.Contain) })
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
    configFilterField.text = ""
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
    targetFilterField.text = ""
    targetPicking = true
    cursorActive = false
    beginEdit(targetFilterField)
  }

  function closeTargetPicker() {
    targetPicking = false
    targetFilterField.text = ""
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
    if (!bar) return
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote(cliPath + " core install"))
    close()
  }

  function uninstallCore() {
    if (!bar) return
    if (!uninstallArmed) {
      focusRow("uninstall")
      uninstallArmed = true
      return
    }
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote(cliPath + " core uninstall"))
    close()
  }

  function submitSubForm() {
    if (subFormUrl === "") {
      focusRow("subUrl")
      beginEdit(subUrlField)
      return
    }
    omihomo.addSubscription(subFormUrl)
    subUrlField.text = ""
    endEdit()
  }

  function openRuleForm() {
    if (view !== "rules") openView("rules")
    omihomo.clearMessages()
    Qt.callLater(function() {
      root.focusRow("ruleValue")
      root.beginEdit(ruleValueField)
    })
  }

  function submitRuleForm() {
    if (ruleFormError !== "") {
      focusRow("ruleValue")
      beginEdit(ruleValueField)
      return
    }
    omihomo.addRule(ruleType, ruleFormValue, ruleTarget)
  }

  function openFilter() {
    configFilterField.text = ""
    filterOpen = true
    beginEdit(configFilterField)
  }

  function closeFilter() {
    filterOpen = false
    configFilterField.text = ""
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
    case "config": list = configList; break
    case "sub": list = subList; break
    case "rule":
    case "irule":
    case "srule":
      list = ruleList
      index = ruleEntries.findIndex(function(entry) { return entry.s === cursorRow.s && entry.i === cursorRow.i })
      break
    case "conn": list = connList; break
    case "target": list = targetList; break
    }
    if (list) Qt.callLater(function() { list.positionViewAtIndex(index, ListView.Contain) })
    else if (view === "settings") scrollSettingIntoView(settingItems[cursorRow.s] || null)
  }

  property var settingItems: ({})

  function scrollSettingIntoView(item) {
    if (!item) return
    Qt.callLater(function() {
      var top = item.mapToItem(settingsFlick.contentItem, 0, 0).y
      var bottom = top + item.height
      var maxY = Math.max(0, settingsFlick.contentHeight - settingsFlick.height)
      if (top < settingsFlick.contentY) settingsFlick.contentY = Math.max(0, top)
      else if (bottom > settingsFlick.contentY + settingsFlick.height)
        settingsFlick.contentY = Math.min(maxY, bottom - settingsFlick.height)
    })
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: {
    if (opened) {
      openView("configs")
      ruleValueField.text = ""
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
        Qt.callLater(function() { subList.positionViewAtIndex(index, ListView.Contain) })
        return
      }
    }
  }

  Service {
    id: omihomo
    settings: root.settings
    cliPath: root.cliPath
    panelOpen: root.opened
    connectionsOpen: root.opened && root.view === "conns"
    onConfigChanged: omihomo.refreshTrace()
    onRuleAdded: {
      ruleValueField.text = ""
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
          crossed: omihomo.installed && !omihomo.coreActive
          alertColor: root.needsRepair ? root.urgent : "transparent"
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
    borderSpec: Border.flat(root.outline, 1)

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
              color: root.soft

              OmihomoIcon {
                anchors.centerIn: parent
                iconSize: Style.space(root.compactHeader ? 16 : 18)
                color: omihomo.coreActive ? root.accent : root.faint
                crossed: omihomo.installed && !omihomo.coreActive
              }
            }

            ColumnLayout {
              Layout.fillWidth: true
              spacing: Style.space(2)

              PanelText {
                Layout.fillWidth: true
                text: omihomo.activeSubscription !== "" ? omihomo.activeSubscription : "Omihomo"
                color: root.foreground
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
                  color: omihomo.coreState === "degraded" ? root.urgent
                    : (omihomo.coreRunning ? root.accent : root.faint)
                }

                PanelText {
                  Layout.fillWidth: true
                  text: root.compactHeader && omihomo.coreRunning
                    ? Model.stateLabel(omihomo.coreState).toLowerCase() + " · " + omihomo.effectiveMode
                    : root.statusText
                  color: omihomo.coreState === "degraded" ? root.urgent : root.dim
                  elide: Text.ElideRight
                }
              }
            }

            Column {
              opacity: omihomo.coreActive ? 1 : 0.35
              visible: root.compactHeader && omihomo.installed
              spacing: 0

              PanelText {
                text: "↓ " + Model.formatRate(omihomo.downloadRate)
                color: root.foreground
              }

              PanelText {
                text: "↑ " + Model.formatRate(omihomo.uploadRate)
                color: root.accent
              }
            }

            TrafficGraph {
              opacity: omihomo.coreActive ? 1 : 0.35
              visible: root.compactHeader && omihomo.installed
              Layout.preferredWidth: Style.space(70)
              Layout.preferredHeight: Style.space(24)
            }

            PanelSwitch {
              id: powerSwitch
              trackWidth: Style.space(root.compactHeader ? 36 : 44)
              trackHeight: Style.space(root.compactHeader ? 20 : 24)
              visible: omihomo.installed
              checked: omihomo.coreActive
              busy: omihomo.busy
              foreground: root.foreground
              onToggled: omihomo.toggleCore()

              PanelToolTip {
                visible: powerSwitch.containsMouse
                text: (omihomo.coreActive ? "Stop mihomo" : "Start mihomo") + " · s"
                fontFamily: root.fontFamily
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
              text: "↓ " + Model.formatRate(omihomo.downloadRate)
              color: root.foreground
              font.pixelSize: Style.font.body
            }

            PanelText {
              Layout.fillWidth: true
              text: "↑ " + Model.formatRate(omihomo.uploadRate)
              color: root.accent
              font.pixelSize: Style.font.body
            }
          }

          TrafficGraph {
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

            Readout { Layout.horizontalStretchFactor: 1; label: "UPTIME"; value: root.uptimeText }
            Readout {
              Layout.horizontalStretchFactor: 1
              label: "EGRESS"
              value: root.egressText
              faded: omihomo.traceFailed && !omihomo.traceTesting
              tooltip: "Check again · r"
              onActivated: omihomo.refreshTrace()
            }
            Readout {
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
          visible: root.headerMessage !== ""
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(14)
          Layout.rightMargin: Style.space(14)
          Layout.bottomMargin: Style.space(10)
          text: (root.headerError || root.repairNotice ? "▲ " : "") + root.headerMessage
          color: root.headerError || root.repairNotice ? root.urgent : root.dim
          font.underline: root.repairNotice && repairMouse.containsMouse
          wrapMode: Text.WordWrap

          MouseArea {
            id: repairMouse
            anchors.fill: parent
            enabled: root.repairNotice
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: root.openView("settings")
          }
        }

        ColumnLayout {
          visible: !omihomo.installed
          Layout.fillWidth: true
          Layout.fillHeight: true
          Layout.margins: Style.space(14)
          spacing: Style.space(10)

          PanelText {
            Layout.fillWidth: true
            text: "The mihomo core is not installed. Installing builds it from the AUR, so it runs in a terminal."
            color: root.dim
            font.pixelSize: Style.font.body
            wrapMode: Text.WordWrap
          }

          SmallButton {
            text: "Install mihomo · i"
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
          color: root.line
        }

        Rectangle {
          visible: omihomo.installed
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(14)
          Layout.rightMargin: Style.space(14)
          Layout.topMargin: Style.space(2)
          implicitHeight: Style.space(54)
          radius: root.cardRadius
          color: root.fill
          border.width: 1
          border.color: root.line

          RowLayout {
            anchors.fill: parent
            anchors.margins: Style.space(4)
            spacing: Style.space(4)

          ViewTab { target: "configs"; number: 1; label: "Configs"; count: root.browsedGroupEntry ? String(root.browsedGroupEntry.all.length) : "" }
          ViewTab { target: "subs"; number: 2; label: "Subs"; count: root.subscriptionFailed && root.view !== "subs" ? "!" : String(omihomo.subscriptions.length); alert: root.subscriptionFailed && root.view !== "subs" }
          ViewTab { target: "rules"; number: 3; label: "Rules"; count: String(root.effectiveRules.length) }
          ViewTab { target: "conns"; number: 4; label: "Conns"; count: String(omihomo.openConnectionCount) }

          ViewTab {
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

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "configs"
            spacing: Style.space(8)
            PanelText {
              visible: !root.liveReady || (omihomo.proxiesLoaded && root.visibleGroups.length === 0)
              Layout.fillWidth: true
              text: !root.liveReady
                ? (omihomo.activeSubscription === "" ? "Add and activate a subscription in Subs to browse its configs."
                  : (omihomo.coreRunning ? "mihomo controller is unreachable." : "Start mihomo to browse configs."))
                : "This subscription declares no groups."
              color: root.dim
              font.pixelSize: Style.font.body
              wrapMode: Text.WordWrap
            }

            Flow {
              visible: root.liveReady
              Layout.fillWidth: true
              spacing: Style.space(6)

              Repeater {
                model: root.liveReady ? root.visibleGroups : []
                GroupChip {
                  required property var modelData
                  required property int index
                  group: modelData
                  rowIndex: index
                }
              }
            }

            RowLayout {
              visible: root.browsedGroupEntry !== null
              Layout.fillWidth: true
              Layout.topMargin: Style.space(4)
              spacing: Style.space(8)

              SectionLine {
                Layout.fillWidth: true
                title: root.browsedGroup.toUpperCase() + (root.browsedGroupEntry ? " · " + root.browsedGroupEntry.type.toUpperCase() : "")
                note: root.browsedGroupEntry && !root.browsedGroupEntry.selectable ? "picks its own config"
                  : root.visibleConfigs.length + (root.browsedGroupEntry && root.visibleConfigs.length !== root.browsedGroupEntry.all.length
                    ? " of " + root.browsedGroupEntry.all.length : "") + " configs"
              }

              SmallButton {
                visible: root.browsedGroup !== omihomo.primaryGroup
                text: "make primary"
                tooltipText: "The primary group's config is the current config · p"
                onClicked: omihomo.setPrimaryGroup(root.browsedGroup)
              }
            }

            PanelField {
              id: configFilterField
              visible: root.filterOpen
              Layout.fillWidth: true
              foreground: root.foreground
              placeholderText: "Filter configs"
              verticalPadding: Style.space(4)
              onAccepted: root.endEdit()
              onActiveFocusChanged: root.fieldFocusChanged(configFilterField, activeFocus)
              Keys.onEscapePressed: root.closeFilter()
            }

            ScrollList {
              id: configList
              Layout.fillWidth: true
              Layout.fillHeight: true
              spacing: Style.space(1)
              model: root.liveReady ? root.visibleConfigs : []

              delegate: ConfigRow {
                required property var modelData
                required property int index
                width: configList.rowWidth
                configName: String(modelData)
                rowIndex: index
              }
            }

            PanelText {
              visible: root.errorAt("configs")
              Layout.fillWidth: true
              text: "▲ " + omihomo.lastError
              color: root.urgent
              wrapMode: Text.WordWrap
            }

            RowLayout {
              visible: root.browsedGroupEntry !== null
              Layout.fillWidth: true
              spacing: Style.space(6)

              PanelText {
                text: omihomo.pingTestsRunning ? "testing…" : root.testedText
                color: root.dim
              }

              SmallButton {
                text: "test all"
                ghost: true
                horizontalPadding: Style.space(4)
                enabled: !omihomo.pingTestsRunning
                onClicked: omihomo.testGroup(root.browsedGroup)
              }

              Item { Layout.fillWidth: true }

              PanelText {
                text: "fast"
                color: root.faint
              }

              Repeater {
                model: [0, 1 / 3, 2 / 3, 1]
                Rectangle {
                  required property var modelData
                  implicitWidth: Style.space(16)
                  implicitHeight: Style.space(8)
                  radius: Math.min(Style.cornerRadius, 2)
                  color: root.latencyFill(modelData)
                }
              }

              Rectangle {
                implicitWidth: Style.space(14)
                implicitHeight: Style.space(8)
                radius: Math.min(Style.cornerRadius, 2)
                color: root.urgent
              }

              PanelText {
                text: "slow"
                color: root.faint
              }
            }
          }

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "subs"
            spacing: Style.space(8)

            SectionLine {
              Layout.fillWidth: true
              title: "SUBSCRIPTIONS"
              note: omihomo.subscriptions.length + (omihomo.activeSubscription !== "" ? " · active " + omihomo.activeSubscription : "")
            }

            PanelText {
              visible: omihomo.subscriptions.length === 0
              Layout.fillWidth: true
              text: "No subscriptions yet. Paste a URL below."
              color: root.dim
              font.pixelSize: Style.font.body
            }

            ScrollList {
              id: subList
              Layout.preferredHeight: contentHeight
              Layout.minimumHeight: 0
              Layout.fillWidth: true
              Layout.fillHeight: true
              Layout.maximumHeight: contentHeight
              spacing: Style.space(7)
              model: omihomo.subscriptions

              delegate: SubscriptionCard {
                required property var modelData
                required property int index
                width: subList.rowWidth
                subscription: modelData
                rowIndex: index
              }
            }

            SectionLine { Layout.fillWidth: true; title: "ADD" }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(6)

              PanelField {
                id: subUrlField
                Layout.fillWidth: true
                foreground: root.foreground
                placeholderText: "Paste a subscription URL"
                hasCursor: root.at("subUrl")
                verticalPadding: Style.space(4)
                onHoveredChanged: if (hovered) root.focusRow("subUrl")
                onAccepted: root.submitSubForm()
                onActiveFocusChanged: root.fieldFocusChanged(subUrlField, activeFocus)
                Keys.onEscapePressed: root.endEdit()
              }

              SmallButton {
                text: "+ add"
                hasCursor: root.at("subSubmit")
                onHovered: function(on) { if (on) root.focusRow("subSubmit") }
                onClicked: root.submitSubForm()
              }
            }

            PanelText {
              visible: root.errorAt("sub:")
              Layout.fillWidth: true
              text: "▲ " + omihomo.lastError
              color: root.urgent
              wrapMode: Text.WordWrap
            }
            Item { Layout.fillHeight: true }
          }

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "rules"
            spacing: Style.space(8)

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(8)

              PanelText {
                text: "test"
                color: root.dim
              }

              PanelField {
                id: ruleTestField
                highlighted: text !== ""
                Layout.fillWidth: true
                foreground: root.foreground
                placeholderText: "domain, URL, or IP address"
                hasCursor: root.at("ruleTest")
                verticalPadding: Style.space(4)
                onHoveredChanged: if (hovered) root.focusRow("ruleTest")
                onAccepted: root.endEdit()
                onActiveFocusChanged: root.fieldFocusChanged(ruleTestField, activeFocus)
                Keys.onEscapePressed: root.endEdit()
              }
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(6)

              PanelText {
                visible: root.ruleTest.hit >= 0 || root.ruleTest.bypassed
                text: root.ruleTest.bypassed ? "Outside VPN"
                  : (root.ruleTest.certain ? "→ " : "possibly → ") + root.ruleHitTarget
                color: root.accent
                font.pixelSize: Style.font.body
                font.bold: true
              }

              PanelText {
                Layout.fillWidth: true
                text: root.ruleTest.host === "" ? "Type a domain or an IP to see which rule catches it."
                  : (root.ruleTest.bypassed ? "Uses the system route · excluded by " + root.ruleTest.bypassCidr
                    : root.ruleTest.hit < 0 ? "No rule matches."
                    : "rule " + (root.ruleTest.hit + 1) + " of " + root.effectiveRules.length
                      + (root.ruleTest.unknown > 0 ? " · " + root.ruleTest.unknown + " above depend on DNS, rule sets, or the app" : ""))
                color: root.dim
                elide: Text.ElideRight
              }
            }
            Row {
              id: tallyBar
              visible: root.ruleTally.length > 0
              Layout.fillWidth: true
              Layout.preferredHeight: Style.space(8)
              spacing: Style.space(2)

              Repeater {
                model: root.ruleTally
                Rectangle {
                  required property var modelData
                  required property int index
                  width: Math.max(Style.space(2), (tallyBar.width - tallyBar.spacing * (root.ruleTally.length - 1))
                    * modelData.count / root.effectiveRules.length)
                  height: tallyBar.height
                  radius: Math.min(Style.cornerRadius, 2)
                  color: modelData.target === root.ruleHitTarget ? root.accent
                    : Util.alpha(root.foreground, [0.45, 0.32, 0.22, 0.15][index] || 0.1)
                }
              }
            }

            Flow {
              visible: root.ruleTally.length > 0
              Layout.fillWidth: true
              spacing: Style.space(10)

              Repeater {
                model: root.ruleTally.slice(0, 6)
                PanelText {
                  required property var modelData
                  text: modelData.count + " " + modelData.target
                  color: modelData.target === root.ruleHitTarget ? root.accent : root.dim
                }
              }
            }

            PanelField {
              id: targetFilterField
              visible: root.targetPicking
              Layout.fillWidth: true
              foreground: root.foreground
              placeholderText: "Filter targets"
              verticalPadding: Style.space(4)
              onAccepted: root.pickTargetAt(0)
              onActiveFocusChanged: root.fieldFocusChanged(targetFilterField, activeFocus)
              Keys.onEscapePressed: root.closeTargetPicker()
              Keys.onDownPressed: {
                root.endEdit()
                root.focusRow("target", 0)
              }
            }

            ScrollList {
              id: targetList
              visible: root.targetPicking
              Layout.fillWidth: true
              Layout.fillHeight: true
              spacing: Style.space(1)
              model: root.visibleTargets

              delegate: TargetRow {
                required property var modelData
                required property int index
                width: targetList.rowWidth
                target: modelData
                rowIndex: index
              }
            }

            ScrollList {
              id: ruleList
              visible: !root.targetPicking
              Layout.fillWidth: true
              Layout.fillHeight: true
              model: root.ruleEntries

              delegate: Item {
                id: ruleEntry
                required property var modelData
                width: ruleList.rowWidth
                height: modelData.kind === "head" ? ruleHead.implicitHeight + Style.space(8) : ruleBody.implicitHeight

                SectionLine {
                  id: ruleHead
                  visible: ruleEntry.modelData.kind === "head"
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.bottom: parent.bottom
                  anchors.bottomMargin: Style.space(2)
                  title: ruleEntry.modelData.title || ""
                  note: ruleEntry.modelData.note || ""
                }

                RuleRow {
                  id: ruleBody
                  visible: ruleEntry.modelData.kind !== "head"
                  width: parent.width
                  entry: ruleEntry.modelData
                }
              }
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(6)

              SmallButton {
                text: root.shortRuleType(root.ruleType) + " ▾"
                tooltipText: "Rule type · h l"
                hasCursor: root.at("ruleType")
                onHovered: function(on) { if (on) root.focusRow("ruleType") }
                onClicked: root.cycleRuleType(1)
              }

              PanelField {
                id: ruleValueField
                Layout.fillWidth: true
                foreground: root.foreground
                placeholderText: root.ruleType === "IP-CIDR" ? "10.0.0.0/8"
                  : (root.ruleType === "PROCESS-NAME" ? "process name" : "example.com")
                hasCursor: root.at("ruleValue")
                verticalPadding: Style.space(4)
                onHoveredChanged: if (hovered) root.focusRow("ruleValue")
                onAccepted: root.submitRuleForm()
                onActiveFocusChanged: root.fieldFocusChanged(ruleValueField, activeFocus)
                Keys.onEscapePressed: root.endEdit()
              }

              SmallButton {
                text: "→ " + root.ruleTarget + " ▾"
                tooltipText: "Target · ↵ pick · h l cycle"
                hasCursor: root.at("ruleTarget")
                onHovered: function(on) { if (on) root.focusRow("ruleTarget") }
                onClicked: root.openTargetPicker()
              }

              SmallButton {
                text: omihomo.ruleSaving ? "saving…" : "+ add"
                enabled: !omihomo.ruleSaving
                hasCursor: root.at("ruleSubmit")
                onHovered: function(on) { if (on) root.focusRow("ruleSubmit") }
                onClicked: root.submitRuleForm()
              }
            }

            PanelText {
              readonly property string message: root.ruleFormValue !== "" && root.ruleFormError !== ""
                ? root.ruleFormError : (root.errorAt("rules") ? omihomo.lastError : "")
              visible: message !== ""
              Layout.fillWidth: true
              text: "▲ " + message
              color: root.urgent
              wrapMode: Text.WordWrap
            }
          }

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "conns"
            spacing: Style.space(8)

            RowLayout {
              Layout.fillWidth: true

              PanelText {
                Layout.fillWidth: true
                text: "↓ " + Model.formatBytes(omihomo.connectionsDownload)
                  + "  ↑ " + Model.formatBytes(omihomo.connectionsUpload) + " total"
                color: root.foreground
              }

              PanelText {
                text: "process → config · width = bytes"
                color: root.faint
              }
            }

            FlowDiagram {
              Layout.fillWidth: true
              Layout.preferredHeight: Math.max(Style.space(44),
                Style.space(22) * Math.max(root.flows.sources.length, root.flows.targets.length))
            }

            SectionLine {
              Layout.fillWidth: true
              title: "OPEN · " + omihomo.openConnectionCount
              note: "bar = bytes, log scale"
            }

            PanelText {
              visible: omihomo.connectionStacks.length === 0
              Layout.fillWidth: true
              text: root.liveReady ? "Nothing logged yet." : "mihomo is not running."
              color: root.dim
              font.pixelSize: Style.font.body
            }

            ScrollList {
              id: connList
              Layout.fillWidth: true
              Layout.fillHeight: true
              spacing: Style.space(2)
              model: omihomo.connectionStacks

              delegate: Item {
                id: connectionEntry
                required property var modelData
                required property int index
                readonly property bool closedHead: !modelData.open
                  && (index === 0 || omihomo.connectionStacks[index - 1].open)
                width: connList.rowWidth
                height: connection.implicitHeight + (closedHead ? Style.space(28) : 0)

                SectionLine {
                  visible: connectionEntry.closedHead
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.top: parent.top
                  anchors.topMargin: Style.space(8)
                  title: "CLOSED · " + root.closedLogged
                }

                ConnectionRow {
                  id: connection
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.bottom: parent.bottom
                  height: implicitHeight
                  stack: connectionEntry.modelData
                  rowIndex: connectionEntry.index
                }
              }
            }

            PanelText {
              visible: root.errorAt("conns")
              Layout.fillWidth: true
              text: "▲ " + omihomo.lastError
              color: root.urgent
              wrapMode: Text.WordWrap
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(6)

              SmallButton {
                text: "close all"
                danger: true
                enabled: omihomo.openConnectionCount > 0
                onClicked: omihomo.closeAllConnections()
              }

              SmallButton {
                text: "clear log"
                enabled: root.closedLogged > 0
                onClicked: omihomo.clearConnectionLog()
              }
            }
          }

          Flickable {
            id: settingsFlick
            anchors.fill: parent
            visible: root.view === "settings"
            contentWidth: width
            contentHeight: settingsColumn.implicitHeight
            clip: true
            boundsBehavior: Flickable.StopAtBounds
            interactive: contentHeight > height
            ScrollBar.vertical: ScrollBar { policy: settingsFlick.interactive ? ScrollBar.AlwaysOn : ScrollBar.AsNeeded }

            ColumnLayout {
              id: settingsColumn
              width: settingsFlick.width - (settingsFlick.interactive ? Style.space(10) : 0)
              spacing: Style.space(6)

              PanelText {
                visible: root.errorAt("settings")
                Layout.fillWidth: true
                text: "▲ " + omihomo.lastError
                color: root.urgent
                wrapMode: Text.WordWrap
              }

              SectionLine {
                Layout.fillWidth: true
                title: "ROUTING"
                note: "applies immediately"
              }

              Rectangle {
                Layout.fillWidth: true
                implicitHeight: routingColumn.implicitHeight + Style.space(6)
                radius: root.cardRadius
                color: root.fill
                border.width: 1
                border.color: root.line
                ColumnLayout {
                  id: routingColumn
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.margins: Style.space(3)
                  spacing: 0
                  SettingRow {
                    Layout.fillWidth: true
                    section: "mode"
                    title: "Mode"
                    keyHint: "m"
                    subtitle: "global sends everything to " + (omihomo.primaryGroup || "the proxy")
                    modeControl: true
                  }

                  SettingRow {
                    Layout.fillWidth: true
                    section: "tun"
                    title: "TUN"
                    keyHint: "t"
                    subtitle: "capture every app, not just the proxy port"
                    checked: omihomo.tunActive
                    onActivated: omihomo.toggleTun()
                  }
                  SettingRow {
                    Layout.fillWidth: true
                    section: "tunRedirect"
                    title: "TUN acceleration"
                    keyHint: "A"
                    subtitle: root.tunRedirectBlocked ? "blocking TUN on this machine: turn it off" : "faster; turn off if TUN fails to start"
                    urgentSubtitle: root.tunRedirectBlocked
                    checked: omihomo.tunRedirectActive
                    onActivated: omihomo.toggleTunRedirect()
                  }
                }
              }

              SectionLine {
                Layout.fillWidth: true
                Layout.topMargin: Style.space(8)
                title: "SYSTEM"
              }

              Rectangle {
                Layout.fillWidth: true
                implicitHeight: systemColumn.implicitHeight + Style.space(6)
                radius: root.cardRadius
                color: root.fill
                border.width: 1
                border.color: root.line
                ColumnLayout {
                  id: systemColumn
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.margins: Style.space(3)
                  spacing: 0
                  SettingRow {
                    Layout.fillWidth: true
                    section: "autostart"
                    title: "Start at login"
                    keyHint: "b"
                    subtitle: "start mihomo when the session starts"
                    checked: omihomo.autostartActive
                    onActivated: omihomo.toggleAutostart()
                  }

                  SettingRow {
                    Layout.fillWidth: true
                    visible: omihomo.tailscalePresent
                    section: "tailscale"
                    title: "Tailscale integration"
                    keyHint: "T"
                    subtitle: "keep the tailnet reachable under TUN"
                    checked: omihomo.tailscaleActive
                    onActivated: omihomo.toggleTailscale()
                  }

                  SettingRow {
                    Layout.fillWidth: true
                    visible: omihomo.tailscalePresent
                    section: "tailnetProxy"
                    title: "Route tailnet through proxy"
                    keyHint: "P"
                    subtitle: "the proxy server must be on the tailnet"
                    checked: omihomo.tailnetProxyActive
                    opacity: omihomo.tailscaleActive ? 1 : 0.45
                    onActivated: omihomo.toggleTailnetProxy()
                  }
                }
              }

              SectionLine {
                Layout.fillWidth: true
                Layout.topMargin: Style.space(8)
                title: "MAINTENANCE"
              }

              Rectangle {
                Layout.fillWidth: true
                implicitHeight: maintenanceColumn.implicitHeight + Style.space(22)
                radius: root.cardRadius
                color: root.fill
                border.width: 1
                border.color: root.line
                ColumnLayout {
                  id: maintenanceColumn
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.rightMargin: Style.space(8)
                  spacing: Style.space(6)
                  PanelText {
                    Layout.fillWidth: true
                    Layout.leftMargin: Style.space(8)
                    text: "core " + Model.stateLabel(omihomo.coreState).toLowerCase() + " · " + (!omihomo.permissionsOk ? "missing root permissions, or open to other accounts" : (omihomo.hardeningPending ? "running core predates the current security rules" : "root grant owner-only"))
                    color: root.needsRepair ? root.urgent : root.dim
                    wrapMode: Text.WordWrap
                  }

                  RowLayout {
                    id: maintenanceRow
                    Layout.fillWidth: true
                    Layout.leftMargin: Style.space(8)
                    spacing: Style.space(6)
                    Component.onCompleted: {
                      root.settingItems["repair"] = maintenanceRow
                      root.settingItems["uninstall"] = maintenanceRow
                    }

                    SmallButton {
                      text: root.needsRepair ? root.repairLabel : "✓ permissions ok"
                      iconText: root.needsRepair ? "󰑐" : ""
                      enabled: root.needsRepair
                      tooltipText: "Repair · R"
                      foreground: root.needsRepair ? root.urgent : root.foreground
                      hasCursor: root.at("repair")
                      onHovered: function(on) { if (on) root.focusRow("repair") }
                      onClicked: omihomo.repairCore()
                    }

                    SmallButton {
                      text: root.uninstallArmed ? "confirm uninstall" : "uninstall…"
                      danger: true
                      hasCursor: root.at("uninstall")
                      onHovered: function(on) { if (on) root.focusRow("uninstall") }
                      onClicked: root.uninstallCore()
                    }

                    SmallButton {
                      visible: root.uninstallArmed
                      text: "cancel"
                      onClicked: root.uninstallArmed = false
                    }
                  }

                  PanelText {
                    visible: root.uninstallArmed
                    Layout.fillWidth: true
                    Layout.leftMargin: Style.space(8)
                    text: "Removes the core, its permissions, the unit, and saved subscriptions."
                    color: root.dim
                    wrapMode: Text.WordWrap
                  }
                }
              }
            }
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
            color: root.line
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

  component NavRow: CursorSurface {
    id: navRow

    property string section: ""
    property int rowIndex: -1
    property bool pendingChoice: false
    property color restFill: "transparent"
    readonly property alias hovered: navMouse.containsMouse
    readonly property color fadeFill: current ? Util.alpha(root.accent, hasCursor ? 0.4 : 0.28)
      : (pendingChoice ? Util.alpha(root.accent, hasCursor ? 0.16 : 0.08) : Util.alpha(root.foreground, 0.1))

    signal activated()

    hasCursor: root.at(navRow.section, navRow.rowIndex < 0 ? undefined : navRow.rowIndex)
    foreground: root.foreground
    radius: root.controlRadius
    borderSpec: Border.none()
    color: restFill
    gradient: current || pendingChoice || hasCursor ? fade : null

    Gradient {
      id: fade
      orientation: Gradient.Horizontal
      GradientStop { position: 0; color: navRow.fadeFill }
      GradientStop { position: 1; color: navRow.restFill.a > 0 ? navRow.restFill : Util.alpha(navRow.fadeFill, 0) }
    }

    MouseArea {
      id: navMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.focusRow(navRow.section,
        navRow.rowIndex < 0 ? undefined : navRow.rowIndex)
      onClicked: navRow.activated()
    }
  }

  component ScrollList: ListView {
    id: scrollList

    readonly property bool scrollable: contentHeight > height
    readonly property real rowWidth: width - (scrollable ? Style.space(10) : 0)

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
        GradientStop { position: 0; color: root.surface }
        GradientStop { position: 1; color: Util.alpha(root.surface, 0) }
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
        GradientStop { position: 0; color: Util.alpha(root.surface, 0) }
        GradientStop { position: 1; color: root.surface }
      }
    }
  }

  component PanelText: Text {
    renderType: Text.NativeRendering
    textFormat: Text.PlainText
    font.family: root.fontFamily
    font.pixelSize: Style.font.bodySmall
  }

  component NameText: RowLayout {
    id: name

    property string text: ""
    property color color: root.foreground
    property int pixelSize: Style.font.bodySmall
    property bool bold: false
    readonly property var parts: Model.leadingEmoji(text)

    spacing: markSlot.advanceWidth / 2

    TextMetrics {
      id: markSlot
      font.family: root.fontFamily
      font.pixelSize: name.pixelSize
      text: "MM"
    }

    PanelText {
      visible: name.parts.mark !== ""
      Layout.minimumWidth: markSlot.advanceWidth
      Layout.maximumWidth: markSlot.advanceWidth
      text: name.parts.mark
      color: name.color
      font.pixelSize: name.pixelSize
    }
    PanelText {
      id: nameRest
      Layout.fillWidth: true
      text: name.parts.rest
      color: name.color
      font.pixelSize: name.pixelSize
      font.bold: name.bold
      elide: Text.ElideRight
    }
  }

  component SmallButton: Button {
    property bool ghost: false
    property bool primary: false
    property bool danger: false

    bordered: true
    foreground: danger ? root.urgent : (primary ? root.surface : root.foreground)
    fontFamily: root.fontFamily
    fontSize: Style.font.bodySmall
    iconSize: Style.font.body
    horizontalPadding: Style.space(9)
    verticalPadding: Style.space(3)
    radius: root.controlRadius
    color: primary ? root.accent : (hot ? root.hoverFill : (ghost || danger ? "transparent" : root.fill))
    borderSpec: Border.flat(hasCursor ? root.accent : (danger ? Util.alpha(root.urgent, 0.55) : (ghost ? "transparent" : (primary ? root.accent : root.outline))), hasCursor ? 2 : 1)
    implicitHeight: Style.space(24)
    opacity: enabled ? 1 : 0.4
  }

  component PanelField: TextField {
    id: field
    property bool highlighted: false
    implicitWidth: Style.space(120)
    implicitHeight: Style.space(26)
    renderType: Text.NativeRendering
    font.family: root.fontFamily
    font.pixelSize: Style.font.bodySmall
    placeholderTextColor: root.faint
    background: Shapes.Shape {
      id: fieldBorder
      width: field.width
      height: field.height
      implicitWidth: 0
      implicitHeight: 0
      readonly property real r: root.controlRadius
      Shapes.ShapePath {
        fillColor: "transparent"
        strokeColor: field.activeFocus || field.highlighted || field.hasCursor ? root.accent : root.outline
        strokeWidth: 1
        strokeStyle: field.activeFocus || field.highlighted || field.hasCursor ? Shapes.ShapePath.SolidLine : Shapes.ShapePath.DashLine
        dashPattern: [3, 2]
        PathSvg {
          path: "M " + fieldBorder.r + " 0.5 H " + (fieldBorder.width - fieldBorder.r)
            + " Q " + (fieldBorder.width - 0.5) + " 0.5 " + (fieldBorder.width - 0.5) + " " + fieldBorder.r
            + " V " + (fieldBorder.height - fieldBorder.r)
            + " Q " + (fieldBorder.width - 0.5) + " " + (fieldBorder.height - 0.5) + " " + (fieldBorder.width - fieldBorder.r) + " " + (fieldBorder.height - 0.5)
            + " H " + fieldBorder.r + " Q 0.5 " + (fieldBorder.height - 0.5) + " 0.5 " + (fieldBorder.height - fieldBorder.r)
            + " V " + fieldBorder.r + " Q 0.5 0.5 " + fieldBorder.r + " 0.5 Z"
        }
      }
    }
  }

  component PanelSwitch: Rectangle {
    id: panelSwitch
    property bool checked: false
    property bool busy: false
    property bool interactive: true
    property color foreground: root.foreground
    property int trackWidth: Style.space(40)
    property int trackHeight: Style.space(22)
    readonly property alias containsMouse: switchMouse.containsMouse
    signal toggled()

    implicitWidth: trackWidth
    implicitHeight: trackHeight
    radius: Style.cornerRadius > 0 ? height / 2 : 0
    color: checked ? root.accent : root.hoverFill
    border.width: 1
    border.color: switchMouse.containsMouse ? foreground : (checked ? root.accent : root.outline)
    opacity: enabled ? 1 : 0.35

    Rectangle {
      width: parent.height - Style.space(6)
      height: width
      radius: Style.cornerRadius > 0 ? width / 2 : 0
      x: panelSwitch.checked ? panelSwitch.width - width - Style.space(3) : Style.space(3)
      anchors.verticalCenter: parent.verticalCenter
      color: panelSwitch.checked ? root.surface : root.dim
      Behavior on x { NumberAnimation { duration: Style.duration(220); easing.type: Easing.OutBack } }
    }

    MouseArea {
      id: switchMouse
      anchors.fill: parent
      enabled: panelSwitch.interactive
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: if (!panelSwitch.busy) panelSwitch.toggled()
    }
  }

  component SectionLine: RowLayout {
    id: sectionLine

    property string title: ""
    property string note: ""

    spacing: Style.space(8)

    PanelText {
      text: sectionLine.title
      color: root.dim
      font.letterSpacing: Style.space(0.8)
    }

    Item { Layout.fillWidth: true }

    PanelText {
      visible: text !== ""
      text: sectionLine.note
      color: root.faint
    }
  }

  component Tag: Rectangle {
    id: tag

    property string text: ""
    property color tint: root.dim
    property bool strong: false
    property bool filled: false
    property bool pill: true
    property real maxWidth: Style.space(110)

    implicitWidth: Math.min(tagText.implicitWidth, maxWidth) + Style.space(10)
    implicitHeight: tagText.implicitHeight + Style.space(2)
    radius: pill ? (Style.cornerRadius > 0 ? height / 2 : 0) : Math.min(root.controlRadius, Style.space(4))
    color: filled ? tint : "transparent"
    border.width: 1
    border.color: Util.alpha(tint, strong ? 0.9 : 0.35)

    NameText {
      id: tagText
      anchors.centerIn: parent
      width: Math.min(implicitWidth, tag.maxWidth)
      text: tag.text
      color: tag.filled ? root.surface : tag.tint
    }
  }

  component Kbd: BorderSurface {
    property string text: ""

    implicitWidth: Math.max(kbdText.implicitWidth + Style.space(8), Style.space(18))
    implicitHeight: Style.space(18)
    radius: Math.min(Style.cornerRadius, 4)
    color: Util.alpha(root.foreground, 0.06)
    borderSpec: Border.flat(Util.alpha(root.foreground, 0.2), "1 1 2 1")

    PanelText {
      id: kbdText
      anchors.centerIn: parent
      anchors.verticalCenterOffset: -0.5
      text: parent.text
      color: root.foreground
    }
  }

  component HintButton: Item {
    id: hint

    property string keys: ""
    property string label: ""

    signal clicked()

    implicitWidth: hintRow.implicitWidth
    implicitHeight: hintRow.implicitHeight

    Row {
      id: hintRow
      spacing: Style.space(4)

      Repeater {
        model: hint.keys.split(" ")
        Kbd {
          required property var modelData
          text: modelData
        }
      }

      PanelText {
        anchors.verticalCenter: parent.verticalCenter
        text: hint.label
        color: hintMouse.containsMouse ? root.foreground : root.dim
      }
    }

    MouseArea {
      id: hintMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: hint.clicked()
    }
  }

  component ViewTab: Rectangle {
    id: tab
    property string target: ""
    property int number: 0
    property string label: ""
    property string count: ""
    property bool alert: false
    property bool gear: false
    readonly property bool current: root.view === target
    readonly property color iconColor: root.needsRepair || root.tunRedirectBlocked ? root.urgent
      : (current || tabMouse.containsMouse ? root.foreground : root.dim)
    onIconColorChanged: settingsIcon.requestPaint()

    Layout.fillWidth: true
    implicitHeight: Style.space(44)
    radius: root.controlRadius
    color: current || tabMouse.containsMouse ? root.hoverFill : "transparent"
    border.width: 1
    border.color: current ? root.outline : "transparent"

    PanelText {
      visible: !tab.gear
      anchors.left: parent.left
      anchors.top: parent.top
      anchors.leftMargin: Style.space(6)
      anchors.topMargin: Style.space(3)
      text: String(tab.number)
      color: root.faint
    }

    PanelText {
      anchors.horizontalCenter: parent.horizontalCenter
      anchors.verticalCenter: tab.gear ? parent.verticalCenter : undefined
      anchors.bottom: tab.gear ? undefined : parent.bottom
      anchors.bottomMargin: Style.space(6)
      visible: !tab.gear
      text: tab.label
      color: tab.current ? root.foreground : root.dim
      font.pixelSize: tab.gear ? Style.font.iconLarge : Style.font.body
    }

    Canvas {
      id: settingsIcon
      visible: tab.gear
      anchors.centerIn: parent
      width: Style.space(17)
      height: width
      onVisibleChanged: if (visible) requestPaint()
      onWidthChanged: requestPaint()
      onHeightChanged: requestPaint()
      onPaint: {
        var ctx = getContext("2d")
        ctx.reset()
        ctx.scale(width / 24, height / 24)
        ctx.strokeStyle = root.css(tab.iconColor)
        ctx.lineWidth = 1.8
        ctx.lineCap = "round"
        ctx.beginPath()
        ctx.moveTo(4, 7); ctx.lineTo(14, 7)
        ctx.moveTo(18, 7); ctx.lineTo(20, 7)
        ctx.moveTo(4, 17); ctx.lineTo(8, 17)
        ctx.moveTo(12, 17); ctx.lineTo(20, 17)
        ctx.stroke()
        ctx.beginPath()
        ctx.arc(16, 7, 2, 0, Math.PI * 2)
        ctx.moveTo(12, 17); ctx.arc(10, 17, 2, 0, Math.PI * 2)
        ctx.stroke()
      }
    }

    Rectangle {
      visible: !tab.gear && tab.count !== ""
      anchors.top: parent.top
      anchors.right: parent.right
      anchors.topMargin: Style.space(3)
      anchors.rightMargin: Style.space(5)
      implicitWidth: countText.implicitWidth + Style.space(8)
      implicitHeight: Style.space(15)
      radius: Math.min(root.controlRadius, Style.space(4))
      color: tab.alert ? root.urgent : root.hoverFill
      PanelText {
        id: countText
        anchors.centerIn: parent
        text: tab.count
        color: tab.alert ? root.surface : root.dim
      }
    }

    Shapes.Shape {
      id: underline
      visible: tab.current
      anchors.fill: parent
      anchors.margins: tab.border.width
      preferredRendererType: Shapes.Shape.CurveRenderer
      readonly property real r: Math.max(0, tab.radius - tab.border.width)
      readonly property real t: Style.space(2)

      Shapes.ShapePath {
        strokeColor: "transparent"
        fillColor: root.accent
        PathSvg {
          readonly property real w: underline.width
          readonly property real h: underline.height
          readonly property real r: underline.r
          readonly property real t: underline.t
          path: "M 0 " + (h - r) + " Q 0 " + h + " " + r + " " + h + " H " + (w - r)
            + " Q " + w + " " + h + " " + w + " " + (h - r) + " V " + (h - r - t)
            + " Q " + w + " " + (h - t) + " " + (w - r) + " " + (h - t) + " H " + r
            + " Q 0 " + (h - t) + " 0 " + (h - r - t) + " Z"
        }
      }
    }

    MouseArea {
      id: tabMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.cursorActive = false
      onClicked: root.openView(tab.gear && tab.current ? "configs" : tab.target)
    }

    PanelToolTip {
      visible: tabMouse.containsMouse
      text: tab.gear ? (root.needsRepair ? "Settings · needs repair · M" : "Settings · M") : tab.label + " · " + tab.number
      fontFamily: root.fontFamily
    }
  }

  component Readout: Item {
    id: readout

    property string label: ""
    property string value: ""
    property bool faded: false
    property string tooltip: ""

    signal activated()

    Layout.fillWidth: true
    implicitHeight: readoutColumn.implicitHeight

    Column {
      id: readoutColumn
      width: parent.width
      spacing: Style.space(2)

      PanelText {
        text: readout.label
        color: root.dim
        font.letterSpacing: Style.space(0.8)
      }

      PanelText {
        width: parent.width
        text: readout.value
        opacity: omihomo.coreActive ? 1 : 0.35
        color: readout.faded ? root.dim : root.foreground
        font.pixelSize: Style.font.subtitle
        font.underline: readout.tooltip !== "" && readoutMouse.containsMouse
        elide: Text.ElideRight
      }
    }

    MouseArea {
      id: readoutMouse
      anchors.fill: parent
      enabled: readout.tooltip !== ""
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: readout.activated()
    }

    PanelToolTip {
      visible: readout.tooltip !== "" && readoutMouse.containsMouse
      text: readout.tooltip
      fontFamily: root.fontFamily
    }
  }

  component TrafficGraph: Canvas {
    id: graph

    readonly property var samples: omihomo.trafficHistory

    onSamplesChanged: requestPaint()
    onWidthChanged: requestPaint()
    onHeightChanged: requestPaint()
    onVisibleChanged: if (visible) requestPaint()

    onPaint: {
      var ctx = getContext("2d")
      ctx.reset()
      var list = samples || []
      var slots = Math.max(2, omihomo.trafficHistoryCap)
      var step = width / (slots - 1)
      var offset = slots - list.length
      var peak = 1
      var i
      for (i = 0; i < list.length; i++) peak = Math.max(peak, list[i].down, list[i].up)

      ctx.strokeStyle = root.css(root.line)
      ctx.lineWidth = 1
      ctx.beginPath()
      ctx.moveTo(0, height - 0.5)
      ctx.lineTo(width, height - 0.5)
      ctx.stroke()
      if (list.length < 2) return

      function trace(key) {
        ctx.beginPath()
        for (var j = 0; j < list.length; j++) {
          var x = (offset + j) * step
          var y = height - 1 - (list[j][key] / peak) * (height - 3)
          if (j === 0) ctx.moveTo(x, y)
          else ctx.lineTo(x, y)
        }
      }

      var series = [["down", root.foreground, 0.14], ["up", root.accent, 0.2]]
      for (i = 0; i < series.length; i++) {
        trace(series[i][0])
        ctx.lineTo(width, height)
        ctx.lineTo(offset * step, height)
        ctx.closePath()
        ctx.fillStyle = root.css(Util.alpha(series[i][1], series[i][2]))
        ctx.fill()
        trace(series[i][0])
        ctx.strokeStyle = root.css(series[i][1])
        ctx.lineWidth = 1.2
        ctx.stroke()
      }
    }
  }

  component FlowDiagram: Canvas {
    id: diagram

    readonly property var flows: root.flows
    readonly property var hot: {
      var stack = root.cursorActive && root.cursorRow && root.cursorRow.s === "conn"
        ? root.connectionAt(root.cursorRow.i) : null
      return stack && stack.open ? { source: stack.process || "unknown", target: stack.chain || "DIRECT" } : null
    }

    onFlowsChanged: requestPaint()
    onHotChanged: requestPaint()
    onWidthChanged: requestPaint()
    onHeightChanged: requestPaint()
    onVisibleChanged: if (visible) requestPaint()

    onPaint: {
      var ctx = getContext("2d")
      ctx.reset()
      var sources = flows.sources
      var targets = flows.targets
      ctx.font = Style.font.bodySmall + "px \"" + root.fontFamily + "\""
      ctx.textBaseline = "middle"
      if (sources.length === 0) {
        ctx.fillStyle = root.css(root.dim)
        ctx.fillText("No open connections.", 0, height / 2)
        return
      }
      var pad = Style.space(14)
      var left = Style.space(86)
      var right = width - Style.space(96)

      function rowY(index, count) {
        return count < 2 ? height / 2 : pad + index * (height - pad * 2) / (count - 1)
      }
      function indexOf(nodes, name) {
        for (var n = 0; n < nodes.length; n++) if (nodes[n].name === name) return n
        return -1
      }
      function fit(text, room) {
        var out = text
        if (ctx.measureText(out).width <= room) return out
        while (out.length > 1 && ctx.measureText(out + "…").width > room) out = out.slice(0, -1)
        return out + "…"
      }
      function isHot(source, target) {
        return hot !== null && (source === undefined || source === hot.source)
          && (target === undefined || target === hot.target)
      }

      var i
      var links = flows.links.slice().sort(function(a, b) {
        return isHot(a.source, a.target) - isHot(b.source, b.target)
      })
      var blend = Qt.tint(root.surface, Util.alpha(root.foreground, 0.22))
      var muted = Qt.rgba(blend.r, blend.g, blend.b, 1)
      for (i = 0; i < links.length; i++) {
        var link = links[i]
        var y1 = rowY(indexOf(sources, link.source), sources.length)
        var y2 = rowY(indexOf(targets, link.target), targets.length)
        var middle = (left + right) / 2
        ctx.beginPath()
        ctx.moveTo(left + 4, y1)
        ctx.bezierCurveTo(middle, y1, middle, y2, right - 4, y2)
        ctx.lineWidth = Math.max(1.5, Math.min(12, 3.5 * Math.log(Math.max(1, link.bytes / 2e4)) / Math.LN10))
        ctx.strokeStyle = root.css(isHot(link.source, link.target) ? root.accent : muted)
        ctx.stroke()
      }

      for (i = 0; i < sources.length; i++) {
        var sy = rowY(i, sources.length)
        ctx.fillStyle = root.css(isHot(sources[i].name, undefined) ? root.accent : root.foreground)
        ctx.fillRect(left, sy - 7, 4, 14)
        ctx.textAlign = "right"
        ctx.fillText(fit(sources[i].name, left - 8), left - 6, sy)
      }

      ctx.textAlign = "left"
      for (i = 0; i < targets.length; i++) {
        var ty = rowY(i, targets.length)
        ctx.fillStyle = root.css(isHot(undefined, targets[i].name) ? root.accent : root.foreground)
        ctx.fillRect(right - 4, ty - 7, 4, 14)
        ctx.fillText(fit(targets[i].name, width - right - 8), right + 6, ty - 5)
        ctx.fillStyle = root.css(root.dim)
        ctx.fillText(Model.formatBytes(targets[i].bytes), right + 6, ty + 7)
      }
    }
  }

  component GroupChip: NavRow {
    id: chip

    property var group: null
    readonly property string groupName: group ? String(group.name) : ""
    readonly property bool primary: groupName !== "" && groupName === omihomo.primaryGroup

    section: "group"
    current: groupName === root.browsedGroup
    bordered: true
    implicitWidth: chipRow.implicitWidth + Style.space(20)
    implicitHeight: chipRow.implicitHeight + Style.space(8)
    radius: Style.cornerRadius > 0 ? height / 2 : 0
    gradient: null
    color: current ? Util.alpha(root.accent, hasCursor ? 0.24 : 0.16) : (hasCursor ? Util.alpha(root.foreground, 0.09) : "transparent")
    borderSpec: Border.flat(current ? Util.alpha(root.accent, 0.5) : root.outline, 1)
    onActivated: root.browseGroupAt(chip.rowIndex)

    Row {
      id: chipRow
      anchors.centerIn: parent
      spacing: Style.space(6)

      PanelText {
        anchors.verticalCenter: parent.verticalCenter
        text: chip.groupName
        color: chip.current || chip.hasCursor ? root.foreground : root.dim
      }

      Rectangle {
        visible: chip.primary
        anchors.verticalCenter: parent.verticalCenter
        width: Style.space(5)
        height: width
        radius: width / 2
        color: root.accent
      }
    }

    PanelToolTip {
      visible: chip.hovered
      text: chip.primary ? "Primary group" : "Browse · p makes it primary"
      fontFamily: root.fontFamily
    }
  }

  component InlineAction: PanelActionButton {
    fontFamily: root.fontFamily
    foreground: root.faint
    hoverColor: root.foreground
    radius: root.controlRadius
  }

  component ConfigRow: NavRow {
    id: configRow

    property string configName: ""
    readonly property bool selected: root.browsedGroupEntry !== null && root.browsedGroupEntry.now === configRow.configName
    readonly property bool pending: omihomo.pendingConfig === configRow.configName
    readonly property string testState: omihomo.configTestState(configRow.configName)
    readonly property int delay: omihomo.configDelay(configRow.configName)

    section: "config"
    current: configRow.selected
    pendingChoice: configRow.pending && !configRow.selected
    implicitHeight: Style.space(30)
    onActivated: root.chooseConfigAt(configRow.rowIndex)

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(4)
      spacing: Style.space(8)

      NameText {
        Layout.fillWidth: true
        text: configRow.configName
        pixelSize: Style.font.body
        bold: configRow.selected
      }
      Rectangle {
        id: pill

        readonly property bool failed: configRow.testState === "failed"
        readonly property bool testing: configRow.testState === "testing"

        implicitWidth: Math.max(pillText.implicitWidth + Style.space(12), Style.space(52))
        implicitHeight: pillText.implicitHeight + Style.space(2)
        radius: Math.min(root.controlRadius, Style.space(5))
        color: pill.failed ? root.urgent
          : (configRow.delay > 0 && !pill.testing
            ? root.latencyFill(Model.delayHeat(configRow.delay, root.delaySpan))
            : "transparent")

        PanelText {
          id: pillText
          anchors.centerIn: parent
          text: pill.testing ? "···" : (pill.failed ? "timeout" : Model.formatDelay(configRow.delay))
          color: pill.failed ? root.surface : (configRow.delay > 0 ? root.foreground : root.faint)

          SequentialAnimation on opacity {
            running: pill.testing
            loops: Animation.Infinite
            alwaysRunToEnd: true
            NumberAnimation { from: 1; to: 0.3; duration: 450 }
            NumberAnimation { from: 0.3; to: 1; duration: 450 }
          }
        }
      }

      InlineAction {
        iconText: "󰓅"
        tooltipText: "Test latency · d"
        foreground: root.faint
        fontFamily: root.fontFamily
        size: Style.space(22)
        enabled: !omihomo.pingTestsRunning
        onClicked: omihomo.testConfig(configRow.configName)
      }
    }
  }

  component SubscriptionCard: NavRow {
    id: card

    property var subscription: null
    readonly property string subName: subscription ? String(subscription.name) : ""
    readonly property bool active: !!subscription && subscription.active === true
    readonly property bool pending: subName !== "" && omihomo.pendingSubscription === subName
    readonly property bool failed: root.errorAt("sub:" + subName)
    readonly property var usage: Model.subscriptionUsage(subscription)
    readonly property string quotaText: usage.text.replace(/(\S+) (\S+) of (\S+) \2/, "$1/$3 $2").replace("expires ", "exp ")

    section: "sub"
    current: active
    pendingChoice: card.pending && !card.active
    radius: root.cardRadius
    restFill: root.fill
    borderSpec: Border.flat(failed ? root.urgent : root.line, 1)
    implicitHeight: cardColumn.implicitHeight + Style.space(18)
    onActivated: root.activateSubscriptionAt(card.rowIndex)

    ColumnLayout {
      id: cardColumn
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(7)

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(8)

        PanelText {
          Layout.fillWidth: true
          text: card.subName
          color: root.foreground
          font.pixelSize: Style.font.body
          font.bold: card.active
          elide: Text.ElideRight
        }

        Tag {
          text: card.pending ? "working…" : (card.failed ? "failed" : (card.active ? "active"
            : "updated " + (Model.relativeTime(card.subscription ? card.subscription.updatedAt : "", root.nowMs) || "never")))
          tint: card.failed ? root.urgent : (card.active ? root.accent : root.dim)
          strong: card.active || card.failed
          pill: false
        }

        SmallButton {
          text: "update"
          iconText: "󰑐"
          iconSpinning: card.pending
          enabled: !card.pending
          tooltipText: "Update · u"
          onClicked: omihomo.updateSubscription(card.subName)
        }

        InlineAction {
          iconText: "×"
          tooltipText: "Remove · x"
          foreground: root.dim
          hoverColor: root.urgent
          fontFamily: root.fontFamily
          size: Style.space(18)
          onClicked: root.removeSubscription(card.subName)
        }
      }

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(8)

        Rectangle {
          Layout.fillWidth: true
          visible: card.usage.fraction >= 0
          Layout.minimumWidth: Style.space(36)
          implicitHeight: Style.space(5)
          radius: Math.min(Style.cornerRadius, 2)
          color: Util.alpha(root.foreground, 0.12)

          Rectangle {
            width: parent.width * Math.max(0, card.usage.fraction)
            height: parent.height
            radius: parent.radius
            color: card.usage.fraction > 0.9 ? root.urgent : (card.active ? root.accent : root.faint)
          }
        }

        PanelText {
          Layout.maximumWidth: cardColumn.width - Style.space(64)
          text: card.quotaText + (card.active ? " · "
            + (Model.relativeTime(card.subscription ? card.subscription.updatedAt : "", root.nowMs) || "never") : "")
          color: root.dim
          elide: Text.ElideRight
        }
      }

      Rectangle {
        visible: card.failed
        Layout.fillWidth: true
        implicitHeight: Math.max(errorMessage.implicitHeight, Style.space(24)) + Style.space(14)
        radius: root.controlRadius
        color: Util.alpha(root.urgent, 0.14)
        Rectangle { anchors.left: parent.left; height: parent.height; width: Style.space(3); color: root.urgent }

        RowLayout {
          id: errorRow
          anchors.fill: parent
          anchors.margins: Style.space(7)
          spacing: Style.space(8)

        PanelText {
          id: errorMessage
          Layout.fillWidth: true
          text: "▲ " + omihomo.lastError
          color: root.urgent
          wrapMode: Text.WordWrap
        }

        SmallButton {
          text: "retry"
          iconText: "󰑐"
          primary: true
          onClicked: omihomo.updateSubscription(card.subName)
        }
        }
      }

      RowLayout {
        visible: root.removeArmed === card.subName
        Layout.fillWidth: true
        spacing: Style.space(6)

        PanelText {
          Layout.fillWidth: true
          text: "Remove " + card.subName + "?"
          color: root.foreground
          elide: Text.ElideRight
        }

        SmallButton {
          text: "keep"
          onClicked: root.removeArmed = ""
        }

        SmallButton {
          text: "remove · x"
          foreground: root.urgent
          onClicked: root.removeSubscription(card.subName)
        }
      }
    }
  }

  component TargetRow: NavRow {
    id: targetRow

    property var target: ({})

    section: "target"
    current: targetRow.target.value === root.ruleTarget
    implicitHeight: Style.space(26)
    onActivated: root.pickTargetAt(targetRow.rowIndex)

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(8)

      NameText {
        Layout.fillWidth: true
        text: targetRow.target.label || ""
        bold: targetRow.current
      }
      PanelText {
        text: targetRow.target.description || ""
        color: root.faint
      }
    }
  }

  component RuleRow: NavRow {
    id: ruleRow

    property var entry: ({})
    readonly property var rule: entry.rule || null
    readonly property bool own: entry.kind === "own"
    readonly property bool testing: root.ruleTest.host !== ""
    readonly property string verdict: entry.eff >= 0 && entry.eff < root.ruleTest.verdicts.length
      ? root.ruleTest.verdicts[entry.eff] : ""
    readonly property bool hit: testing && root.ruleTest.hit >= 0 && entry.eff === root.ruleTest.hit
    readonly property bool below: testing && root.ruleTest.hit >= 0 && (entry.eff < 0 || entry.eff > root.ruleTest.hit)

    section: entry.s || ""
    rowIndex: entry.i === undefined ? -1 : entry.i
    current: hit
    implicitHeight: Style.space(22)
    opacity: below ? 0.5 : 1

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(4)
      anchors.rightMargin: Style.space(2)
      spacing: Style.space(8)

      Item {
        id: rail

        readonly property real knot: Style.space(8)
        readonly property real knotTop: Math.round((height - knot) / 2)
        readonly property real stroke: Style.space(2)
        readonly property real lineX: Math.round((width - stroke) / 2)
        readonly property color tint: ruleRow.testing && !ruleRow.below ? root.accent : root.outline

        Layout.fillHeight: true
        implicitWidth: Style.space(14)

        Rectangle {
          visible: !ruleRow.below && ruleRow.entry.eff !== 0
          x: rail.lineX
          width: rail.stroke
          height: rail.knotTop
          color: rail.tint
        }

        Rectangle {
          visible: !ruleRow.below && !ruleRow.hit
          x: rail.lineX
          y: rail.knotTop + rail.knot
          width: rail.stroke
          height: parent.height - y
          color: rail.tint
        }

        Repeater {
          model: ruleRow.below ? [0, 2 * rail.stroke, rail.knotTop + rail.knot + rail.stroke] : []
          Rectangle {
            required property real modelData
            x: rail.lineX
            y: modelData
            width: rail.stroke
            height: rail.stroke
            color: root.outline
          }
        }

        Rectangle {
          x: Math.round((parent.width - width) / 2)
          y: rail.knotTop
          width: rail.knot
          height: width
          radius: width / 2
          color: ruleRow.hit ? root.accent : "transparent"
          border.width: rail.stroke
          border.color: rail.tint
        }
      }

      PanelText {
        Layout.preferredWidth: Style.space(56)
        text: root.shortRuleType(ruleRow.rule ? ruleRow.rule.type : "")
        color: root.dim
        elide: Text.ElideRight
      }

      PanelText {
        Layout.fillWidth: true
        text: ruleRow.rule ? (ruleRow.rule.value !== "" ? ruleRow.rule.value : "everything else") : ""
        color: ruleRow.own || ruleRow.hit ? root.foreground : root.dim
        elide: Text.ElideRight
      }

      PanelText {
        visible: ruleRow.own && !!ruleRow.rule && ruleRow.rule.kind !== "prepend"
        text: ruleRow.rule && ruleRow.rule.kind ? ruleRow.rule.kind : ""
        color: root.faint
      }

      Tag {
        text: ruleRow.rule ? ruleRow.rule.target : ""
        tint: ruleRow.hit ? root.accent : root.dim
        strong: ruleRow.hit
        filled: ruleRow.hit
        maxWidth: Style.space(84)
      }

      PanelText {
        Layout.preferredWidth: Style.space(22)
        text: ruleRow.hit ? "hit" : (ruleRow.verdict === "miss" ? "no" : (ruleRow.verdict === "unknown" ? "?" : ""))
        color: ruleRow.hit ? root.accent : root.faint
      }

      Item {
        implicitWidth: Style.space(18)
        implicitHeight: Style.space(18)

        InlineAction {
          visible: ruleRow.own && ruleRow.hasCursor
          anchors.fill: parent
          iconText: "×"
          tooltipText: "Delete · x"
          foreground: root.foreground
          hoverColor: root.urgent
          fontFamily: root.fontFamily
          size: Style.space(18)
          onClicked: if (ruleRow.rule) omihomo.removeRule(ruleRow.rule.index)
        }
      }
    }

    PanelToolTip {
      visible: ruleRow.hovered && ruleRow.verdict === "unknown"
      text: "Depends on a DNS answer, a rule set, or the app making the connection"
      fontFamily: root.fontFamily
    }
  }

  component ConnectionRow: NavRow {
    id: connectionRow

    property var stack: null
    readonly property bool live: !!stack && stack.open
    readonly property double bytes: stack ? stack.download + stack.upload : 0

    section: "conn"
    implicitHeight: Style.space(24)
    opacity: live ? 1 : 0.55
    onActivated: root.activateConnectionAt(connectionRow.rowIndex)

    Rectangle {
      anchors.left: parent.left
      anchors.top: parent.top
      anchors.bottom: parent.bottom
      anchors.margins: 1
      width: (parent.width - 2) * Math.max(0.02, Math.min(1, root.largestStack > 5000 && connectionRow.bytes > 5000
        ? Math.log(connectionRow.bytes / 5000) / Math.log(root.largestStack / 5000) : 0))
      radius: root.controlRadius
      color: Util.alpha(root.foreground, 0.07)
    }

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(6)
      anchors.rightMargin: Style.space(2)
      spacing: Style.space(6)

      PanelText {
        text: connectionRow.live ? "●" : "○"
        color: connectionRow.live ? root.accent : root.faint
        font.pixelSize: Style.font.caption
      }

      PanelText {
        Layout.maximumWidth: Style.space(96)
        text: connectionRow.stack ? (connectionRow.stack.process || "unknown") : ""
        color: connectionRow.live ? root.foreground : root.dim
        font.bold: connectionRow.live
        elide: Text.ElideRight
      }

      PanelText {
        visible: connectionRow.stack && connectionRow.stack.count > 1
        text: connectionRow.stack ? "×" + connectionRow.stack.count : ""
        color: root.dim
      }

      PanelText {
        Layout.fillWidth: true
        text: connectionRow.stack ? connectionRow.stack.host : ""
        color: root.dim
        elide: Text.ElideRight
      }

      Tag {
        visible: text !== ""
        text: connectionRow.stack ? connectionRow.stack.chain : ""
        maxWidth: Style.space(80)
      }

      PanelText {
        text: Model.formatBytes(connectionRow.bytes)
        color: connectionRow.live ? root.foreground : root.dim
      }

      InlineAction {
        opacity: connectionRow.hasCursor ? 1 : 0
        iconText: connectionRow.live ? "×" : "󰩹"
        tooltipText: connectionRow.live ? "Close · x" : "Remove from log · x"
        foreground: root.foreground
        hoverColor: root.urgent
        fontFamily: root.fontFamily
        size: Style.space(18)
        onClicked: root.activateConnectionAt(connectionRow.rowIndex)
      }
    }
    PanelToolTip {
      visible: connectionRow.hovered && !!connectionRow.stack
      text: {
        var stack = connectionRow.stack
        if (!stack) return ""
        var parts = []
        if (stack.network !== "") parts.push(stack.network)
        if (connectionRow.live) {
          if (stack.rule !== "") parts.push(stack.rule)
          parts.push(Model.formatDuration(root.nowMs - stack.startMs))
        } else {
          parts.push("closed " + Model.relativeSince(stack.closedAtMs, root.nowMs))
        }
        return parts.join(" · ")
      }
      fontFamily: root.fontFamily
    }
  }

  component SettingRow: NavRow {
    id: settingRow

    property string title: ""
    property string subtitle: ""
    property string keyHint: ""
    property bool urgentSubtitle: false
    property bool checked: false
    property bool modeControl: false

    implicitHeight: settingColumn.implicitHeight + Style.space(14)
    onActivated: if (settingRow.modeControl) root.cycleMode(1)
    Component.onCompleted: root.settingItems[settingRow.section] = settingRow

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(10)

      ColumnLayout {
        id: settingColumn
        Layout.fillWidth: true
        spacing: Style.space(2)

        RowLayout {
          Layout.fillWidth: true
          spacing: Style.space(6)

          PanelText {
            Layout.fillWidth: true
            text: settingRow.title
            color: root.foreground
            font.pixelSize: Style.font.body
          }

          Kbd { text: settingRow.keyHint; visible: text !== "" }
        }

        PanelText {
          Layout.fillWidth: true
          text: settingRow.subtitle
          color: settingRow.urgentSubtitle ? root.urgent : root.faint
          wrapMode: Text.WordWrap
        }
      }

      PanelSwitch {
        visible: !settingRow.modeControl
        interactive: false
        checked: settingRow.checked
        foreground: root.foreground
      }

      Rectangle {
        visible: settingRow.modeControl
        implicitWidth: modes.implicitWidth + 2
        implicitHeight: Style.space(24)
        radius: root.controlRadius
        color: "transparent"
        border.width: 1
        border.color: root.outline

        Row {
          id: modes
          anchors.fill: parent
          anchors.margins: 1

          Repeater {
            model: Model.MODES

            Item {
              id: segment
              required property var modelData
              required property int index
              readonly property bool on: omihomo.effectiveMode === modelData

              implicitWidth: segmentText.implicitWidth + Style.space(18)
              height: modes.height

              Rectangle {
                visible: segment.on
                anchors.fill: parent
                radius: Math.max(0, root.controlRadius - 1)
                color: root.foreground
              }

              Rectangle {
                visible: segment.on
                anchors.fill: parent
                anchors.leftMargin: segment.index === 0 ? parent.width / 2 : 0
                anchors.rightMargin: segment.index === Model.MODES.length - 1 ? parent.width / 2 : 0
                color: root.foreground
              }

              PanelText {
                id: segmentText
                anchors.centerIn: parent
                text: String(segment.modelData)
                color: segment.on ? root.surface : (segmentMouse.containsMouse ? root.foreground : root.dim)
              }

              MouseArea {
                id: segmentMouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: omihomo.setMode(String(segment.modelData))
              }
            }
          }
        }
      }
    }
  }
}
