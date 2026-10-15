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
    tailscale: tailscaleRow, tailnetProxy: tailnetProxyRow, uninstall: maintenanceRow
  })

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
      title: "MAINTENANCE"
    }

    Rectangle {
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
          id: maintenanceRow
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
          text: "Removes the omihomo-bin package and stops the service. Subscriptions and settings stay in /var/lib/omihomo."
          color: view.theme.dim
          wrapMode: Text.WordWrap
        }
      }
    }
  }
}
