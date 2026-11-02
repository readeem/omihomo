import QtQuick
import QtTest
import Quickshell
import qs.Ui
import "omihomo/widget" as Omihomo

ShellRoot {
  id: testRoot

  function key(name, modifiers) { events.keyClick(name, modifiers || 0, -1) }
  function type(text) {
    for (var i = 0; i < text.length; i++) events.keyClickChar(text[i], 0, -1)
  }

  property string categoryId: ""

  function clickObject(name) {
    var item = findObject(panel, name)
    if (!item) return false
    events.mouseClick(item, item.width / 2, item.height / 2, Qt.LeftButton, 0, -1)
    return true
  }

  function findObject(item, name) {
    if (item.visible && item.objectName === name) return item
    for (var i = 0; i < item.children.length; i++) {
      var found = findObject(item.children[i], name)
      if (found) return found
    }
    return null
  }

  property real savedScroll: 0
  property string savedConnection: ""

  function settingsScroll(item) {
    if (item.visible && item.contentY !== undefined && item.flickableDirection !== undefined && item.model === undefined)
      return item
    for (var i = 0; i < item.children.length; i++) {
      var scroll = settingsScroll(item.children[i])
      if (scroll) return scroll
    }
    return null
  }

  function clickHint(label) {
    function find(item) {
      if (item.keys !== undefined && item.label === label && item.visible) return item
      for (var i = 0; i < item.children.length; i++) {
        var match = find(item.children[i])
        if (match) return match
      }
      return null
    }
    var hint = find(panel)
    if (!hint) return false
    events.mouseClick(hint, hint.width / 2, hint.height / 2, Qt.LeftButton, 0, -1)
    return true
  }

  PluginBarApi { id: bar; pluginId: "dev.readeem.omihomo" }

  FloatingWindow {
    implicitWidth: 422
    implicitHeight: 722
    Omihomo.Panel {
      id: panel
      width: 420
      height: 720
      bar: bar
      Component.onCompleted: {
        service.cliPath = Quickshell.env("OMIHOMO_TEST_CLI")
        open()
      }
    }
    TestEvent { id: events }
  }

  StateTest {
    id: t
    name: "PANEL"
    steps: [
      { name: "panel ready", until: function() { return panel.service.proxiesLoaded && panel.service.rules.length === 4 }, then: function() {
          testRoot.type("3")
        } },
      { then: function() {
          t.check(testRoot.findObject(panel, "ruleForm"), null, "the rule form starts hidden")
          t.check(panel.navRows.some(function(r) { return r.s === "ruleValue" }), false, "hidden rule fields are excluded from navigation")
          t.check(testRoot.clickObject("newRule"), true, "the plus button opens a new rule")
        } },
      { name: "new rule focused", until: function() { return panel.editing !== null }, then: function() {
          t.check(panel.ruleFormOpen, true, "creating a rule reveals the form")
          t.check(panel.hintsFor("rules")[0].label, "save", "the footer offers save while editing")
          t.check(panel.hintsFor("rules")[1].label, "cancel", "the footer offers cancel while editing")
          testRoot.type("discard.example")
          testRoot.key(Qt.Key_Escape)
        } },
      { then: function() {
          t.check(panel.ruleFormValue, "", "Escape discards the draft")
          t.check(panel.ruleFormOpen, false, "Escape hides the rule form")
          t.check(panel.view, "rules", "Escape stays on the rules tab")
          testRoot.type("n")
        } },
      { until: function() { return panel.editing !== null }, then: function() {
          testRoot.type("tab.example")
          testRoot.key(Qt.Key_Tab)
        } },
      { then: function() {
          t.check(panel.view, "conns", "Tab switches views while typing")
          t.check(panel.ruleFormValue, "", "switching views discards the draft")
          t.check(panel.ruleFormOpen, false, "switching views hides the rule form")
          testRoot.type("3")
        } },
      { then: function() { testRoot.type("n") } },
      { until: function() { return panel.editing !== null }, then: function() {
          testRoot.type("cancel.example")
          t.check(testRoot.clickHint("cancel"), true, "the footer cancel button is clickable")
        } },
      { then: function() {
          t.check(panel.ruleFormValue, "", "the footer cancels the draft")
          testRoot.key(Qt.Key_Tab)
          testRoot.key(Qt.Key_Backtab)
        } },
      { then: function() { panel.focusRow("rule", 0); testRoot.key(Qt.Key_Return) } },
      { name: "owner rule focused", until: function() { return panel.editingRule === 1 && panel.editing !== null }, then: function() {
          t.check(panel.ruleFormOpen, true, "editing reveals the rule form")
          t.check(panel.ruleFormValue, "github.com", "editing starts from the saved value")
          testRoot.key(Qt.Key_A, Qt.ControlModifier)
          testRoot.type("saved.example")
          testRoot.key(Qt.Key_Return)
        } },
      { name: "saved rule", until: function() { return !panel.service.ruleSaving && panel.service.rules[0].value === "saved.example" }, then: function() {
          t.check(panel.editingRule, 0, "Enter finishes editing")
          t.check(panel.ruleFormOpen, false, "saving hides the rule form")
          t.check(panel.service.rules.length, 4, "saving replaces the rule")
          testRoot.type("M")
        } },
      { then: function() { testRoot.key(Qt.Key_Down); testRoot.key(Qt.Key_Down); testRoot.key(Qt.Key_Return) } },
      { name: "DNS picker", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.value, "", "DNS starts with the confirmed default")
          t.check(panel.activeSelector.choices.some(function(c) { return c.value === "REJECT" }), false,
            "DNS excludes rule-only destinations")
          t.check(panel.hintsFor("settings")[0].label, "pick", "the footer offers selection in the picker")
          var scroll = testRoot.settingsScroll(panel)
          t.check(scroll !== null, true, "the settings page has a scrollable body")
          if (scroll) {
            var field = panel.activeSelector.field
            var top = field.mapToItem(panel, 0, 0).y
            var oldScroll = scroll.contentY
            scroll.contentY = Math.min(scroll.contentHeight - scroll.height, oldScroll + 60)
            t.check(scroll.contentY > oldScroll, true, "settings content scrolls while choosing DNS")
            t.check(field.mapToItem(panel, 0, 0).y, top, "the DNS picker stays pinned while settings scroll")
          }
          testRoot.type("Proxy")
          testRoot.key(Qt.Key_Return)
          t.check(panel.service.effectiveDNSDestination, "Proxy", "DNS selection displays immediately")
        } },
      { name: "DNS saved", until: function() { return !panel.service.busy && panel.service.dnsDestination === "Proxy" }, then: function() {
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.label, "Proxy", "reopening shows the saved DNS destination")
          testRoot.type("Streaming")
          testRoot.key(Qt.Key_Escape)
          t.check(panel.service.effectiveDNSDestination, "Proxy", "Escape leaves DNS unchanged")
          testRoot.type("3")
        } },
      { then: function() { testRoot.type("n") } },
      { until: function() { return panel.ruleFormOpen && panel.editing !== null }, then: function() {
          panel.focusRow("ruleTarget")
          panel.activateCursor()
        } },
      { name: "rule picker", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.choices.some(function(c) { return c.value === "REJECT" }), true,
            "rules include their built-in destinations")
          testRoot.type("Streaming")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector === null && panel.editing !== null }, then: function() {
          t.check(panel.ruleTarget, "Streaming", "filtered selection changes the rule target")
          testRoot.key(Qt.Key_Escape)
        } },
      { then: function() { testRoot.type("n") } },
      { until: function() { return panel.ruleFormOpen && panel.editing !== null }, then: function() {
          panel.focusRow("ruleType")
          panel.activateCursor()
        } },
      { name: "rule type picker", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.section, "ruleType", "rule types open through the shared picker")
          t.check(panel.hintsFor("rules")[0].label, "pick", "rule types use the same selection hints")
          testRoot.type("GEOSITE")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector === null && panel.editing !== null }, then: function() {
          t.check(panel.ruleType, "GEOSITE", "filtering and choosing changes the rule type")
          t.check(panel.service.rules.length, 4, "choosing a type does not save the rule")
          testRoot.key(Qt.Key_Backtab)
        } },
      { then: function() {
          t.check(panel.view, "subs", "Shift+Tab switches back while editing")
          t.check(panel.ruleFormValue, "", "switching back also discards the draft")
          testRoot.type("3")
        } },
      { then: function() { testRoot.type("n") } },
      { until: function() { return panel.editing !== null }, then: function() {
          testRoot.type("discard-category.example")
          panel.focusRow("ruleCategory")
          panel.activateCursor()
        } },
      { until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          testRoot.type("Discarded")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector === null && panel.editing !== null }, then: function() {
          t.check(panel.newRuleCategory, "Discarded", "a new category stays in the rule draft")
          t.check(panel.service.ruleDocument.length, 4, "selecting a new category does not persist an empty category")
          testRoot.key(Qt.Key_Escape)
        } },
      { then: function() {
          t.check(panel.newRuleCategory, "", "cancel discards the draft category")
          testRoot.type("n")
        } },
      { until: function() { return panel.editing !== null }, then: function() {
          testRoot.type("categorized.example")
          panel.focusRow("ruleCategory")
          panel.activateCursor()
        } },
      { name: "category selector", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.section, "ruleCategory", "categories use the shared selector")
          testRoot.type("tttttttooooppppAAAA")
          var field = panel.activeSelector.field
          t.check(field.height >= field.contentHeight + field.topPadding + field.bottomPadding, true,
            "category search fits its text: height=" + field.height + ", content=" + field.contentHeight + ", padding=" + (field.topPadding + field.bottomPadding))
          testRoot.key(Qt.Key_A, Qt.ControlModifier)
          testRoot.type("Work")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector === null && panel.editing !== null }, then: function() {
          t.check(panel.newRuleCategory, "Work", "Enter chooses the category creation option")
          t.check(panel.service.ruleDocument.length, 4, "the category is saved together with its first rule")
          t.check(testRoot.clickObject("ruleEnabled"), true, "the individual rule toggle is clickable")
        } },
      { then: function() { testRoot.key(Qt.Key_Return) } },
      { name: "disabled categorized rule saved", until: function() { return !panel.service.ruleSaving && panel.service.rules.length === 5 }, then: function() {
          var rule = panel.service.rules[4]
          testRoot.categoryId = rule.category
          t.check(panel.categoryChoices[1].label, "Work", "saving creates the named category")
          t.check(panel.expandedCategories[testRoot.categoryId] === true, false, "new categories start collapsed")
          t.check(rule.category, testRoot.categoryId, "the saved rule belongs to the category")
          t.check(rule.disabled, true, "the individual toggle is persisted")
          t.check(rule.inactive, true, "a disabled rule cannot match traffic")
          panel.focusRow("toggleCategories")
          testRoot.key(Qt.Key_Return)
        } },
      { then: function() {
          t.check(panel.ruleEntries.some(function(e) { return e.kind === "own" && e.rule.index === 5 }), true, "expand all reveals categorized rules")
          panel.focusRow("rule", 4)
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.editingRule === 5 && panel.editing !== null }, then: function() {
          t.check(panel.ruleEnabled, false, "reopening preserves the individual toggle")
          t.check(testRoot.clickObject("ruleEnabled"), true, "the rule can be enabled again")
        } },
      { then: function() { testRoot.key(Qt.Key_Return) } },
      { until: function() { return !panel.service.ruleSaving && !panel.service.rules[4].inactive }, then: function() {
          var category = panel.ruleCategories.find(function(c) { return c.id === testRoot.categoryId })
          panel.toggleCategory(category)
        } },
      { until: function() { return !panel.service.ruleSaving && panel.service.rules[4].inactive }, then: function() {
          t.check(panel.service.rules[4].disabled, false, "disabling a category preserves the rule's own toggle")
          panel.focusRow("toggleCategories")
          testRoot.key(Qt.Key_Return)
        } },
      { then: function() {
          t.check(panel.ruleEntries.some(function(e) { return e.kind === "own" && e.rule.index === 5 }), false, "collapse all hides category contents")
          var category = panel.ruleCategories.find(function(c) { return c.id === "subscription" })
          t.check(category.own, false, "the subscription category is read-only")
          panel.toggleCategory(category)
        } },
      { until: function() { return !panel.service.ruleSaving && panel.service.ruleState.subscriptionDisabled }, then: function() {
          t.check(panel.service.subscriptionRules.every(function(r) { return r.inactive }), true, "the read-only subscription category can be disabled")
          t.check(testRoot.clickObject("categoryTarget-" + testRoot.categoryId), true, "the category target action is clickable")
        } },
      { name: "bulk destination picker", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.section, "categoryTarget", "bulk targeting uses the shared bottom picker")
          testRoot.type("REJECT")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return !panel.service.ruleSaving && panel.service.rules[4].target === "REJECT" }, then: function() {
          t.check(panel.service.rules[4].disabled, false, "retargeting preserves the rule toggle")
          t.check(testRoot.clickObject("categoryRename-" + testRoot.categoryId), true, "the category rename action is clickable")
        } },
      { until: function() { return panel.categoryEditing === testRoot.categoryId && panel.editing !== null }, then: function() {
          testRoot.key(Qt.Key_A, Qt.ControlModifier)
          testRoot.type("Renamed")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return !panel.service.ruleSaving && panel.categoryEditing === "" && panel.editing === null }, then: function() {
          t.check(panel.categoryChoices[1].label, "Renamed", "Enter saves a category rename")
          t.check(panel.categoryChoices[1].value, testRoot.categoryId, "rename preserves category identity")
          panel.focusRow("category", 0)
          testRoot.key(Qt.Key_Up, Qt.ControlModifier)
        } },
      { until: function() { return !panel.service.ruleSaving && panel.service.ruleDocument[3].category !== undefined }, then: function() {
          t.check(panel.service.ruleDocument[3].category.id, testRoot.categoryId, "Ctrl+Up reorders the category")
          t.check(panel.cursorRow.s, "category", "the cursor follows the moved category")
          panel.expandCategory(testRoot.categoryId)
        } },
      { then: function() { panel.focusRow("rule", 3); testRoot.key(Qt.Key_Return) } },
      { until: function() { return panel.editingRule === 4 && panel.editing !== null }, then: function() {
          panel.focusRow("ruleCategory")
          panel.activateCursor()
        } },
      { until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          testRoot.type("Moved")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector === null && panel.editing !== null }, then: function() {
          t.check(panel.newRuleCategory, "Moved", "editing an existing rule can create a category")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return !panel.service.ruleSaving && panel.editingRule === 0 && panel.service.ruleDocument[4].category !== undefined }, then: function() {
          t.check(panel.service.ruleDocument[4].category.name, "Moved", "editing saves the new category with the existing rule")
          t.check(panel.categoryChoices.some(function(c) { return c.value === testRoot.categoryId }), false, "moving the last rule removes the old category")
          t.check(panel.service.rules.length, 5, "moving between categories preserves the rule")
          var state = panel.service.ruleState
          var rules = state.own.slice(), document = state.document.slice()
          for (var i = 0; i < 20; i++) {
            var rule = Object.assign({}, rules[0], { index: rules.length + 1, value: "scroll-" + i + ".example", category: "" })
            rules.push(rule)
            document.push({ type: rule.type, value: rule.value, target: rule.target })
          }
          panel.service.ruleState = Object.assign({}, state, { own: rules, document: document })
          panel.toggleAllCategories()
        } },
      { then: function() {
          var list = testRoot.findObject(panel, "ruleList")
          t.check(list.contentHeight > list.height + 45, true, "the rules fixture is scrollable")
          list.contentY = 45
          testRoot.savedScroll = list.contentY
          panel.expandCategory(panel.service.ruleDocument[4].category.id)
        } },
      { then: function() {
          var list = testRoot.findObject(panel, "ruleList")
          t.check(Math.round(list.contentY), Math.round(testRoot.savedScroll), "category collapse retains the scrolled viewport")
          panel.openView("conns")
          var connections = panel.service.connectionLog.slice()
          for (var i = 0; i < 15; i++) connections.push(Object.assign({}, connections[0], {
            id: "scroll-" + i, host: "scroll-" + i + ".example", open: false, closedAtMs: Date.now()
          }))
          panel.service.connectionLog = connections
        } },
      { then: function() {
          var list = testRoot.findObject(panel, "connectionList")
          t.check(list.contentHeight > list.height + 40, true, "the connections fixture is scrollable")
          list.contentY = 40
          testRoot.savedScroll = list.contentY
          panel.service.connectionLog = panel.service.connectionLog.map(function(c) { return Object.assign({}, c, { upload: c.upload + 1 }) })
        } },
      { then: function() {
          var list = testRoot.findObject(panel, "connectionList")
          t.check(Math.round(list.contentY), Math.round(testRoot.savedScroll), "connection refresh retains the scrolled viewport")
          var index = list.indexAt(1, list.contentY + 1)
          testRoot.savedConnection = panel.service.connectionStacks[index].key
          testRoot.savedScroll = list.itemAtIndex(index).y - list.contentY
          panel.service.connectionLog = panel.service.connectionLog.concat([Object.assign({}, panel.service.connectionLog[0], {
            id: "new-connection", host: "new.example", startMs: Date.now(), open: true
          })])
        } },
      { then: function() {
          var list = testRoot.findObject(panel, "connectionList")
          var index = panel.service.connectionStacks.findIndex(function(c) { return c.key === testRoot.savedConnection })
          t.check(Math.round(list.itemAtIndex(index).y - list.contentY), Math.round(testRoot.savedScroll), "a new connection preserves the visible row's position")
        } }
    ]
  }
}
