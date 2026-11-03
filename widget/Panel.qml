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
  property int editingRule: 0
  property bool ruleFormOpen: false
  property string editingRuleOriginal: ""
  property string savedRuleTarget: ""
  property string ruleParams: ""
  property string ruleCategory: ""
  property string newRuleCategory: ""
  property bool ruleEnabled: true
  property var expandedCategories: ({})
  property string categoryEditing: ""
  property string categoryTarget: ""
  readonly property var categoryChoices: [{ value: "", label: "No category" }].concat(
    omihomo.ruleDocument.filter(function(e) { return !!e.category }).map(function(e) {
      return { value: e.category.id, label: e.category.name }
    })).concat(newRuleCategory !== "" ? [{ value: "new", label: newRuleCategory }] : [])
  property int ruleTargetIndex: 0
  readonly property var activeSelector: rulesView.typeSelector.picking ? rulesView.typeSelector
    : rulesView.categorySelector.picking ? rulesView.categorySelector
    : rulesView.bulkSelector.picking ? rulesView.bulkSelector
    : rulesView.selector.picking ? rulesView.selector
    : (settingsView.selector.picking ? settingsView.selector : null)

  readonly property string subFormUrl: subscriptionsView.urlField.text.trim()
  readonly property string ruleFormValue: rulesView.valueField.text
  readonly property string ruleQuery: rulesView.testField.text

  property Item editing: null

  property double nowMs: Date.now()

  readonly property Theme theme: Theme { bar: root.bar; emoji: EmojiImages {} }
  readonly property Service service: omihomo
  readonly property UpdateService updater: updateService
  readonly property color barIconColor: omihomo.coreActive ? barForeground : Qt.darker(barForeground, 1.55)

  readonly property string installerPath: decodeURIComponent(String(Qt.resolvedUrl("../bin/omihomo-install")).replace(/^file:\/\//, ""))
  // Without a daemon to talk to, the panel is one card saying why, and the
  // commands that fix it, run in a terminal since they ask for a password.
  readonly property var setup: {
    switch (omihomo.link) {
    case "absent": return { section: "SETUP", title: "Core not installed",
      text: "Omihomo runs mihomo as a system service that this panel controls. Installing it:",
      steps: ["verifies the latest signed release on GitHub", "installs the omihomo package",
        "claims the core for this account"],
      actions: [{ button: "Install core", tag: "install…", key: "i", hint: "asks for your password in a terminal",
        command: Util.shellQuote(installerPath) + " install" }] }
    case "outdated": return { section: "UPDATE REQUIRED", title: "Core is older than this widget",
      text: "The installed core speaks an older protocol, so this panel cannot use it until it is updated. Updating:",
      steps: ["updates this widget through Omarchy", "verifies the latest signed release on GitHub",
        "updates the omihomo package"],
      actions: [{ button: "Update", tag: "update…", key: "i", hint: "asks for your password in a terminal",
        command: Util.shellQuote(installerPath) + " update" }] }
    case "newer": return { section: "UPDATE REQUIRED", title: "Widget is older than the core",
      text: "The installed core speaks a newer protocol, so this widget cannot use it until it is updated. Updating:",
      steps: ["updates this widget through Omarchy", "updates the core too if a newer release is out"],
      actions: [{ button: "Update widget", tag: "update…", key: "i", hint: "runs in a terminal",
        command: Util.shellQuote(installerPath) + " update" }] }
    // A core whose setup stopped before claim refuses this account just as
    // one owned by another account does, so both ways out are offered.
    case "refused": return { section: "OWNERSHIP", title: "Core not available to this account",
      text: omihomo.lastError || "Setup has not finished, or another account owns the core.",
      steps: [], actions: [
        { button: "Finish setup", tag: "setup…", key: "i",
          hint: "resumes the install; never takes the core from another account",
          command: Util.shellQuote(installerPath) + " install" },
        { button: "Claim core", tag: "claim…", key: "c",
          hint: "moves control of the core to this account",
          command: Util.shellQuote(installerPath) + " claim" }] }
    case "busy": return { section: "CORE", title: "Core busy",
      text: omihomo.lastError, steps: [], actions: [] }
    }
    return { section: "CORE", title: "Connecting…", text: "Waiting for the Omihomo core to answer.",
      steps: [], actions: [] }
  }
  // The key of the setup action handed to the terminal, until the link
  // changes, so a reopened panel says it is under way. A cancelled run leaves
  // the link, and so this, as it was.
  property string setupStarted: ""
  readonly property bool coreAlert: omihomo.pendingDownloads.length > 0 || omihomo.link === "outdated" || omihomo.link === "newer"
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

  readonly property var ruleTargets: {
    var targets = ruleType === "SUB-RULE"
      ? omihomo.subRules.map(function(name) { return { value: name, label: name, description: "sub-rule" } }) : Model.ruleTargets(visibleGroups)
    if (editingRule > 0 && savedRuleTarget !== "" && !targets.some(function(target) { return target.value === savedRuleTarget }))
      targets.push({ value: savedRuleTarget, label: savedRuleTarget, description: "unavailable in this subscription" })
    return targets
  }
  readonly property string ruleType: Model.RULE_TYPES[Math.max(0, Math.min(ruleTypeIndex, Model.RULE_TYPES.length - 1))]
  readonly property string ruleTarget: ruleTargets.length > 0 ? ruleTargets[Math.max(0, Math.min(ruleTargetIndex, ruleTargets.length - 1))].value : ""
  readonly property string ruleFormError: Model.validateRule(ruleType, ruleFormValue, ruleTarget)

  readonly property var effectiveRules: omihomo.effectiveRules
  readonly property var ruleTest: Model.testRules(effectiveRules, ruleQuery, omihomo.routing)
  readonly property string ruleHitTarget: ruleTest.hit >= 0 ? effectiveRules[ruleTest.hit].target : ""
  readonly property var ruleTally: Model.ruleTargetTally(effectiveRules)
  readonly property var rulePresentation: Model.rulePresentation(omihomo.ruleState, expandedCategories, omihomo.activeSubscription)
  readonly property var ruleCategories: rulePresentation.categories
  readonly property var ruleEntries: rulePresentation.rows

  function categorySummary(category) {
    var targets = []
    category.rules.forEach(function(r) { if (r.type !== "SUB-RULE" && targets.indexOf(r.target) === -1) targets.push(r.target) })
    return targets.length === 1 ? targets[0] : targets.length > 1 ? "Mixed targets" : "Set target"
  }

  function expandCategory(id) {
    var expanded = Object.assign({}, expandedCategories)
    expanded[id] = !expanded[id]
    expandedCategories = expanded
  }

  readonly property bool allCategoriesExpanded: ruleCategories.length > 0
    && ruleCategories.every(function(c) { return expandedCategories[c.id] === true })

  function toggleAllCategories() {
    var expanded = {}
    if (!allCategoriesExpanded) ruleCategories.forEach(function(c) { expanded[c.id] = true })
    expandedCategories = expanded
  }

  function toggleCategory(category) {
    if (editingRule > 0 || categoryEditing !== "" || omihomo.ruleSaving) return
    omihomo.categoryAction(category.disabled ? "enable" : "disable", category.id)
  }

  function moveCategory(category, delta) {
    var to = category.top + delta
    if (!category.own || editingRule > 0 || to < 1 || to > omihomo.ruleDocument.length) return
    omihomo.moveRuleEntry(category.top, to, function() {
      root.focusRow("category", root.ruleCategories.findIndex(function(c) { return c.id === category.id }))
      root.scrollCursorIntoView()
    })
  }

  function editCategory(id) {
    cancelRuleEdit()
    categoryEditing = id
    var category = ruleCategories.find(function(c) { return c.id === id })
    rulesView.categoryField.text = category ? category.name : ""
    beginEdit(rulesView.categoryField)
  }

  function saveCategory() {
    var name = rulesView.categoryField.text.trim()
    if (name === "" || omihomo.ruleSaving) return
    omihomo.categoryAction("rename", categoryEditing, name, function() {
      root.categoryEditing = ""
      rulesView.categoryField.text = ""
      root.endEdit()
    })
  }

  function retargetCategory(category) {
    if (!category.own || editingRule > 0 || categoryEditing !== "") return
    categoryTarget = category.id
    rulesView.bulkSelector.openPicker()
  }

  readonly property var flows: Model.connectionFlows(omihomo.connectionStacks, 6, 4)
  readonly property double largestStack: {
    var most = 0
    for (let i = 0; i < omihomo.connectionStacks.length; i++) {
      const stack = omihomo.connectionStacks[i]
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
      for (i = 0; i < setup.actions.length; i++) rows.push({ s: "install", i: i })
      return rows
    }
    if (activeSelector) {
      for (i = 0; i < activeSelector.filtered.length; i++) rows.push({ s: "choice", i: i })
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
      rows.push({ s: "ruleTest" }, { s: "toggleCategories" }, { s: "newRule" })
      for (i = 0; i < ruleEntries.length; i++) if (ruleEntries[i].s) rows.push({ s: ruleEntries[i].s, i: ruleEntries[i].i })
      if (ruleFormOpen) rows.push({ s: "ruleValue" }, { s: "ruleType" }, { s: "ruleCategory" }, { s: "ruleTarget" }, { s: "ruleEnabled" })
      break
    case "conns":
      for (i = 0; i < omihomo.connectionStacks.length; i++) rows.push({ s: "conn", i: i })
      break
    case "settings":
      rows.push({ s: "mode" }, { s: "dns" })
      rows.push({ s: "tun" }, { s: "tunRedirect" })
      if (omihomo.tailscalePresent) rows.push({ s: "tailscale" }, { s: "tailnetProxy" })
      rows.push({ s: "checkUpdates" })
      if (updater.updateAvailable) rows.push({ s: "update" })
      if (updater.releaseUrl !== "") rows.push({ s: "releaseNotes" })
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
      const sub = cursorRow && cursorRow.s === "sub" ? subscriptionAt(cursorRow.i) : null
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
    for (let i = 0; i < navRows.length; i++) {
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
    case "install": runSetup(setup.actions[cursorRow.i]); break
    case "group": browseGroupAt(cursorRow.i); break
    case "config": chooseConfigAt(cursorRow.i); break
    case "sub": activateSubscriptionAt(cursorRow.i); break
    case "subUrl": beginEdit(subscriptionsView.urlField); break
    case "subSubmit": submitSubForm(); break
    case "toggleCategories": toggleAllCategories(); break
    case "newRule": newRule(); break
    case "category": expandCategory(ruleCategories[cursorRow.i].id); break
    case "ruleCategory": rulesView.categorySelector.openPicker(); break
    case "ruleEnabled": ruleEnabled = !ruleEnabled; openRuleForm(); break
    case "ruleTest": beginEdit(rulesView.testField); break
    case "ruleType": rulesView.typeSelector.openPicker(); break
    case "rule": editRule(omihomo.rules[cursorRow.i]); break
    case "ruleValue": beginEdit(rulesView.valueField); break
    case "ruleTarget": rulesView.selector.openPicker(); break
    case "choice": if (activeSelector) activeSelector.pick(cursorRow.i); break
    case "conn": activateConnectionAt(cursorRow.i); break
    case "mode": cycleMode(1); break
    case "dns": settingsView.selector.openPicker(); break
    case "tun": omihomo.toggleTun(); break
    case "tunRedirect": omihomo.toggleTunRedirect(); break
    case "tailscale": omihomo.toggleTailscale(); break
    case "tailnetProxy": omihomo.toggleTailnetProxy(); break
    case "checkUpdates": checkUpdates(); break
    case "update": updateOmihomo(); break
    case "releaseNotes": openReleaseNotes(); break
    case "uninstall": uninstallCore(); break
    }
  }

  function deleteCursorRow() {
    if (!cursorRow) return
    if (cursorRow.s === "sub") {
      const sub = subscriptionAt(cursorRow.i)
      if (sub) removeSubscription(sub.name)
    } else if (cursorRow.s === "rule") {
      const rule = omihomo.rules[cursorRow.i]
      if (rule) omihomo.removeRule(rule.index)
    } else if (cursorRow.s === "conn") {
      activateConnectionAt(cursorRow.i)
    }
  }

  function handleTextKey(key) {
    if (!omihomo.installed) {
      const action = setup.actions.find(a => a.key === key.toLowerCase())
      if (action) runSetup(action)
      return
    }
    if (key === "/" && activeSelector) { beginEdit(activeSelector.field); return }
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
    case "n": newRule(); return
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
      if (key === "/" && activeSelector) beginEdit(activeSelector.field)
      else if (key === "/") { focusRow("ruleTest"); beginEdit(rulesView.testField) }
    } else if (view === "conns") {
      if (key === "A") omihomo.closeAllConnections()
      else if (key === "L") omihomo.clearConnectionLog()
    } else if (view === "settings") {
      if (key === "A") omihomo.toggleTunRedirect()
      else if (key === "T") omihomo.toggleTailscale()
      else if (key === "P") omihomo.toggleTailnetProxy()
      else if (key === "u") checkUpdates()
      else if (key === "U") updateOmihomo()
    }
  }

  function handleEscape() {
    if (activeSelector) { activeSelector.cancel(); return }
    if (categoryEditing !== "") { categoryEditing = ""; rulesView.categoryField.text = ""; endEdit(); return }
    if (ruleFormOpen) {
      cancelRuleEdit()
      return
    }
    if (view === "subs" && editing) subscriptionsView.urlField.text = ""
    if (view === "rules" && editing === rulesView.testField) rulesView.testField.text = ""
    if (filterOpen) { closeFilter(); return }
    if (editing) { endEdit(); return }
    if (removeArmed !== "") { removeArmed = ""; return }
    if (uninstallArmed) { uninstallArmed = false; return }
    if (view !== "configs") { openView("configs"); return }
    close()
  }

  function acceptEdit() {
    if (activeSelector) {
      activeSelector.pick(cursorRow && cursorRow.s === "choice" ? cursorRow.i : 0)
    } else if (view === "rules" && categoryEditing !== "") { saveCategory()
    } else if (view === "rules" && ruleFormOpen) {
      submitRuleForm()
    } else if (editing) editing.accepted()
    else { cursorActive = true; activateCursor() }
  }

  function reorderCursor(dy) {
    if (!cursorActive || !cursorRow) return
    if (cursorRow.s === "rule") moveRule(cursorRow.i + 1, dy)
    if (cursorRow.s === "category") moveCategory(ruleCategories[cursorRow.i], dy)
  }

  function runHint(key) {
    if (key === "enter") acceptEdit()
    else if (key === "x") { cursorActive = true; deleteCursorRow() }
    else if (key === "esc") handleEscape()
    else if (key === "j") moveCursor(0, 1)
    else if (key === "reorder" && cursorRow && cursorRow.s === "rule") moveRule(cursorRow.i + 1, -1)
    else handleTextKey(key)
  }

  function hintsFor(name) {
    if (!omihomo.installed) return setup.actions.map(a => ({ keys: a.key, label: a.button.split(" ")[0].toLowerCase(), key: a.key }))
      .concat([{ keys: "esc", label: "close", key: "esc" }])
    if (activeSelector) return [
      { keys: "↵", label: "pick", key: "enter" }, { keys: "/", label: "filter", key: "/" },
      { keys: "j k", label: "move", key: "j" }, { keys: "esc", label: "cancel", key: "esc" }]
    if (editing !== null || ruleFormOpen || categoryEditing !== "") return [
      { keys: "↵", label: "save", key: "enter" }, { keys: "esc", label: "cancel", key: "esc" }]
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
      { keys: "n", label: "new rule", key: "n" },
      { keys: "↵", label: "edit", key: "enter" }, { keys: "ctrl ↑↓", label: "reorder", key: "reorder" },
      { keys: "/", label: "test", key: "/" }, { keys: "esc", label: "back", key: "esc" }]
    case "conns": return [
      { keys: "↵", label: "close", key: "enter" }, { keys: "A", label: "close all", key: "A" },
      { keys: "L", label: "clear log", key: "L" }, { keys: "esc", label: "back", key: "esc" }]
    case "settings": return [
      { keys: "↵", label: "activate", key: "enter" }, { keys: "u", label: "check", key: "u" },
      { keys: "t", label: "TUN", key: "t" }, { keys: "A", label: "accel", key: "A" },
      { keys: "M", label: "back", key: "M" }]
    }
    return []
  }

  function switchPanel(direction) {
    var views = tabViews.concat(["settings"])
    openView(views[(views.indexOf(view) + direction + views.length) % views.length])
  }

  function openView(name) {
    if (activeSelector) activeSelector.cancel()
    cancelRuleEdit()
    subscriptionsView.urlField.text = ""
    rulesView.testField.text = ""
    editing = null
    endEdit()
    categoryEditing = ""
    view = name
    cursor = 0
    cursorActive = false
    removeArmed = ""
    uninstallArmed = false
    if (filterOpen) closeFilter()
    if (name === "configs" && browsedGroupEntry) {
      const at = visibleConfigs.indexOf(browsedGroupEntry.now)
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
    for (let i = 0; i < visibleGroups.length; i++) if (visibleGroups[i].name === browsedGroup) index = i
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
    if (ruleTargets.length === 0) return
    ruleTargetIndex = (ruleTargetIndex + delta + ruleTargets.length) % ruleTargets.length
  }

  function chooseConfigAt(index) {
    var name = visibleConfigs[index]
    if (name && browsedGroupEntry && browsedGroupEntry.selectable) omihomo.selectConfig(browsedGroup, name)
  }

  function testSelectedConfig() {
    if (cursorRow && cursorRow.s === "config") omihomo.testConfig(visibleConfigs[cursorRow.i])
    else if (omihomo.currentConfig !== "") omihomo.testConfig(omihomo.currentConfig)
  }

  function runSetup(action) {
    if (!bar) return
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote(action.command))
    setupStarted = action.key
    close()
  }

  function checkUpdates() {
    updater.check()
  }

  function updateOmihomo() {
    if (!bar || !updater.updateAvailable) return
    bar.run("omarchy-launch-floating-terminal-with-presentation "
      + Util.shellQuote(Util.shellQuote(installerPath) + " update"))
    close()
  }

  function openReleaseNotes() {
    if (/^https:\/\/github\.com\/readeem\/omihomo\/releases\/tag\/v[0-9]+\.[0-9]+\.[0-9]+$/.test(updater.releaseUrl))
      Qt.openUrlExternally(updater.releaseUrl)
  }

  function uninstallCore() {
    if (!bar) return
    if (!uninstallArmed) {
      focusRow("uninstall")
      uninstallArmed = true
      return
    }
    bar.run("omarchy-launch-floating-terminal-with-presentation " + Util.shellQuote("sudo pacman -R omihomo"))
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

  function ruleMoveTarget(index, delta) {
    var rule = omihomo.rules[index - 1]
    if (!rule) return 0
    if (!rule.category) {
      var entry = ruleEntries.find(function(e) { return e.kind === "own" && e.rule.index === index })
      return entry && entry.top + delta > 0 && entry.top + delta <= omihomo.ruleDocument.length ? entry.top + delta : 0
    }
    var siblings = omihomo.rules.filter(function(r) { return r.category === rule.category })
    var at = siblings.findIndex(function(r) { return r.index === index })
    return siblings[at + delta] ? siblings[at + delta].index : 0
  }

  function moveRule(index, delta) {
    if (editingRule > 0 || categoryEditing !== "" || omihomo.ruleSaving) return
    var to = ruleMoveTarget(index, delta)
    if (to <= 0) return
    var rule = omihomo.rules[index - 1]
    if (rule.category) omihomo.moveRule(index, to)
    else {
      var entry = ruleEntries.find(function(e) { return e.kind === "own" && e.rule.index === index })
      omihomo.moveRuleEntry(entry.top, to, function() {
        var moved = root.ruleEntries.find(function(e) { return e.kind === "own" && e.top === to })
        if (moved) root.focusRow("rule", moved.i)
        root.scrollCursorIntoView()
      })
    }
  }

  function editRule(rule) {
    if (!rule || omihomo.ruleSaving) return
    editingRule = rule.index
    editingRuleOriginal = JSON.stringify(rule)
    savedRuleTarget = rule.target
    ruleTypeIndex = Model.RULE_TYPES.indexOf(rule.type)
    rulesView.valueField.text = rule.value
    ruleParams = rule.params || ""
    ruleCategory = rule.category || ""
    newRuleCategory = ""
    ruleEnabled = !rule.disabled
    ruleTargetIndex = Math.max(0, ruleTargets.findIndex(function(target) { return target.value === rule.target }))
    openRuleForm()
  }

  function cancelRuleEdit() {
    if (activeSelector && view === "rules") activeSelector.cancel()
    ruleFormOpen = false
    editingRule = 0
    ruleCategory = ""
    newRuleCategory = ""
    ruleEnabled = true
    savedRuleTarget = ""
    ruleParams = ""
    ruleTypeIndex = 0
    ruleTargetIndex = 0
    rulesView.valueField.text = ""
    endEdit()
  }

  function openRuleForm() {
    if (view !== "rules") openView("rules")
    categoryEditing = ""
    ruleFormOpen = true
    omihomo.clearMessages()
    Qt.callLater(function() {
      if (!root.ruleFormOpen) return
      root.focusRow("ruleValue")
      root.beginEdit(rulesView.valueField)
    })
  }

  function newRule() {
    if (omihomo.ruleSaving) return
    if (ruleFormOpen) { cancelRuleEdit(); return }
    cancelRuleEdit()
    openRuleForm()
  }

  function submitRuleForm() {
    if (!ruleFormOpen || omihomo.ruleSaving) return
    if (ruleFormError !== "") {
      focusRow("ruleValue")
      beginEdit(rulesView.valueField)
      return
    }
    omihomo.addRule(ruleType, ruleFormValue.trim(), ruleTarget, ruleParams, editingRule, ruleCategory === "new" ? "" : ruleCategory, ruleEnabled, ruleCategory === "new" ? newRuleCategory : "")
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
    case "category":
    case "readonlyRule":
      list = rulesView.ruleList
      index = ruleEntries.findIndex(function(entry) { return entry.s === cursorRow.s && entry.i === cursorRow.i })
      break
    case "conn": list = connectionsView.list; break
    case "choice": if (activeSelector) list = activeSelector.list; break
    }
    if (list) Qt.callLater(function() { list.positionViewAtIndex(index, ListView.Contain) })
    else if (view === "settings") settingsView.reveal(cursorRow.s, cursorRow.i)
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onOpenedChanged: {
    if (opened) {
      openView("configs")
      rulesView.valueField.text = ""
      editing = null
      omihomo.clearMessages()
      Qt.callLater(function() {
        if (omihomo.pendingDownloads.length > 0) directPrompt.forceActiveFocus()
        else keyCatcher.forceActiveFocus()
      })
    }
  }

  onNavRowsChanged: clampCursor()

  Connections {
    target: omihomo
    function onPendingDownloadsChanged() {
      if (omihomo.pendingDownloads.length > 0) root.open()
      Qt.callLater(function() {
        if (omihomo.pendingDownloads.length > 0) directPrompt.forceActiveFocus()
        else keyCatcher.forceActiveFocus()
      })
    }
    function onErrorScopeChanged() {
      var scope = omihomo.errorScope
      if (root.view !== "subs" || scope.indexOf("sub:") !== 0 || scope === "sub:") return
      for (let i = 0; i < omihomo.subscriptions.length; i++) {
        if ("sub:" + omihomo.subscriptions[i].name !== scope) continue
        const index = i
        Qt.callLater(function() { subscriptionsView.list.positionViewAtIndex(index, ListView.Contain) })
        return
      }
    }
  }

  UpdateService { id: updateService }

  Connections {
    target: omihomo
    function onRulesChanged() {
      if (root.editingRule > 0 && JSON.stringify(omihomo.rules[root.editingRule - 1]) !== root.editingRuleOriginal)
        root.cancelRuleEdit()
    }
    function onLinkChanged() { root.setupStarted = "" }
    function onDaemonVersionChanged() {
      if (omihomo.daemonVersion !== "") updateService.check()
    }
  }

  Service {
    id: omihomo
    panelOpen: root.opened
    onConfigChanged: omihomo.refreshTrace()
    onRuleMoved: function(index) {
      root.focusRow("rule", index - 1)
      root.scrollCursorIntoView()
    }
    onRuleAdded: root.cancelRuleEdit()
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
    focusTarget: omihomo.pendingDownloads.length > 0 ? directPrompt : keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(420))
    // Setup is one card, so the panel fits it instead of the views' height.
    contentHeight: panel.cappedContentHeight(omihomo.installed ? Style.space(720) : body.implicitHeight)
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
      onActivateRequested: {
        if (root.cursorRow && ["ruleType", "ruleCategory", "ruleTarget", "ruleEnabled"].indexOf(root.cursorRow.s) !== -1)
          root.activateCursor()
        else root.acceptEdit()
      }
      onDeleteRequested: if (root.cursorActive) root.deleteCursorRow()
      onCloseRequested: root.handleEscape()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) { root.handleTextKey(t) }

      // Stable Omarchy's key catcher cannot reorder, so the panel does.
      Shortcut {
        sequences: ["Ctrl+Up", "Ctrl+K"]
        enabled: root.view === "rules" && root.editing === null
        onActivated: root.reorderCursor(-1)
      }
      Shortcut {
        sequences: ["Ctrl+Down", "Ctrl+J"]
        enabled: root.view === "rules" && root.editing === null
        onActivated: root.reorderCursor(1)
      }

      ColumnLayout {
        id: body
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

          SectionLine {
            theme: root.theme
            Layout.fillWidth: true
            title: root.setup.section
          }

          Rectangle {
            Layout.fillWidth: true
            implicitHeight: setupColumn.implicitHeight + Style.space(6)
            radius: root.theme.cardRadius
            color: root.theme.fill
            border.width: 1
            border.color: root.theme.line

            ColumnLayout {
              id: setupColumn
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              anchors.margins: Style.space(3)
              spacing: 0

              RowLayout {
                Layout.fillWidth: true
                Layout.margins: Style.space(8)
                spacing: Style.space(12)

                OmihomoIcon {
                  Layout.alignment: Qt.AlignTop
                  Layout.topMargin: Style.space(2)
                  iconSize: Style.space(20)
                  color: root.theme.dim
                  alertColor: root.coreAlert ? root.theme.urgent : "transparent"
                }

                ColumnLayout {
                  Layout.fillWidth: true
                  spacing: Style.space(4)

                  PanelText {
                    theme: root.theme
                    Layout.fillWidth: true
                    text: root.setup.title
                    color: root.coreAlert ? root.theme.urgent : root.theme.foreground
                    font.pixelSize: Style.font.body
                    wrapMode: Text.WordWrap
                  }

                  PanelText {
                    theme: root.theme
                    Layout.fillWidth: true
                    text: root.setup.text
                    color: root.theme.dim
                    wrapMode: Text.WordWrap
                  }

                  Repeater {
                    model: root.setup.steps

                    RowLayout {
                      required property string modelData
                      required property int index
                      Layout.fillWidth: true
                      Layout.topMargin: index === 0 ? Style.space(2) : 0
                      spacing: Style.space(8)

                      PanelText {
                        theme: root.theme
                        Layout.alignment: Qt.AlignTop
                        text: String(index + 1)
                        color: root.theme.accent
                      }

                      PanelText {
                        theme: root.theme
                        Layout.fillWidth: true
                        text: modelData
                        color: root.theme.dim
                        wrapMode: Text.WordWrap
                      }
                    }
                  }
                }
              }

              Rectangle {
                visible: root.setup.actions.length > 0
                Layout.fillWidth: true
                Layout.leftMargin: Style.space(5)
                Layout.rightMargin: Style.space(5)
                implicitHeight: 1
                color: root.theme.line
              }

              Repeater {
                model: root.setup.actions

                SettingRow {
                  required property var modelData
                  required property int index
                  Layout.fillWidth: true
                  Layout.topMargin: index === 0 ? Style.space(3) : 0
                  panel: root
                  section: "install"
                  rowIndex: index
                  title: modelData.button + (root.updater.availableVersion !== ""
                    && (omihomo.link === "absent" || omihomo.link === "outdated" && root.updater.updateAvailable)
                    ? " " + root.updater.availableVersion : "")
                  keyHint: modelData.key
                  subtitle: root.setupStarted === modelData.key
                    ? "started in a terminal; " + modelData.key + " runs it again" : modelData.hint
                  tag: modelData.tag
                  tagTint: root.theme.accent
                  tagFilled: index === 0
                  onActivated: root.runSetup(modelData)
                }
              }
            }
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
          visible: root.hintsFor(root.view).length > 0
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
    DirectDownloadPrompt {
      id: directPrompt
      anchors.fill: parent
      theme: root.theme
      download: omihomo.pendingDownloads.length > 0 ? omihomo.pendingDownloads[0] : null
      onDecided: function(allow) { omihomo.decideDownload(download.id, allow) }
    }
  }
}
