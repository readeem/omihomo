import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell.Io
import qs.Commons
import qs.Commons as Commons
import qs.Ui
import "Model.js" as Model

// Omihomo's bar widget: a compact core-state icon and one popup. The popup is a
// header that always shows the core and its traffic, four tabbed views over the
// same surface — configs, subscriptions, rules, connections — and a settings
// view behind the gear.
//
// Navigation is one flat cursor over the current view's `navRows`: every row the
// view draws appears once in that list, so j/k walks it top to bottom and a row
// appearing or disappearing costs nothing but a rebuild.
Panel {
  id: root

  moduleName: "dev.readeem.omihomo"
  ipcTarget: "dev.readeem.omihomo"
  manageIpc: false

  // "configs", "subs", "rules", "conns", or "settings".
  property string view: "configs"
  readonly property var tabViews: ["configs", "subs", "rules", "conns"]

  property bool cursorActive: false
  property int cursor: 0

  property string browseGroup: ""
  property bool filterOpen: false
  readonly property string configFilter: filterOpen ? configFilterField.text : ""

  // Removing a subscription and uninstalling the core both ask twice.
  property string removeArmed: ""
  property bool uninstallArmed: false
  property int ruleTypeIndex: 0
  property int ruleTargetIndex: 0

  // Form values live in their fields: binding a TextField's `text` to panel
  // state breaks the moment the user types into it, so the field is the source
  // and the panel reads it.
  readonly property string subFormUrl: subUrlField.text.trim()
  readonly property string ruleFormValue: ruleValueField.text
  readonly property string ruleQuery: ruleTestField.text

  // The field currently taking keys, or null. While set, the key catcher is
  // blocked and the field owns Esc and Enter.
  property Item editing: null

  property double nowMs: Date.now()

  readonly property color foreground: bar ? bar.foreground : Commons.Color.foreground
  readonly property color urgent: bar ? bar.urgent : Commons.Color.urgent
  readonly property color accent: Commons.Color.accent
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property color faint: Qt.darker(foreground, 2.3)
  readonly property color line: Util.alpha(foreground, 0.12)
  readonly property color surface: Commons.Color.popups.background
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property color hoverFill: bar ? Style.hoverFillFor(bar.foreground, Commons.Color.accent) : "transparent"
  readonly property color selectedFill: bar ? Style.selectedFillFor(bar.foreground, Commons.Color.accent) : "transparent"
  readonly property bool needsRepair: omihomo.installed && !omihomo.permissionsOk
  readonly property color barIconColor: needsRepair ? urgent
    : (omihomo.coreActive ? barForeground : Qt.darker(barForeground, 1.55))

  // The plugin invokes its own CLI by absolute path, derived from where this
  // file sits — no PATH lookup, so a stale copy can never be picked up.
  readonly property string pluginDir: decodeURIComponent(String(Qt.resolvedUrl(".")).replace(/^file:\/\//, ""))
  readonly property string cliPath: pluginDir + "bin/omihomo"

  readonly property bool liveReady: omihomo.apiReady
  // A degraded core with the redirect still on is the case the acceleration
  // switch repairs, so that is when its row is worth colouring.
  readonly property bool tunRedirectBlocked: omihomo.coreState === "degraded"
    && omihomo.tunRedirectActive
  readonly property bool compactHeader: view === "rules" || view === "conns" || view === "settings"

  readonly property var visibleGroups: Model.userGroups(omihomo.groups)
  readonly property string browsedGroup: Model.groupByName(visibleGroups, browseGroup)
    ? browseGroup : omihomo.primaryGroup
  readonly property var browsedGroupEntry: Model.groupByName(visibleGroups, browsedGroup)
  readonly property var visibleConfigs: browsedGroupEntry
    ? Model.filterNames(browsedGroupEntry.all, configFilter) : []
  readonly property string testedText: {
    var at = omihomo.groupTestedAt[browsedGroup]
    return at ? "tested " + Model.relativeSince(at, nowMs) : "not tested yet"
  }

  readonly property var ruleTargets: {
    var targets = ["DIRECT", "REJECT"]
    for (var i = 0; i < visibleGroups.length; i++) targets.push(visibleGroups[i].name)
    return targets
  }
  readonly property string ruleType: Model.RULE_TYPES[Math.max(0, Math.min(ruleTypeIndex, Model.RULE_TYPES.length - 1))]
  readonly property string ruleTarget: ruleTargets[Math.max(0, Math.min(ruleTargetIndex, ruleTargets.length - 1))]
  readonly property string ruleFormError: Model.validateRule(ruleType, ruleFormValue, ruleTarget)

  // Rules in the order mihomo walks them: ours that are prepended, then
  // everything the core reports after them.
  readonly property var ownHeadRules: omihomo.rules.filter(function(rule) { return rule.kind === "prepend" })
  readonly property var effectiveRules: ownHeadRules.concat(omihomo.subscriptionRules)
  readonly property var ruleTest: Model.testRules(effectiveRules, ruleQuery)
  readonly property string ruleHitTarget: ruleTest.hit >= 0 ? effectiveRules[ruleTest.hit].target : ""
  readonly property var ruleTally: Model.ruleTargetTally(effectiveRules)
  // The rules view's one list: a heading, your rules, a heading, the
  // subscription's. `eff` is the rule's place in `effectiveRules`, or -1 for
  // one of yours that is appended or filtered rather than prepended.
  readonly property var ruleEntries: {
    var out = [{ kind: "head", title: "YOURS · " + omihomo.rules.length, note: "checked first" }]
    var head = 0
    var i
    for (i = 0; i < omihomo.rules.length; i++) {
      var rule = omihomo.rules[i]
      out.push({ kind: "own", i: i, rule: rule, eff: rule.kind === "prepend" ? head++ : -1 })
    }
    out.push({
      kind: "head",
      title: (omihomo.activeSubscription !== "" ? omihomo.activeSubscription.toUpperCase() : "SUBSCRIPTION")
        + " · " + omihomo.subscriptionRules.length,
      note: "read-only"
    })
    for (i = 0; i < omihomo.subscriptionRules.length; i++)
      out.push({ kind: "sub", i: i, rule: omihomo.subscriptionRules[i], eff: head + i })
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

  // An error is shown next to whatever caused it when that is on screen, and
  // under the header otherwise, so it is never out of sight.
  readonly property bool errorInView: omihomo.lastError !== "" && (view === "subs"
    ? omihomo.errorScope.indexOf("sub:") === 0 : omihomo.errorScope === view)
  readonly property bool headerError: omihomo.lastError !== "" && !errorInView
  readonly property string headerMessage: headerError ? omihomo.lastError : omihomo.actionStatus
  readonly property bool subscriptionFailed: omihomo.lastError !== "" && omihomo.errorScope.indexOf("sub:") === 0

  // Canvas styles are CSS strings; a QML colour would lose its alpha.
  function css(c) {
    return "rgba(" + Math.round(c.r * 255) + "," + Math.round(c.g * 255) + "," + Math.round(c.b * 255) + "," + c.a + ")"
  }

  function errorAt(scope) {
    return omihomo.lastError !== "" && omihomo.errorScope === scope
  }

  // Delay buckets for the latency pills and their legend, as how much of the
  // urgent colour is mixed in.
  function latencyHeat(ms) {
    if (ms < 50) return 0.08
    if (ms < 80) return 0.22
    if (ms < 130) return 0.4
    return 0.62
  }

  function shortRuleType(type) {
    var t = String(type)
    if (t === "DOMAIN-SUFFIX") return "suffix"
    if (t === "DOMAIN-KEYWORD") return "keyword"
    if (t === "IP-CIDR" || t === "IP-CIDR6") return "cidr"
    if (t === "PROCESS-NAME") return "process"
    return t.toLowerCase()
  }

  // ---------------------------------------------------------------- cursor

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
      rows.push({ s: "ruleTest" })
      for (i = 0; i < omihomo.rules.length; i++) rows.push({ s: "rule", i: i })
      for (i = 0; i < omihomo.subscriptionRules.length; i++) rows.push({ s: "srule", i: i })
      rows.push({ s: "ruleType" }, { s: "ruleValue" }, { s: "ruleTarget" }, { s: "ruleSubmit" })
      break
    case "conns":
      for (i = 0; i < omihomo.connectionStacks.length; i++) rows.push({ s: "conn", i: i })
      break
    case "settings":
      rows.push({ s: "mode" }, { s: "tun" }, { s: "tunRedirect" }, { s: "autostart" })
      if (omihomo.tailscalePresent) rows.push({ s: "tailscale" }, { s: "tailnetProxy" })
      if (!omihomo.permissionsOk) rows.push({ s: "repair" })
      rows.push({ s: "uninstall" })
      break
    }
    return rows
  }

  readonly property var navRows: navRowsFor()
  readonly property var cursorRow: cursor >= 0 && cursor < navRows.length ? navRows[cursor] : null
  // Leaving the row disarms it, so a confirm can never be inherited by whatever
  // the cursor lands on next.
  onCursorRowChanged: {
    if (uninstallArmed && (!cursorRow || cursorRow.s !== "uninstall")) uninstallArmed = false
    if (removeArmed !== "") {
      var sub = cursorRow && cursorRow.s === "sub" ? subscriptionAt(cursorRow.i) : null
      if (!sub || sub.name !== removeArmed) removeArmed = ""
    }
  }

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
    case "ruleTarget": cycleRuleTarget(1); break
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
      if (key === "/") { focusRow("ruleTest"); beginEdit(ruleTestField) }
    } else if (view === "conns") {
      // Not "X": PanelKeyCatcher takes both cases of x as its delete key, so an
      // uppercase one never gets here.
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
    if (view !== "configs") { openView("configs"); return }
    close()
  }

  // The hint bar is the same keys under a pointer.
  function runHint(key) {
    if (key === "enter") { cursorActive = true; activateCursor() }
    else if (key === "x") { cursorActive = true; deleteCursorRow() }
    else if (key === "esc") handleEscape()
    else if (key === "j") moveCursor(0, 1)
    else handleTextKey(key)
  }

  function hintsFor(name) {
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

  // ---------------------------------------------------------------- actions

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
    if (name === "conns") omihomo.refreshConnections()
    // The cursor starts on the selected config, so j/k moves from there.
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

  // The first request arms the row; the second removes.
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

  // A group that picks for itself (URLTest, Fallback) has nothing to select.
  function chooseConfigAt(index) {
    var name = visibleConfigs[index]
    if (name && browsedGroupEntry && browsedGroupEntry.selectable) omihomo.selectConfig(browsedGroup, name)
  }

  function testSelectedConfig() {
    if (cursorRow && cursorRow.s === "config") omihomo.testConfig(visibleConfigs[cursorRow.i])
    else if (omihomo.currentConfig !== "") omihomo.testConfig(omihomo.currentConfig)
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
  // somewhere to go. The first activation only arms the row.
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

  // `n` means "new rule" from anywhere: it opens the rules view with the
  // pattern field taking keys.
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

  // Each view scrolls one list, so the cursor only ever has to bring a row of
  // that list into view; everything else in a view is always on screen.
  function scrollCursorIntoView() {
    if (!cursorRow) return
    var list = null
    var index = cursorRow.i
    switch (cursorRow.s) {
    case "config": list = configList; break
    case "sub": list = subList; break
    case "rule": list = ruleList; index = 1 + cursorRow.i; break
    case "srule": list = ruleList; index = 2 + omihomo.rules.length + cursorRow.i; break
    case "conn": list = connList; break
    }
    if (list) Qt.callLater(function() { list.positionViewAtIndex(index, ListView.Contain) })
    else if (view === "settings") scrollSettingIntoView(settingItems[cursorRow.s] || null)
  }

  // Settings rows register by section, so the cursor can bring one into view
  // when the list is taller than the popup.
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
      // The reads that opening triggers live in Service.onPanelOpenChanged,
      // which runs after this handler and after `panelOpen` is actually true.
      Qt.callLater(function() { keyCatcher.forceActiveFocus() })
    }
  }

  onNavRowsChanged: clampCursor()

  // A failed subscription shows its error on its own card, so that card is
  // brought into view rather than leaving the message scrolled away.
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
    // One fixed surface for every view, so switching tabs never moves or
    // resizes the popup under the pointer.
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(Style.space(660))

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
        spacing: Style.space(10)

        // ---- header ---------------------------------------------------------
        //
        // Always the core: its subscription, state, switch, and traffic. The
        // configs and subscriptions views get the full readout; the rest fold
        // it into one line to leave room for their lists.

        ColumnLayout {
          Layout.fillWidth: true
          spacing: Style.space(8)

          RowLayout {
            Layout.fillWidth: true
            spacing: Style.space(12)

            OmihomoIcon {
              iconSize: Style.space(24)
              color: omihomo.coreActive ? root.foreground : root.dim
              crossed: omihomo.installed && !omihomo.coreActive
            }

            ColumnLayout {
              Layout.fillWidth: true
              spacing: Style.space(2)

              Text {
                Layout.fillWidth: true
                text: omihomo.activeSubscription !== "" ? omihomo.activeSubscription : "Omihomo"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.title
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

                Text {
                  Layout.fillWidth: true
                  text: root.compactHeader && omihomo.coreRunning
                    ? Model.stateLabel(omihomo.coreState).toLowerCase() + " · " + omihomo.effectiveMode
                    : root.statusText
                  color: omihomo.coreState === "degraded" ? root.urgent : root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.bodySmall
                  elide: Text.ElideRight
                }
              }
            }

            Column {
              visible: root.compactHeader && omihomo.installed
              spacing: 0

              Text {
                text: "↓ " + Model.formatRate(omihomo.downloadRate)
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }

              Text {
                text: "↑ " + Model.formatRate(omihomo.uploadRate)
                color: root.accent
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
            }

            TrafficGraph {
              visible: root.compactHeader && omihomo.installed
              Layout.preferredWidth: Style.space(64)
              Layout.preferredHeight: Style.space(24)
            }

            ToggleSwitch {
              id: powerSwitch
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
            Layout.fillWidth: true
            spacing: Style.space(12)

            Text {
              text: "↓ " + Model.formatRate(omihomo.downloadRate)
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              font.bold: true
            }

            Text {
              Layout.fillWidth: true
              text: "↑ " + Model.formatRate(omihomo.uploadRate)
              color: root.accent
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              font.bold: true
            }
          }

          TrafficGraph {
            visible: !root.compactHeader && omihomo.installed
            Layout.fillWidth: true
            Layout.preferredHeight: Style.space(44)
          }

          RowLayout {
            visible: !root.compactHeader && omihomo.installed
            Layout.fillWidth: true
            spacing: Style.space(8)

            Readout { Layout.horizontalStretchFactor: 27; label: "UPTIME"; value: root.uptimeText }
            Readout {
              Layout.horizontalStretchFactor: 46
              label: "EGRESS"
              value: root.egressText
              faded: omihomo.traceFailed && !omihomo.traceTesting
              tooltip: "Check again · r"
              onActivated: omihomo.refreshTrace()
            }
            Readout { Layout.horizontalStretchFactor: 27; label: "LATENCY"; value: root.latencyText }
          }
        }

        Text {
          visible: root.headerMessage !== ""
          Layout.fillWidth: true
          text: (root.headerError ? "▲ " : "") + root.headerMessage
          color: root.headerError ? root.urgent : root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }

        // ---- core is not installed -------------------------------------------

        ColumnLayout {
          visible: !omihomo.installed
          Layout.fillWidth: true
          Layout.fillHeight: true
          spacing: Style.space(10)

          Text {
            Layout.fillWidth: true
            text: "The mihomo core is not installed. Installing builds it from the AUR, so it runs in a terminal."
            color: root.dim
            font.family: root.fontFamily
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

        // ---- tabs -------------------------------------------------------------

        RowLayout {
          visible: omihomo.installed
          Layout.fillWidth: true
          spacing: Style.space(4)

          ViewTab { target: "configs"; number: 1; label: "Configs"; count: root.browsedGroupEntry ? String(root.browsedGroupEntry.all.length) : "" }
          ViewTab { target: "subs"; number: 2; label: "Subs"; count: root.subscriptionFailed ? "!" : String(omihomo.subscriptions.length); alert: root.subscriptionFailed }
          ViewTab { target: "rules"; number: 3; label: "Rules"; count: String(root.effectiveRules.length) }
          ViewTab { target: "conns"; number: 4; label: "Conns"; count: String(omihomo.openConnectionCount) }

          CursorSurface {
            id: gear
            Layout.preferredWidth: Style.space(36)
            implicitHeight: Style.space(30)
            current: root.view === "settings"
            hasCursor: gearMouse.containsMouse
            bordered: true
            foreground: root.needsRepair || root.tunRedirectBlocked ? root.urgent : root.foreground
            fill: root.hoverFill
            currentFill: root.selectedFill

            Text {
              anchors.centerIn: parent
              text: "󰒓"
              color: gear.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.icon
            }

            MouseArea {
              id: gearMouse
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onContainsMouseChanged: if (containsMouse) root.cursorActive = false
              onClicked: root.openView(root.view === "settings" ? "configs" : "settings")
            }

            PanelToolTip {
              visible: gearMouse.containsMouse
              text: root.needsRepair ? "Settings · needs repair · M" : "Settings · M"
              fontFamily: root.fontFamily
            }
          }
        }

        // ---- views --------------------------------------------------------------

        Item {
          visible: omihomo.installed
          Layout.fillWidth: true
          Layout.fillHeight: true

          // ---- configs

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "configs"
            spacing: Style.space(8)

            // An empty view always says why, because a bare list reads as a
            // panel that lost its data rather than one with none to show.
            Text {
              visible: !root.liveReady || (omihomo.proxiesLoaded && root.visibleGroups.length === 0)
              Layout.fillWidth: true
              text: !root.liveReady
                ? (omihomo.activeSubscription === "" ? "Add and activate a subscription in Subs to browse its configs."
                  : (omihomo.coreRunning ? "mihomo controller is unreachable." : "Start mihomo to browse configs."))
                : "This subscription declares no groups."
              color: root.dim
              font.family: root.fontFamily
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

            TextField {
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
              spacing: Style.space(2)
              model: root.liveReady ? root.visibleConfigs : []

              delegate: ConfigRow {
                required property var modelData
                required property int index
                width: configList.rowWidth
                configName: String(modelData)
                rowIndex: index
              }
            }

            Text {
              visible: root.errorAt("configs")
              Layout.fillWidth: true
              text: "▲ " + omihomo.lastError
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }

            RowLayout {
              visible: root.browsedGroupEntry !== null
              Layout.fillWidth: true
              spacing: Style.space(6)

              Text {
                text: omihomo.pingTestsRunning ? "testing…" : root.testedText
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }

              SmallButton {
                text: "test all"
                enabled: !omihomo.pingTestsRunning
                onClicked: omihomo.testGroup(root.browsedGroup)
              }

              Item { Layout.fillWidth: true }

              Text {
                text: "fast"
                color: root.faint
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }

              Repeater {
                model: [30, 60, 100, 200]
                Rectangle {
                  required property var modelData
                  implicitWidth: Style.space(14)
                  implicitHeight: Style.space(8)
                  radius: Math.min(Style.cornerRadius, 2)
                  color: Qt.tint(Util.alpha(root.foreground, 0.08), Util.alpha(root.urgent, root.latencyHeat(modelData)))
                }
              }

              Rectangle {
                implicitWidth: Style.space(14)
                implicitHeight: Style.space(8)
                radius: Math.min(Style.cornerRadius, 2)
                color: root.urgent
              }

              Text {
                text: "slow"
                color: root.faint
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
            }
          }

          // ---- subscriptions

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "subs"
            spacing: Style.space(8)

            SectionLine {
              Layout.fillWidth: true
              title: "SUBSCRIPTIONS"
              note: omihomo.activeSubscription !== "" ? "active " + omihomo.activeSubscription : ""
            }

            Text {
              visible: omihomo.subscriptions.length === 0
              Layout.fillWidth: true
              text: "No subscriptions yet. Paste a URL below."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }

            ScrollList {
              id: subList
              Layout.fillWidth: true
              Layout.fillHeight: true
              spacing: Style.space(6)
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

              TextField {
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

            Text {
              visible: root.errorAt("sub:")
              Layout.fillWidth: true
              text: "▲ " + omihomo.lastError
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
          }

          // ---- rules

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "rules"
            spacing: Style.space(8)

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(8)

              Text {
                text: "TEST"
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                font.bold: true
                font.letterSpacing: 1.2
              }

              TextField {
                id: ruleTestField
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

              Text {
                visible: root.ruleTest.hit >= 0
                text: "→ " + root.ruleHitTarget
                color: root.accent
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                font.bold: true
              }

              Text {
                Layout.fillWidth: true
                text: root.ruleTest.host === "" ? "Type a domain or an IP to see which rule catches it."
                  : (root.ruleTest.hit < 0 ? "No rule matches."
                    : "rule " + (root.ruleTest.hit + 1) + " of " + root.effectiveRules.length
                      + (root.ruleTest.unknown > 0 ? " · " + root.ruleTest.unknown + " above depend on DNS, rule sets, or the app" : ""))
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                elide: Text.ElideRight
              }
            }

            // Where the rules send traffic, one segment per target, with the
            // tested host's target lit.
            Row {
              id: tallyBar
              visible: root.ruleTally.length > 0
              Layout.fillWidth: true
              Layout.preferredHeight: Style.space(6)
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
                Text {
                  required property var modelData
                  text: modelData.count + " " + modelData.target
                  color: modelData.target === root.ruleHitTarget ? root.accent : root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.bodySmall
                }
              }
            }

            ScrollList {
              id: ruleList
              Layout.fillWidth: true
              Layout.fillHeight: true
              model: root.ruleEntries

              delegate: Item {
                id: ruleEntry
                required property var modelData
                width: ruleList.rowWidth
                height: modelData.kind === "head" ? ruleHead.implicitHeight + Style.space(8) : ruleRow.implicitHeight

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
                  id: ruleRow
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

              TextField {
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
                tooltipText: "Target · h l"
                hasCursor: root.at("ruleTarget")
                onHovered: function(on) { if (on) root.focusRow("ruleTarget") }
                onClicked: root.cycleRuleTarget(1)
              }

              SmallButton {
                text: omihomo.ruleSaving ? "saving…" : "+ add"
                enabled: !omihomo.ruleSaving
                hasCursor: root.at("ruleSubmit")
                onHovered: function(on) { if (on) root.focusRow("ruleSubmit") }
                onClicked: root.submitRuleForm()
              }
            }

            Text {
              readonly property string message: root.ruleFormValue !== "" && root.ruleFormError !== ""
                ? root.ruleFormError : (root.errorAt("rules") ? omihomo.lastError : "")
              visible: message !== ""
              Layout.fillWidth: true
              text: "▲ " + message
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }
          }

          // ---- connections

          ColumnLayout {
            anchors.fill: parent
            visible: root.view === "conns"
            spacing: Style.space(8)

            RowLayout {
              Layout.fillWidth: true

              Text {
                Layout.fillWidth: true
                text: "↓ " + Model.formatBytes(omihomo.connectionsDownload)
                  + "  ↑ " + Model.formatBytes(omihomo.connectionsUpload) + " total"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }

              Text {
                text: "process → config · width = bytes"
                color: root.faint
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
            }

            FlowDiagram {
              Layout.fillWidth: true
              Layout.preferredHeight: Math.max(Style.space(44),
                Style.space(22) * Math.max(root.flows.sources.length, root.flows.targets.length))
            }

            SectionLine {
              Layout.fillWidth: true
              title: "CONNECTIONS"
              note: omihomo.openConnectionCount + " open · " + root.closedLogged + " closed"
            }

            Text {
              visible: omihomo.connectionStacks.length === 0
              Layout.fillWidth: true
              text: root.liveReady ? "Nothing logged yet." : "mihomo is not running."
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }

            ScrollList {
              id: connList
              Layout.fillWidth: true
              Layout.fillHeight: true
              spacing: Style.space(2)
              model: omihomo.connectionStacks

              delegate: ConnectionRow {
                required property var modelData
                required property int index
                width: connList.rowWidth
                stack: modelData
                rowIndex: index
              }
            }

            Text {
              visible: root.errorAt("conns")
              Layout.fillWidth: true
              text: "▲ " + omihomo.lastError
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.WordWrap
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.space(6)

              SmallButton {
                text: "close all"
                foreground: root.urgent
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

          // ---- settings

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
              spacing: Style.space(4)

              Text {
                visible: root.errorAt("settings")
                Layout.fillWidth: true
                text: "▲ " + omihomo.lastError
                color: root.urgent
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
              }

              SectionLine { Layout.fillWidth: true; title: "ROUTING"; note: "applies immediately" }

              SettingRow {
                Layout.fillWidth: true
                section: "mode"
                title: "Mode"
                keyHint: "m"
                subtitle: "rule follows the rules; global sends everything to " + (omihomo.primaryGroup || "the proxy")
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

              // The repair for a TUN that will not start on this machine. It is
              // urgent only while the core is actually degraded by it.
              SettingRow {
                Layout.fillWidth: true
                section: "tunRedirect"
                title: "TUN acceleration"
                keyHint: "A"
                subtitle: root.tunRedirectBlocked ? "blocking TUN on this machine: turn it off"
                  : "faster; turn off if TUN fails to start"
                urgentSubtitle: root.tunRedirectBlocked
                checked: omihomo.tunRedirectActive
                onActivated: omihomo.toggleTunRedirect()
              }

              SectionLine { Layout.fillWidth: true; Layout.topMargin: Style.space(8); title: "SYSTEM" }

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
                title: "Tailscale"
                keyHint: "T"
                subtitle: "keep the tailnet reachable under TUN"
                checked: omihomo.tailscaleActive
                onActivated: omihomo.toggleTailscale()
              }

              SettingRow {
                Layout.fillWidth: true
                visible: omihomo.tailscalePresent
                section: "tailnetProxy"
                title: "Tailnet via proxy"
                keyHint: "P"
                subtitle: "send tailnet addresses to the proxy server, which must be on the tailnet"
                checked: omihomo.tailnetProxyActive
                onActivated: omihomo.toggleTailnetProxy()
              }

              SectionLine { Layout.fillWidth: true; Layout.topMargin: Style.space(8); title: "MAINTENANCE" }

              Text {
                Layout.fillWidth: true
                Layout.leftMargin: Style.space(8)
                text: "core " + Model.stateLabel(omihomo.coreState).toLowerCase() + " · "
                  + (omihomo.permissionsOk ? "root grant owner-only" : "missing root permissions, or open to other accounts")
                color: omihomo.permissionsOk ? root.dim : root.urgent
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
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
                  visible: !omihomo.permissionsOk
                  text: "↻ repair permissions · R"
                  foreground: root.urgent
                  hasCursor: root.at("repair")
                  onHovered: function(on) { if (on) root.focusRow("repair") }
                  onClicked: omihomo.repairCore()
                }

                SmallButton {
                  text: root.uninstallArmed ? "confirm uninstall" : "uninstall…"
                  foreground: root.urgent
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

              Text {
                visible: root.uninstallArmed
                Layout.fillWidth: true
                Layout.leftMargin: Style.space(8)
                text: "Removes the core, its permissions, the unit, and saved subscriptions."
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
              }
            }
          }
        }

        // ---- key hints ------------------------------------------------------------

        Row {
          visible: omihomo.installed
          Layout.fillWidth: true
          spacing: Style.space(10)

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

  // ---------------------------------------------------------------- pieces

  // A row the cursor can land on. Hover moves the cursor here, so mouse and
  // keyboard paint the same single highlight.
  component NavRow: CursorSurface {
    id: navRow

    property string section: ""
    property int rowIndex: -1
    readonly property alias hovered: navMouse.containsMouse

    signal activated()

    hasCursor: root.at(navRow.section, navRow.rowIndex < 0 ? undefined : navRow.rowIndex)
    foreground: root.foreground
    fill: root.hoverFill
    currentFill: root.selectedFill

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

  // The one scrolling list of a view. Its edges fade while there is more to
  // scroll to, and the bar stays visible, so a cut-off list never looks complete.
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

  component SmallButton: Button {
    bordered: true
    foreground: root.foreground
    fontFamily: root.fontFamily
    fontSize: Style.font.bodySmall
    horizontalPadding: Style.space(8)
    verticalPadding: Style.space(3)
    opacity: enabled ? 1 : 0.4
  }

  component SectionLine: RowLayout {
    id: sectionLine

    property string title: ""
    property string note: ""

    spacing: Style.space(8)

    Text {
      text: sectionLine.title
      color: root.dim
      font.family: root.fontFamily
      font.pixelSize: Style.font.caption
      font.bold: true
      font.letterSpacing: 1.2
    }

    Item { Layout.fillWidth: true }

    Text {
      visible: text !== ""
      text: sectionLine.note
      color: root.faint
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
  }

  // A small outlined label: a badge on a subscription, a target on a rule or a
  // connection.
  component Tag: Rectangle {
    id: tag

    property string text: ""
    property color tint: root.dim
    property bool strong: false
    property real maxWidth: Style.space(110)

    implicitWidth: Math.min(tagMetrics.advanceWidth, maxWidth) + Style.space(10)
    implicitHeight: tagText.implicitHeight + Style.space(2)
    radius: Math.min(Style.cornerRadius, height / 2)
    color: strong ? Util.alpha(tint, 0.16) : "transparent"
    border.width: 1
    border.color: Util.alpha(tint, strong ? 0.9 : 0.35)

    TextMetrics {
      id: tagMetrics
      text: tag.text
      font: tagText.font
    }

    Text {
      id: tagText
      anchors.centerIn: parent
      width: Math.min(tagMetrics.advanceWidth, tag.maxWidth)
      text: tag.text
      color: tag.tint
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideRight
      horizontalAlignment: Text.AlignHCenter
    }
  }

  component Radio: Item {
    property bool checked: false
    property bool pending: false

    implicitWidth: Style.space(12)
    implicitHeight: Style.space(12)

    Rectangle {
      anchors.fill: parent
      radius: width / 2
      color: "transparent"
      border.width: Math.max(1, Style.space(1.5))
      border.color: parent.checked || parent.pending ? root.accent : root.faint
    }

    Rectangle {
      visible: parent.checked || parent.pending
      anchors.centerIn: parent
      width: parent.width / 2
      height: width
      radius: width / 2
      color: parent.pending ? root.dim : root.accent
    }
  }

  component Kbd: Rectangle {
    property string text: ""

    implicitWidth: Math.max(kbdText.implicitWidth + Style.space(8), Style.space(16))
    implicitHeight: kbdText.implicitHeight + Style.space(2)
    radius: Math.min(Style.cornerRadius, 4)
    color: Util.alpha(root.foreground, 0.06)
    border.width: 1
    border.color: Util.alpha(root.foreground, 0.2)

    Text {
      id: kbdText
      anchors.centerIn: parent
      text: parent.text
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.caption
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

      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: hint.label
        color: hintMouse.containsMouse ? root.foreground : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
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

  component ViewTab: CursorSurface {
    id: tab

    property string target: ""
    property int number: 0
    property string label: ""
    property string count: ""
    property bool alert: false

    Layout.fillWidth: true
    implicitHeight: Style.space(30)
    current: root.view === tab.target
    hasCursor: tabMouse.containsMouse
    bordered: true
    foreground: root.foreground
    fill: root.hoverFill
    currentFill: root.selectedFill

    Row {
      anchors.centerIn: parent
      spacing: Style.space(5)

      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: String(tab.number)
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }

      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: tab.label
        color: tab.current ? root.foreground : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        font.bold: tab.current
      }

      Rectangle {
        visible: tab.count !== ""
        anchors.verticalCenter: parent.verticalCenter
        implicitWidth: Math.max(countText.implicitWidth + Style.space(6), Style.space(14))
        implicitHeight: countText.implicitHeight
        radius: Math.min(Style.cornerRadius, height / 2)
        color: tab.alert ? root.urgent : Util.alpha(root.foreground, 0.1)

        Text {
          id: countText
          anchors.centerIn: parent
          text: tab.count
          color: tab.alert ? root.surface : root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          font.bold: tab.alert
        }
      }
    }

    MouseArea {
      id: tabMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onContainsMouseChanged: if (containsMouse) root.cursorActive = false
      onClicked: root.openView(tab.target)
    }

    PanelToolTip {
      visible: tabMouse.containsMouse
      text: tab.label + " · " + tab.number
      fontFamily: root.fontFamily
    }
  }

  // Label over value, one column of the header's readout.
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

      Text {
        text: readout.label
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        font.bold: true
        font.letterSpacing: 1.2
      }

      Text {
        width: parent.width
        text: readout.value
        color: readout.faded ? root.dim : root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.body
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

  // The last minute of `/traffic`: download in the foreground colour, upload in
  // the accent, both against the larger of the two.
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

  // Open traffic as process → config flows: one curve per pair, as thick as the
  // bytes it carried, with the pair under the cursor in the accent.
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
      for (i = 0; i < flows.links.length; i++) {
        var link = flows.links[i]
        var y1 = rowY(indexOf(sources, link.source), sources.length)
        var y2 = rowY(indexOf(targets, link.target), targets.length)
        var middle = (left + right) / 2
        ctx.beginPath()
        ctx.moveTo(left + 4, y1)
        ctx.bezierCurveTo(middle, y1, middle, y2, right - 4, y2)
        ctx.lineWidth = Math.max(1.5, Math.min(12, 3.5 * Math.log(Math.max(1, link.bytes / 2e4)) / Math.LN10))
        ctx.strokeStyle = root.css(isHot(link.source, link.target) ? root.accent : Util.alpha(root.foreground, 0.22))
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
    implicitWidth: chipRow.implicitWidth + Style.space(16)
    implicitHeight: chipRow.implicitHeight + Style.space(8)
    onActivated: root.browseGroupAt(chip.rowIndex)

    Row {
      id: chipRow
      anchors.centerIn: parent
      spacing: Style.space(6)

      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: chip.groupName
        color: chip.current ? root.foreground : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
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

  component ConfigRow: NavRow {
    id: configRow

    property string configName: ""
    readonly property bool selected: root.browsedGroupEntry !== null && root.browsedGroupEntry.now === configRow.configName
    readonly property bool pending: omihomo.pendingConfig === configRow.configName
    readonly property string testState: omihomo.configTestState(configRow.configName)
    readonly property int delay: omihomo.configDelay(configRow.configName)

    section: "config"
    current: selected || pending
    implicitHeight: Style.space(28)
    onActivated: root.chooseConfigAt(configRow.rowIndex)

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(4)
      spacing: Style.space(8)

      Radio {
        checked: configRow.selected
        pending: configRow.pending && !configRow.selected
      }

      Text {
        Layout.fillWidth: true
        text: configRow.configName
        color: root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.body
        font.bold: configRow.selected
        elide: Text.ElideRight
      }

      // The delay is the colour: the slower the config, the more of the urgent
      // colour the pill takes, and a failure takes all of it.
      Rectangle {
        id: pill

        readonly property bool failed: configRow.testState === "failed"
        readonly property bool testing: configRow.testState === "testing"

        implicitWidth: Math.max(pillText.implicitWidth + Style.space(12), Style.space(54))
        implicitHeight: pillText.implicitHeight + Style.space(2)
        radius: Math.min(Style.cornerRadius, height / 2)
        color: pill.failed ? root.urgent
          : (configRow.delay > 0 && !pill.testing
            ? Qt.tint(Util.alpha(root.foreground, 0.08), Util.alpha(root.urgent, root.latencyHeat(configRow.delay)))
            : "transparent")

        Text {
          id: pillText
          anchors.centerIn: parent
          text: pill.testing ? "···" : (pill.failed ? "timeout" : Model.formatDelay(configRow.delay))
          color: pill.failed ? root.surface : (configRow.delay > 0 ? root.foreground : root.faint)
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall

          SequentialAnimation on opacity {
            running: pill.testing
            loops: Animation.Infinite
            alwaysRunToEnd: true
            NumberAnimation { from: 1; to: 0.3; duration: 450 }
            NumberAnimation { from: 0.3; to: 1; duration: 450 }
          }
        }
      }

      PanelActionButton {
        iconText: "󰓅"
        tooltipText: "Test latency · d"
        foreground: root.foreground
        fontFamily: root.fontFamily
        size: Style.space(20)
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

    section: "sub"
    current: active
    bordered: true
    implicitHeight: cardColumn.implicitHeight + Style.space(14)
    onActivated: root.activateSubscriptionAt(card.rowIndex)

    ColumnLayout {
      id: cardColumn
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(8)
      anchors.rightMargin: Style.space(6)
      spacing: Style.space(6)

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(8)

        Radio { checked: card.active; pending: card.pending && !card.active }

        Text {
          Layout.fillWidth: true
          text: card.subName
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          font.bold: card.active
          elide: Text.ElideRight
        }

        Tag {
          text: card.pending ? "working…" : (card.failed ? "failed" : (card.active ? "active"
            : "updated " + (Model.relativeTime(card.subscription ? card.subscription.updatedAt : "", root.nowMs) || "never")))
          tint: card.failed ? root.urgent : (card.active ? root.accent : root.dim)
          strong: card.active || card.failed
        }

        SmallButton {
          text: card.pending ? "…" : "↻ update"
          enabled: !card.pending
          tooltipText: "Update · u"
          onClicked: omihomo.updateSubscription(card.subName)
        }

        PanelActionButton {
          iconText: "󰅙"
          tooltipText: "Remove · x"
          foreground: root.foreground
          hoverColor: root.urgent
          fontFamily: root.fontFamily
          size: Style.space(20)
          onClicked: root.removeSubscription(card.subName)
        }
      }

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(8)

        Rectangle {
          visible: card.usage.fraction >= 0
          implicitWidth: Style.space(84)
          implicitHeight: Style.space(4)
          radius: Math.min(Style.cornerRadius, 2)
          color: Util.alpha(root.foreground, 0.12)

          Rectangle {
            width: parent.width * Math.max(0, card.usage.fraction)
            height: parent.height
            radius: parent.radius
            color: card.usage.fraction > 0.9 ? root.urgent : root.foreground
          }
        }

        Text {
          Layout.fillWidth: true
          text: card.usage.text + (card.active ? " · updated "
            + (Model.relativeTime(card.subscription ? card.subscription.updatedAt : "", root.nowMs) || "never") : "")
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          elide: Text.ElideRight
        }
      }

      RowLayout {
        visible: card.failed
        Layout.fillWidth: true
        spacing: Style.space(8)

        Text {
          Layout.fillWidth: true
          text: "▲ " + omihomo.lastError
          color: root.urgent
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }

        SmallButton {
          text: "↻ retry"
          foreground: root.urgent
          onClicked: omihomo.updateSubscription(card.subName)
        }
      }

      RowLayout {
        visible: root.removeArmed === card.subName
        Layout.fillWidth: true
        spacing: Style.space(6)

        Text {
          Layout.fillWidth: true
          text: "Remove " + card.subName + "?"
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
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

  // One step of the rule pipeline. With a host in the tester, rules above the
  // hit say why they passed ("no", or "?" when only the core can tell), the hit
  // is lit, and everything below it fades.
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

    section: own ? "rule" : "srule"
    rowIndex: entry.i === undefined ? -1 : entry.i
    current: hit
    implicitHeight: Style.space(24)
    opacity: below ? 0.4 : 1

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(4)
      anchors.rightMargin: Style.space(2)
      spacing: Style.space(8)

      Item {
        Layout.fillHeight: true
        implicitWidth: Style.space(8)

        Rectangle {
          anchors.horizontalCenter: parent.horizontalCenter
          width: 1
          height: parent.height
          color: root.line
        }

        Rectangle {
          anchors.centerIn: parent
          width: Style.space(ruleRow.hit ? 8 : 5)
          height: width
          radius: width / 2
          color: ruleRow.hit ? root.accent : (ruleRow.verdict !== "" ? root.dim : root.faint)
        }
      }

      Text {
        Layout.preferredWidth: Style.space(56)
        text: root.shortRuleType(ruleRow.rule ? ruleRow.rule.type : "")
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        elide: Text.ElideRight
      }

      Text {
        Layout.fillWidth: true
        text: ruleRow.rule ? (ruleRow.rule.value !== "" ? ruleRow.rule.value : "everything else") : ""
        color: ruleRow.own || ruleRow.hit ? root.foreground : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        elide: Text.ElideRight
      }

      Text {
        visible: ruleRow.own && ruleRow.rule && ruleRow.rule.kind !== "prepend"
        text: ruleRow.rule ? ruleRow.rule.kind : ""
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
      }

      Tag {
        text: ruleRow.rule ? ruleRow.rule.target : ""
        tint: ruleRow.hit ? root.accent : root.dim
        strong: ruleRow.hit
        maxWidth: Style.space(84)
      }

      Text {
        Layout.preferredWidth: Style.space(22)
        text: ruleRow.hit ? "hit" : (ruleRow.verdict === "miss" ? "no" : (ruleRow.verdict === "unknown" ? "?" : ""))
        color: ruleRow.hit ? root.accent : root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
      }

      Item {
        implicitWidth: Style.space(18)
        implicitHeight: Style.space(18)

        PanelActionButton {
          visible: ruleRow.own
          anchors.fill: parent
          iconText: "󰅙"
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

  // One stack on one line. The bar behind it is the bytes it moved, on a log
  // scale so a game download does not flatten every other row. The leading mark
  // and the brightness say open or closed, because the panel has no colour to
  // spare for a third state.
  component ConnectionRow: NavRow {
    id: connectionRow

    property var stack: null
    readonly property bool live: !!stack && stack.open
    readonly property double bytes: stack ? stack.download + stack.upload : 0

    section: "conn"
    implicitHeight: Style.space(24)
    onActivated: root.activateConnectionAt(connectionRow.rowIndex)

    Rectangle {
      anchors.left: parent.left
      anchors.top: parent.top
      anchors.bottom: parent.bottom
      anchors.margins: 1
      width: (parent.width - 2) * Math.max(0.02, Math.min(1, root.largestStack > 5000 && connectionRow.bytes > 5000
        ? Math.log(connectionRow.bytes / 5000) / Math.log(root.largestStack / 5000) : 0))
      radius: Style.cornerRadius
      color: Util.alpha(connectionRow.live ? root.foreground : root.faint, 0.07)
    }

    RowLayout {
      anchors.fill: parent
      anchors.leftMargin: Style.space(6)
      anchors.rightMargin: Style.space(2)
      spacing: Style.space(6)

      Text {
        text: connectionRow.live ? "●" : "○"
        color: connectionRow.live ? root.foreground : root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }

      Text {
        Layout.maximumWidth: Style.space(96)
        text: connectionRow.stack ? (connectionRow.stack.process || "unknown") : ""
        color: connectionRow.live ? root.foreground : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        font.bold: connectionRow.live
        elide: Text.ElideRight
      }

      Text {
        visible: connectionRow.stack && connectionRow.stack.count > 1
        text: connectionRow.stack ? "×" + connectionRow.stack.count : ""
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
      }

      Text {
        Layout.fillWidth: true
        text: connectionRow.stack ? connectionRow.stack.host : ""
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        elide: Text.ElideRight
      }

      Tag {
        visible: text !== ""
        text: connectionRow.stack ? connectionRow.stack.chain : ""
        maxWidth: Style.space(80)
      }

      Text {
        text: Model.formatBytes(connectionRow.bytes)
        color: connectionRow.live ? root.foreground : root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
      }

      PanelActionButton {
        iconText: connectionRow.live ? "󰅙" : "󰩹"
        tooltipText: connectionRow.live ? "Close · x" : "Remove from log · x"
        foreground: root.foreground
        hoverColor: root.urgent
        fontFamily: root.fontFamily
        size: Style.space(18)
        onClicked: root.activateConnectionAt(connectionRow.rowIndex)
      }
    }

    // A closed stack spends the slot the rule would have taken on when it
    // closed, which is the thing you read a log for.
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

  // A labelled switch, or the mode selector. The whole row is the control, so
  // the switch itself takes no clicks of its own.
  component SettingRow: NavRow {
    id: settingRow

    property string title: ""
    property string subtitle: ""
    property string keyHint: ""
    property bool urgentSubtitle: false
    property bool checked: false
    property bool modeControl: false

    implicitHeight: settingColumn.implicitHeight + Style.space(12)
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
          spacing: Style.space(6)

          Text {
            text: settingRow.title
            color: root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
          }

          Kbd { text: settingRow.keyHint; visible: text !== "" }
        }

        Text {
          Layout.fillWidth: true
          text: settingRow.subtitle
          color: settingRow.urgentSubtitle ? root.urgent : root.faint
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          wrapMode: Text.WordWrap
        }
      }

      ToggleSwitch {
        visible: !settingRow.modeControl
        interactive: false
        checked: settingRow.checked
        foreground: root.foreground
      }

      ButtonGroup {
        visible: settingRow.modeControl
        options: Model.MODES
        value: omihomo.effectiveMode
        foreground: root.foreground
        fontFamily: root.fontFamily
        fontSize: Style.font.bodySmall
        focusable: false
        onChanged: function(value) { omihomo.setMode(value) }
      }
    }
  }
}
