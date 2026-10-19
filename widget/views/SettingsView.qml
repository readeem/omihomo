import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import qs.Commons
import "../components"
import "../Model.js" as Model

Flickable {
  id: view

  required property var panel
  readonly property var service: panel.service
  readonly property Theme theme: panel.theme
  readonly property var rows: ({
    mode: modeRow, tun: tunRow, tunRedirect: tunRedirectRow,
    tailscale: tailscaleRow, tailnetProxy: tailnetProxyRow, uninstall: maintenanceCard,
    checkUpdates: checkRow, update: updateRow, releaseNotes: releaseNotesRow
  })

  readonly property var updater: panel.updater
  readonly property bool checked: updater.availableVersion !== ""

  function reveal(section) {
    var item = rows[section]
    if (!item) return
    Qt.callLater(function() {
      var top = item.mapToItem(view.contentItem, 0, 0).y
      var bottom = top + item.height
      var maxY = Math.max(0, view.contentHeight - view.height)
      if (top < view.contentY) view.contentY = Math.max(0, top)
      else if (bottom > view.contentY + view.height)
        view.contentY = Math.min(maxY, bottom - view.height)
    })
  }

  contentWidth: width
  contentHeight: settingsColumn.implicitHeight
  // Arming uninstall grows the last card with its warning; keep it in view.
  onContentHeightChanged: if (panel.uninstallArmed) contentY = Math.max(0, contentHeight - height)
  clip: true
  boundsBehavior: Flickable.StopAtBounds
  interactive: contentHeight > height
  ScrollBar.vertical: ScrollBar { policy: view.interactive ? ScrollBar.AlwaysOn : ScrollBar.AsNeeded }

  ColumnLayout {
    id: settingsColumn
    width: view.width - (view.interactive ? Style.space(10) : 0)
    spacing: Style.space(6)

    PanelText {
      theme: view.theme
      visible: view.panel.errorAt("settings")
      Layout.fillWidth: true
      text: "▲ " + view.service.lastError
      color: view.theme.urgent
      wrapMode: Text.WordWrap
    }

    SectionLine {
      theme: view.theme
      Layout.fillWidth: true
      title: "ROUTING"
      note: "applies immediately"
    }

    Rectangle {
      Layout.fillWidth: true
      implicitHeight: routingColumn.implicitHeight + Style.space(6)
      radius: view.theme.cardRadius
      color: view.theme.fill
      border.width: 1
      border.color: view.theme.line
      ColumnLayout {
        id: routingColumn
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        anchors.margins: Style.space(3)
        spacing: 0
        SettingRow {
          id: modeRow
          panel: view.panel
          Layout.fillWidth: true
          section: "mode"
          title: "Mode"
          keyHint: "m"
          subtitle: "global sends everything to " + (view.service.primaryGroup || "the proxy")
          modeControl: true
        }

        SettingRow {
          id: tunRow
          panel: view.panel
          Layout.fillWidth: true
          section: "tun"
          title: "TUN"
          keyHint: "t"
          subtitle: "capture every app, not just the proxy port"
          checked: view.service.tunActive
          onActivated: view.service.toggleTun()
        }
        SettingRow {
          id: tunRedirectRow
          panel: view.panel
          Layout.fillWidth: true
          section: "tunRedirect"
          title: "TUN acceleration"
          keyHint: "A"
          subtitle: view.panel.tunRedirectBlocked ? "blocking TUN on this machine: turn it off" : "faster; turn off if TUN fails to start"
          urgentSubtitle: view.panel.tunRedirectBlocked
          checked: view.service.tunRedirectActive
          onActivated: view.service.toggleTunRedirect()
        }
      }
    }

    SectionLine {
      theme: view.theme
      visible: view.service.tailscalePresent
      Layout.fillWidth: true
      Layout.topMargin: Style.space(8)
      title: "TAILSCALE"
    }

    Rectangle {
      visible: view.service.tailscalePresent
      Layout.fillWidth: true
      implicitHeight: systemColumn.implicitHeight + Style.space(6)
      radius: view.theme.cardRadius
      color: view.theme.fill
      border.width: 1
      border.color: view.theme.line
      ColumnLayout {
        id: systemColumn
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        anchors.margins: Style.space(3)
        spacing: 0
        SettingRow {
          id: tailscaleRow
          panel: view.panel
          Layout.fillWidth: true
          visible: view.service.tailscalePresent
          section: "tailscale"
          title: "Tailscale integration"
          keyHint: "T"
          subtitle: "keep the tailnet reachable under TUN"
          checked: view.service.tailscaleActive
          onActivated: view.service.toggleTailscale()
        }

        SettingRow {
          id: tailnetProxyRow
          panel: view.panel
          Layout.fillWidth: true
          visible: view.service.tailscalePresent
          section: "tailnetProxy"
          title: "Route tailnet through proxy"
          keyHint: "P"
          subtitle: "the proxy server must be on the tailnet"
          checked: view.service.tailnetProxyActive
          opacity: view.service.tailscaleActive ? 1 : 0.45
          onActivated: view.service.toggleTailnetProxy()
        }
      }
    }

    SectionLine {
      theme: view.theme
      Layout.fillWidth: true
      Layout.topMargin: Style.space(8)
      title: "UPDATES"
      note: "checked every 12 hours"
    }

    Rectangle {
      Layout.fillWidth: true
      implicitHeight: updatesColumn.implicitHeight + Style.space(6)
      radius: view.theme.cardRadius
      color: view.theme.fill
      border.width: 1
      border.color: view.theme.line
      ColumnLayout {
        id: updatesColumn
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        anchors.margins: Style.space(3)
        spacing: 0
        SettingRow {
          id: checkRow
          panel: view.panel
          Layout.fillWidth: true
          section: "checkUpdates"
          title: "Check for updates"
          keyHint: "u"
          subtitle: view.updater.error !== "" ? "▲ " + view.updater.error
            : (view.checked ? (view.updater.installedVersion !== "" ? "core " + view.updater.installedVersion : "core not installed")
              + (view.updater.pluginVersion !== view.updater.availableVersion.split("-")[0]
                ? " · widget " + view.updater.pluginVersion : "") + " · latest " + view.updater.availableVersion
              : (view.updater.checking ? "asking GitHub for the latest release" : "not checked yet"))
          urgentSubtitle: view.updater.error !== ""
          tag: view.updater.checking ? "checking…"
            : (view.checked && !view.updater.updateAvailable && view.updater.installedVersion !== ""
              && view.updater.error === "" ? "up to date" : "check")
          onActivated: view.panel.checkUpdates()
        }

        SettingRow {
          id: updateRow
          panel: view.panel
          Layout.fillWidth: true
          visible: view.updater.updateAvailable
          section: "update"
          title: "Update to " + view.updater.availableVersion
          keyHint: "U"
          subtitle: "widget and core, in a terminal"
          tag: "update…"
          tagTint: view.theme.accent
          tagFilled: true
          onActivated: view.panel.updateOmihomo()
        }

        SettingRow {
          id: releaseNotesRow
          panel: view.panel
          Layout.fillWidth: true
          visible: view.updater.releaseUrl !== ""
          section: "releaseNotes"
          title: "Release notes"
          subtitle: "what changed in " + view.updater.availableVersion.split("-")[0] + ", on GitHub"
          tag: "↗"
          onActivated: view.panel.openReleaseNotes()
        }
      }
    }

    SectionLine {
      theme: view.theme
      Layout.fillWidth: true
      Layout.topMargin: Style.space(8)
      title: "MAINTENANCE"
    }

    Rectangle {
      id: maintenanceCard
      Layout.fillWidth: true
      implicitHeight: maintenanceColumn.implicitHeight + Style.space(22)
      radius: view.theme.cardRadius
      color: view.theme.fill
      border.width: 1
      border.color: view.theme.line
      ColumnLayout {
        id: maintenanceColumn
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        anchors.rightMargin: Style.space(8)
        spacing: Style.space(6)
        PanelText {
          theme: view.theme
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(8)
          text: "core " + Model.stateLabel(view.service.coreState).toLowerCase()
            + (view.service.daemonVersion !== "" ? " · " + view.service.daemonVersion : "")
            + " · starts at boot"
          color: view.theme.dim
          wrapMode: Text.WordWrap
        }

        RowLayout {
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(8)
          spacing: Style.space(6)

          SmallButton {
            theme: view.theme
            text: view.panel.uninstallArmed ? "confirm uninstall" : "uninstall…"
            danger: true
            hasCursor: view.panel.at("uninstall")
            onHovered: function(on) { if (on) view.panel.focusRow("uninstall") }
            onClicked: view.panel.uninstallCore()
          }

          SmallButton {
            theme: view.theme
            visible: view.panel.uninstallArmed
            text: "cancel"
            onClicked: view.panel.uninstallArmed = false
          }
        }

        PanelText {
          theme: view.theme
          visible: view.panel.uninstallArmed
          Layout.fillWidth: true
          Layout.leftMargin: Style.space(8)
          text: "Removes the omihomo package and stops the service. Subscriptions and settings stay in /var/lib/omihomo."
          color: view.theme.dim
          wrapMode: Text.WordWrap
        }
      }
    }
  }
}
