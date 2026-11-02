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
      { then: function() { testRoot.type("n") } },
      { name: "new rule focused", until: function() { return panel.editing !== null }, then: function() {
          t.check(panel.hintsFor("rules")[0].label, "save", "the footer offers save while editing")
          t.check(panel.hintsFor("rules")[1].label, "cancel", "the footer offers cancel while editing")
          testRoot.type("discard.example")
          testRoot.key(Qt.Key_Escape)
        } },
      { then: function() {
          t.check(panel.ruleFormValue, "", "Escape discards the draft")
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
      { then: function() { testRoot.key(Qt.Key_Down); testRoot.key(Qt.Key_Down); testRoot.key(Qt.Key_Return) } },
      { name: "owner rule focused", until: function() { return panel.editingRule === 1 && panel.editing !== null }, then: function() {
          t.check(panel.ruleFormValue, "github.com", "editing starts from the saved value")
          testRoot.key(Qt.Key_A, Qt.ControlModifier)
          testRoot.type("saved.example")
          testRoot.key(Qt.Key_Return)
        } },
      { name: "saved rule", until: function() { return !panel.service.ruleSaving && panel.service.rules[0].value === "saved.example" }, then: function() {
          t.check(panel.editingRule, 0, "Enter finishes editing")
          t.check(panel.service.rules.length, 4, "saving replaces the rule")
          testRoot.type("M")
        } },
      { then: function() { testRoot.key(Qt.Key_Down); testRoot.key(Qt.Key_Down); testRoot.key(Qt.Key_Return) } },
      { name: "DNS picker", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.value, "", "DNS starts with the confirmed default")
          t.check(panel.activeSelector.choices.some(function(c) { return c.value === "REJECT" }), false,
            "DNS excludes rule-only destinations")
          t.check(panel.hintsFor("settings")[0].label, "pick", "the footer offers selection in the picker")
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
      { then: function() {
          testRoot.key(Qt.Key_Down)
          for (var i = 0; i < panel.navRows.length; i++) testRoot.key(Qt.Key_Down)
          testRoot.key(Qt.Key_Return)
        } },
      { name: "rule picker", until: function() { return panel.activeSelector !== null && panel.editing !== null }, then: function() {
          t.check(panel.activeSelector.choices.some(function(c) { return c.value === "REJECT" }), true,
            "rules include their built-in destinations")
          testRoot.type("Streaming")
          testRoot.key(Qt.Key_Return)
        } },
      { until: function() { return panel.activeSelector === null && panel.editing !== null }, then: function() {
          t.check(panel.ruleTarget, "Streaming", "filtered selection changes the rule target")
          testRoot.key(Qt.Key_Backtab)
        } },
      { then: function() {
          t.check(panel.view, "subs", "Shift+Tab switches back while editing")
          t.check(panel.ruleFormValue, "", "switching back also discards the draft")
        } }
    ]
  }
}
