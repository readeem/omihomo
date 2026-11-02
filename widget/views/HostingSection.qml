import QtQuick
import QtQuick.Layouts
import qs.Commons
import "../components"
import "../Model.js" as Model
ColumnLayout {
  id: host

  required property var panel
  required property Item pickerHost
  readonly property var service: panel.service
  readonly property Theme theme: panel.theme
  readonly property var hosting: service.hosting
  readonly property bool online: service.streamsReady(["hosting"])
  readonly property var summary: online ? Model.hostingStatus(hosting, service.coreRunning)
    : { tone: "bad", tag: "offline", text: service.streamErrors.hosting
      ? Model.daemonError(service.streamErrors.hosting) : "The core is not answering." }
  readonly property color toneColor: summary.tone === "ok" ? theme.accent
    : (summary.tone === "bad" ? theme.urgent : (summary.tone === "wait" ? theme.foreground : theme.dim))
  readonly property bool retryable: online && Model.hostingRetryable(hosting, service.coreRunning)

  property bool open: false
  property string form: ""
  property string shareId: ""
  property string armed: ""
  property bool attempted: false
  property string draftProtocol: "tcp"

  readonly property var editedShare: form === "share" && shareId !== ""
    ? hosting.shares.find(function(share) { return share.id === shareId }) || null : null
  readonly property var fieldItems: form === "relay"
    ? [relayLabel, relayHost, relayPort, relayAddress, relayServerName, relayPublicKey, relayShortId]
    : (form === "credential" ? [credentialField]
      : (form === "share" ? [shareLabel, shareAddress, shareLocal, shareExposed] : []))
  readonly property alias protocolSelector: protocolPicker
  readonly property var problem: form === "relay" ? Model.validateRelayForm({
      label: relayLabel.field.text, host: relayHost.field.text, port: relayPort.field.text,
      address: relayAddress.field.text, serverName: relayServerName.field.text,
      publicKey: relayPublicKey.field.text, shortId: relayShortId.field.text })
    : (form === "share" ? Model.validateShareForm({
      label: shareLabel.field.text, localAddress: shareAddress.field.text,
      localPort: shareLocal.field.text, exposedPort: shareExposed.field.text })
    : (form === "credential" ? (Model.validateCredential(credentialField.field.text) === ""
      ? null : { field: 0, message: Model.validateCredential(credentialField.field.text) }) : null))
  readonly property string formMessage: attempted && problem ? problem.message
    : (panel.errorAt("settings") ? service.lastError : "")

  spacing: Style.space(6)

  function fieldAt(index) {
    var item = fieldItems[index]
    return item ? item.field : null
  }

  function itemFor(row, index) {
    switch (row) {
    case "hosting": return headerRow
    case "hostPause": return pauseRow
    case "hostRetry": return retryRow
    case "hostRelay": return relayRow
    case "hostCredential": return credentialRow
    case "hostNewShare": return newShareRow
    case "hostShare": return shareRepeater.itemAt(index)
    case "hostField": return fieldItems[index] || null
    case "hostProtocol": return protocolPicker
    case "hostCopy": return copyRow
    case "hostSubmit": return formButtons
    case "hostRemove": return formButtons
    }
    return null
  }

  function navRows() {
    var rows = [{ s: "hosting" }]
    if (!open) return rows
    if (hosting.relay) rows.push({ s: "hostPause" })
    if (retryable) rows.push({ s: "hostRetry" })
    rows.push({ s: "hostRelay" })
    if (form === "relay") formRows(rows, hosting.relay !== null)
    if (hosting.relay) {
      rows.push({ s: "hostCredential" })
      if (form === "credential") formRows(rows, hosting.credentialPresent)
    }
    for (var i = 0; i < hosting.shares.length; i++) rows.push({ s: "hostShare", i: i })
    rows.push({ s: "hostNewShare" })
    if (form === "share") formRows(rows, editedShare !== null)
    return rows
  }

  function formRows(rows, removable) {
    for (var i = 0; i < fieldItems.length; i++) rows.push({ s: "hostField", i: i })
    if (form === "share") rows.push({ s: "hostProtocol" })
    if (form === "share" && editedShare && hosting.relay) rows.push({ s: "hostCopy" })
    rows.push({ s: "hostSubmit" })
    if (removable) rows.push({ s: "hostRemove" })
  }

  function clearForm() {
    for (var item of [relayLabel, relayHost, relayPort, relayAddress, relayServerName, relayPublicKey,
      relayShortId, credentialField, shareLabel, shareAddress, shareLocal, shareExposed]) item.field.text = ""
    draftProtocol = "tcp"
    attempted = false
  }

  function openForm(name, id) {
    armed = ""
    clearForm()
    form = name
    shareId = id || ""
    if (name === "relay" && hosting.relay) {
      var relay = hosting.relay
      relayLabel.field.text = relay.label
      relayHost.field.text = relay.host
      relayPort.field.text = String(relay.port)
      relayAddress.field.text = relay.address
      relayServerName.field.text = relay.serverName
      relayPublicKey.field.text = relay.publicKey
      relayShortId.field.text = relay.shortId
    } else if (name === "share") {
      var share = editedShare
      shareAddress.field.text = share ? share.localAddress : "127.0.0.1"
      if (share) {
        shareLabel.field.text = share.label
        shareLocal.field.text = String(share.localPort)
        shareExposed.field.text = String(share.exposedPort)
        draftProtocol = share.protocol
      }
    }
    service.clearMessages()
    Qt.callLater(function() {
      if (host.form !== name) return
      panel.focusRow("hostField", 0)
      panel.scrollCursorIntoView()
      panel.beginEdit(host.fieldAt(0))
    })
  }

  function closeForm(returnTo) {
    var was = form
    clearForm()
    form = ""
    shareId = ""
    armed = ""
    panel.endEdit()
    panel.focusRow(returnTo || (was === "credential" ? "hostCredential" : was === "share" ? "hostNewShare" : "hostRelay"))
  }
  function reset() {
    open = false
    clearForm()
    form = ""
    shareId = ""
    armed = ""
  }
  function cancel() {
    if (form !== "") { closeForm(); return true }
    if (armed !== "") { armed = ""; return true }
    return false
  }

  function step(index, delta) {
    var next = index + delta
    if (next < 0) return
    if (next < fieldItems.length) {
      panel.focusRow("hostField", next)
      panel.scrollCursorIntoView()
      panel.beginEdit(fieldAt(next))
      return
    }
    panel.endEdit()
    panel.focusRow(form === "share" ? "hostProtocol" : "hostSubmit")
    panel.scrollCursorIntoView()
  }

  function submit() {
    if (!online || service.hostingBusy || form === "") return
    if (problem) {
      attempted = true
      panel.focusRow("hostField", problem.field)
      panel.beginEdit(fieldAt(problem.field))
      return
    }
    var done = function(ok) { if (ok) host.closeForm() }
    if (form === "relay") service.setHostingRelay(Model.relayFromForm({
      label: relayLabel.field.text, host: relayHost.field.text, port: relayPort.field.text,
      address: relayAddress.field.text, serverName: relayServerName.field.text,
      publicKey: relayPublicKey.field.text, shortId: relayShortId.field.text }), done)
    else if (form === "share") service.saveHostingShare(Model.shareFromForm({
      label: shareLabel.field.text, localAddress: shareAddress.field.text, localPort: shareLocal.field.text,
      exposedPort: shareExposed.field.text, protocol: draftProtocol }, editedShare), done)
    else {
      var uuid = credentialField.field.text
      credentialField.field.text = ""
      service.setHostingCredential(uuid, done)
      uuid = ""
    }
  }

  function confirmRemove(kind, run) {
    if (!online || service.hostingBusy) return
    if (armed !== kind) { armed = kind; return }
    armed = ""
    run()
  }

  function removeForm() {
    if (form === "relay") confirmRemove("relay", function() { service.removeHostingRelay(function(ok) { if (ok) host.closeForm() }) })
    else if (form === "credential") confirmRemove("credential", function() { service.removeHostingCredential(function(ok) { if (ok) host.closeForm() }) })
    else if (form === "share" && editedShare) {
      var id = editedShare.id
      confirmRemove("share:" + id, function() { service.removeHostingShare(id, function(ok) { if (ok) host.closeForm() }) })
    }
  }

  function setEnabled(share, enabled) {
    if (online && !service.hostingBusy) service.enableHostingShare(share.id, enabled)
  }

  function togglePause() {
    if (online && !service.hostingBusy) service.pauseHosting(!hosting.paused)
  }

  function adjust(row) {
    if (row.s === "hostPause") togglePause()
    else if (row.s === "hostShare" && hosting.shares[row.i]) setEnabled(hosting.shares[row.i], !hosting.shares[row.i].enabled)
  }

  function removeRow(row) {
    if (row.s === "hostShare" && hosting.shares[row.i]) {
      var id = hosting.shares[row.i].id
      confirmRemove("share:" + id, function() { service.removeHostingShare(id) })
    } else if (row.s === "hostRemove") removeForm()
  }

  function activate(row) {
    switch (row.s) {
    case "hosting": open = !open; if (!open) { closeForm("hosting") } break
    case "hostPause": togglePause(); break
    case "hostRetry": if (online) service.retryHosting(); break
    case "hostRelay": form === "relay" ? closeForm() : openForm("relay"); break
    case "hostCredential": form === "credential" ? closeForm() : openForm("credential"); break
    case "hostNewShare": form === "share" && shareId === "" ? closeForm() : openForm("share"); break
    case "hostShare":
      var share = hosting.shares[row.i]
      if (!share) break
      form === "share" && shareId === share.id ? closeForm("hostShare") : openForm("share", share.id)
      break
    case "hostField": panel.beginEdit(fieldAt(row.i)); break
    case "hostProtocol": protocolPicker.openPicker(); break
    case "hostCopy": if (editedShare) panel.copyText(Model.publicEndpoint(hosting.relay, editedShare)); break
    case "hostSubmit": submit(); break
    case "hostRemove": removeForm(); break
    }
  }

  Connections {
    target: host.panel
    function onCursorRowChanged() {
      var row = host.panel.cursorRow
      if (host.armed === "" || (row && row.s === "hostRemove")) return
      var share = row && row.s === "hostShare" ? host.hosting.shares[row.i] : null
      if (!share || host.armed !== "share:" + share.id) host.armed = ""
    }
  }

  SectionLine {
    theme: host.theme
    Layout.fillWidth: true
    Layout.topMargin: Style.space(8)
    title: "HOSTING"
    note: "share a local server with friends"
  }

  Rectangle {
    Layout.fillWidth: true
    implicitHeight: hostingColumn.implicitHeight + Style.space(6)
    radius: host.theme.cardRadius
    color: host.theme.fill
    border.width: 1
    border.color: host.theme.line

    ColumnLayout {
      id: hostingColumn
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.margins: Style.space(3)
      spacing: 0

      NavRow {
        id: headerRow
        panel: host.panel
        Layout.fillWidth: true
        section: "hosting"
        implicitHeight: headerColumn.implicitHeight + Style.space(14)
        onActivated: host.activate({ s: "hosting" })
        Accessible.role: Accessible.Button
        Accessible.name: "Hosting, " + host.summary.tag
        Accessible.description: host.summary.text

        RowLayout {
          anchors.fill: parent
          anchors.leftMargin: Style.space(8)
          anchors.rightMargin: Style.space(6)
          spacing: Style.space(8)

          ColumnLayout {
            id: headerColumn
            Layout.fillWidth: true
            spacing: Style.space(2)
            PanelText {
              theme: host.theme
              Layout.fillWidth: true
              text: "Hosting"
              color: host.theme.foreground
              font.pixelSize: Style.font.body
            }
            PanelText {
              theme: host.theme
              Layout.fillWidth: true
              text: host.summary.text
              color: host.summary.tone === "bad" ? host.theme.urgent : host.theme.faint
              wrapMode: Text.WordWrap
            }
          }

          Tag {
            theme: host.theme
            text: host.summary.tag
            tint: host.toneColor
            strong: host.summary.tone === "ok"
            filled: host.summary.tone === "ok"
          }

          Chevron { theme: host.theme; expanded: host.open; color: host.theme.dim }
        }
      }

      ColumnLayout {
        visible: host.open
        Layout.fillWidth: true
        Layout.leftMargin: Style.space(8)
        Layout.rightMargin: Style.space(8)
        Layout.bottomMargin: Style.space(4)
        spacing: Style.space(2)

        PanelText {
          theme: host.theme
          visible: host.hosting.relay !== null
          Layout.fillWidth: true
          text: host.hosting.relay ? host.hosting.relay.label + " · friends connect to " + host.hosting.relay.address : ""
          color: host.theme.dim
          elide: Text.ElideRight
        }
        PanelText {
          readonly property var live: host.hosting.shares.filter(function(share) { return share.enabled })
          theme: host.theme
          visible: host.hosting.relay !== null
          Layout.fillWidth: true
          text: (live.length === 0 ? "No ports selected" : "Ports " + live.map(function(share) {
            return share.exposedPort + "/" + share.protocol }).join(" · "))
            + " · " + host.hosting.active + " active"
          color: host.theme.dim
          wrapMode: Text.WordWrap
        }
        PanelText {
          theme: host.theme
          visible: text !== ""
          Layout.fillWidth: true
          text: Model.hostingCounters(host.hosting)
          color: host.theme.faint
        }
      }

      SettingRow {
        id: pauseRow
        panel: host.panel
        visible: host.open && host.hosting.relay !== null
        Layout.fillWidth: true
        section: "hostPause"
        title: "Pause hosting"
        subtitle: "closes the tunnel and every session, keeps your shares"
        checked: host.hosting.paused
        opacity: host.online ? 1 : 0.45
        Accessible.role: Accessible.CheckBox
        Accessible.name: "Pause hosting"
        Accessible.checked: checked
        onActivated: host.togglePause()
      }

      SettingRow {
        id: retryRow
        panel: host.panel
        visible: host.open && host.retryable
        Layout.fillWidth: true
        section: "hostRetry"
        title: "Retry now"
        subtitle: "try the relay again instead of waiting"
        tag: "retry"
        tagTint: host.theme.accent
        tagFilled: true
        onActivated: host.activate({ s: "hostRetry" })
      }

      SettingRow {
        id: relayRow
        panel: host.panel
        visible: host.open
        Layout.fillWidth: true
        section: "hostRelay"
        title: "Relay"
        subtitle: host.hosting.relay ? host.hosting.relay.host + ":" + host.hosting.relay.port
          : "the operator's relay: endpoint, private address, Reality keys"
        tag: host.form === "relay" ? "close" : (host.hosting.relay ? "edit…" : "add…")
        onActivated: host.activate({ s: "hostRelay" })
      }

      ColumnLayout {
        visible: host.open && host.form === "relay"
        Layout.fillWidth: true
        Layout.margins: Style.space(8)
        spacing: Style.space(6)
        HostingFormField { id: relayLabel; panel: host.panel; owner: host; index: 0; label: "Name"; placeholder: "Friends relay" }
        HostingFormField { id: relayHost; panel: host.panel; owner: host; index: 1; label: "Host"; placeholder: "relay.example.net" }
        HostingFormField { id: relayPort; panel: host.panel; owner: host; index: 2; label: "Port"; placeholder: "443" }
        HostingFormField { id: relayAddress; panel: host.panel; owner: host; index: 3; label: "Private address"; placeholder: "10.77.0.2" }
        HostingFormField { id: relayServerName; panel: host.panel; owner: host; index: 4; label: "Reality name"; placeholder: "www.example.com" }
        HostingFormField { id: relayPublicKey; panel: host.panel; owner: host; index: 5; label: "Public key"; placeholder: "Reality public key" }
        HostingFormField { id: relayShortId; panel: host.panel; owner: host; index: 6; label: "Short ID"; placeholder: "6ba85179e30d4fc2" }
        PanelText {
          theme: host.theme
          Layout.fillWidth: true
          text: "Replacing the relay closes the current tunnel first."
          color: host.theme.faint
          wrapMode: Text.WordWrap
        }
        ColumnLayout { id: relaySlot; Layout.fillWidth: true; spacing: 0 }
      }

      SettingRow {
        id: credentialRow
        panel: host.panel
        visible: host.open && host.hosting.relay !== null
        Layout.fillWidth: true
        section: "hostCredential"
        title: "Credential"
        subtitle: host.hosting.credentialPresent ? "stored" + (host.hosting.credentialId !== "" ? " · " + host.hosting.credentialId : "")
          : "not set; the relay cannot connect"
        urgentSubtitle: !host.hosting.credentialPresent && host.online
        tag: host.form === "credential" ? "close" : (host.hosting.credentialPresent ? "replace…" : "add…")
        onActivated: host.activate({ s: "hostCredential" })
      }

      ColumnLayout {
        visible: host.open && host.form === "credential"
        Layout.fillWidth: true
        Layout.margins: Style.space(8)
        spacing: Style.space(6)
        HostingFormField { id: credentialField; objectName: "credentialField"; panel: host.panel; owner: host; index: 0; label: "Credential"; placeholder: "publisher UUID"; secret: true }
        PanelText {
          theme: host.theme
          Layout.fillWidth: true
          text: "Hidden while you type, sent once to the core, then cleared. It is never shown again."
          color: host.theme.faint
          wrapMode: Text.WordWrap
        }
        ColumnLayout { id: credentialSlot; Layout.fillWidth: true; spacing: 0 }
      }

      PanelText {
        visible: host.open
        theme: host.theme
        Layout.fillWidth: true
        Layout.leftMargin: Style.space(8)
        Layout.topMargin: Style.space(6)
        text: "SHARES"
        color: host.theme.faint
        font.letterSpacing: Style.space(0.8)
      }

      Repeater {
        id: shareRepeater
        model: host.open ? host.hosting.shares : []
        HostingShareRow {
          required property var modelData
          panel: host.panel
          owner: host
          share: modelData
          Layout.fillWidth: true
        }
      }

      SettingRow {
        id: newShareRow
        panel: host.panel
        visible: host.open
        Layout.fillWidth: true
        section: "hostNewShare"
        title: host.hosting.shares.length === 0 ? "No shares yet" : "New share"
        subtitle: "a local server on this computer; saved off until you switch it on"
        tag: host.form === "share" && host.shareId === "" ? "close" : "add…"
        onActivated: host.activate({ s: "hostNewShare" })
      }

      ColumnLayout {
        visible: host.open && host.form === "share"
        Layout.fillWidth: true
        Layout.margins: Style.space(8)
        spacing: Style.space(6)
        HostingFormField { id: shareLabel; panel: host.panel; owner: host; index: 0; label: "Name"; placeholder: "Minecraft" }
        HostingFormField { id: shareAddress; panel: host.panel; owner: host; index: 1; label: "Local address"; placeholder: "127.0.0.1" }
        HostingFormField { id: shareLocal; panel: host.panel; owner: host; index: 2; label: "Local port"; placeholder: "25565" }
        HostingFormField { id: shareExposed; panel: host.panel; owner: host; index: 3; label: "Exposed port"; placeholder: "same as local" }
        RowLayout {
          Layout.fillWidth: true
          spacing: Style.space(8)
          PanelText {
            theme: host.theme
            Layout.preferredWidth: Style.space(86)
            text: "Protocol"
            color: host.theme.dim
          }
          Selector {
            id: protocolPicker
            panel: host.panel
            section: "hostProtocol"
            pickerHost: host.pickerHost
            placeholderText: "Filter protocols"
            choices: [{ value: "tcp", label: "TCP" }, { value: "udp", label: "UDP" }, { value: "both", label: "TCP+UDP" }]
            value: host.draftProtocol
            onSelected: function(value) { host.draftProtocol = value }
          }
          Item { Layout.fillWidth: true }
        }
        SettingRow {
          id: copyRow
          panel: host.panel
          visible: host.editedShare !== null && host.hosting.relay !== null
          Layout.fillWidth: true
          section: "hostCopy"
          title: "Copy address"
          subtitle: host.editedShare ? Model.publicEndpoint(host.hosting.relay, host.editedShare) + " · never the credential" : ""
          tag: "copy"
          onActivated: host.activate({ s: "hostCopy" })
        }
        PanelText {
          theme: host.theme
          Layout.fillWidth: true
          text: host.editedShare ? "Editing keeps this share's on/off state; changing the local target closes its sessions."
            : "Saved off. Switch it on to expose it. Only loopback addresses can be shared."
          color: host.theme.faint
          wrapMode: Text.WordWrap
        }
        ColumnLayout { id: shareSlot; Layout.fillWidth: true; spacing: 0 }
      }

      ColumnLayout {
        id: formTail
        visible: host.open && host.form !== ""
        parent: host.form === "relay" ? relaySlot : (host.form === "credential" ? credentialSlot : shareSlot)
        Layout.fillWidth: true
        spacing: 0
        PanelText {
          theme: host.theme
          visible: host.formMessage !== ""
          Layout.fillWidth: true
          text: "▲ " + host.formMessage
          color: host.theme.urgent
          wrapMode: Text.WordWrap
        }

        RowLayout {
          id: formButtons
          visible: host.open && host.form !== ""
          Layout.fillWidth: true
          Layout.topMargin: Style.space(4)
          spacing: Style.space(6)

          SmallButton {
            theme: host.theme
            primary: true
            text: host.form === "credential" ? "store credential" : "save"
            enabled: host.online && !host.service.hostingBusy
            hasCursor: host.panel.at("hostSubmit")
            onHovered: function(on) { if (on) host.panel.focusRow("hostSubmit") }
            onClicked: host.submit()
          }
          SmallButton {
            theme: host.theme
            text: "cancel"
            onClicked: host.closeForm()
          }
          Item { Layout.fillWidth: true }
          SmallButton {
            theme: host.theme
            visible: host.form === "relay" ? host.hosting.relay !== null
              : (host.form === "credential" ? host.hosting.credentialPresent : host.editedShare !== null)
            danger: true
            text: host.armed !== "" && host.armed.indexOf(host.form) === 0 ? "confirm remove" : "remove…"
            enabled: host.online && !host.service.hostingBusy
            hasCursor: host.panel.at("hostRemove")
            onHovered: function(on) { if (on) host.panel.focusRow("hostRemove") }
            onClicked: host.removeForm()
          }
        }
      }
    }
  }
}
