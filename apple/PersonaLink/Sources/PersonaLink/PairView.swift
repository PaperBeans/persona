import SwiftUI

/// 「配对 / 修改连接」 (Pair / Change Connection): choose how this Mac and the hub find each other. Active (「我去连 hub」): paste
/// the link the phone or the web made in 「添加电脑」 (or open it: `persona://link?…` fills this in), and look at or add the
/// hub's addresses. Passive (「让 hub 来连」): make the link the hub's admin page takes.
struct PairView: View {
    let model: LinkModel
    @State private var link = ""
    @State private var machine = ""
    @State private var result: (ok: Bool, message: String)?
    @State private var busy = false
    @State private var newAddress = ""
    @State private var addressError: String?
    /// Which mode the window is showing (not yet switched to: that happens on pairing, or with the switch-back button).
    @State private var tab: String
    @State private var listen: String
    @State private var invite: (link: String, note: String)?
    @State private var inviteError: String?
    @State private var copied = false
    @State private var advanced = false
    @State private var sshExpanded: Bool
    @State private var sshServer: String
    @State private var sshTarget: String
    @State private var sshTargetEdited: Bool
    @State private var sshAlways: Bool
    @State private var sshAlwaysEdited: Bool
    @State private var sshTesting = false
    @State private var sshSaving = false
    @State private var sshNotice: (ok: Bool, message: String)?

    init(model: LinkModel, link: String = "", machine: String = "", result: (ok: Bool, message: String)? = nil,
         newAddress: String = "", addressError: String? = nil, tab: String? = nil, invite: (link: String, note: String)? = nil,
         sshExpanded: Bool? = nil, sshServer: String? = nil, sshTarget: String? = nil, sshAlways: Bool? = nil,
         sshNotice: (ok: Bool, message: String)? = nil) {
        self.model = model
        let parsed: PairLink? = if case .link(let l) = PairLink.parse(link) { l } else { nil }
        _link = State(initialValue: link)
        _machine = State(initialValue: machine)
        _result = State(initialValue: result)
        _newAddress = State(initialValue: newAddress)
        _addressError = State(initialValue: addressError)
        _tab = State(initialValue: tab ?? (model.passive ? "passive" : "active"))
        _listen = State(initialValue: model.passivePairing?.listen ?? ":18663")
        _invite = State(initialValue: invite)
        _sshExpanded = State(initialValue: sshExpanded ?? (model.ssh != nil))
        _sshServer = State(initialValue: sshServer ?? model.ssh?.server ?? "")
        _sshTarget = State(initialValue: sshTarget ?? model.ssh?.target ?? parsed?.defaultSSHTarget ?? "")
        _sshTargetEdited = State(initialValue: sshTarget != nil || model.ssh != nil)
        _sshAlways = State(initialValue: sshAlways ?? model.ssh?.isAlways ?? parsed?.loopbackOnly ?? false)
        _sshAlwaysEdited = State(initialValue: sshAlways != nil || model.ssh != nil)
        _sshNotice = State(initialValue: sshNotice)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Picker("", selection: $tab) {
                Text("我去连 hub（主动）").tag("active")
                Text("让 hub 来连（被动）").tag("passive")
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            if tab == "passive" {
                passivePairing
            } else {
                pairing
                Divider()
                addresses
            }
        }
        .padding(20)
        .frame(width: 480)
    }

    /// The other mode is on but this one was paired before: back to it without pairing again.
    @ViewBuilder private func switchBack(to mode: String, paired: Bool, what: String) -> some View {
        if paired && (mode == "passive") != model.passive {
            HStack(spacing: 10) {
                Image(systemName: "arrow.uturn.backward.circle").foregroundStyle(.secondary)
                Text(what).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 8)
                Button {
                    Task { _ = await model.setMode(mode) }
                } label: {
                    Text("改用这个").lineLimit(1)
                }
            }
            .padding(10)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
        }
    }

    /// Passive mode's pairing in one sentence; its own function so the view below type-checks in time.
    private static func pairedText(_ p: PassivePairing) -> String {
        let hub: String = p.hubName.map { "「\($0)」" } ?? ""
        let machine: String = p.machine.map { "，这台是上面的「\($0)」" } ?? ""
        return "已经和 hub\(hub)配对\(machine)。再生成链接、换一台 hub 配对，原来的就连不进来了。"
    }

    private var passivePairing: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("让 hub 来连这台电脑").font(.title3.weight(.semibold))
            Text("这台电脑在本机监听一个端口，hub 按配对时拿到的指纹连进来。适合 hub 能直接连到的电脑：固定地址的台式机、服务器。笔记本换网络后 hub 连不到，选「我去连 hub」。")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let p = model.passivePairing, p.paired {
                Label {
                    Text(Self.pairedText(p)).fixedSize(horizontal: false, vertical: true)
                } icon: {
                    Image(systemName: "checkmark.seal").foregroundStyle(.green)
                }
                .font(.callout)
            }
            switchBack(to: "passive", paired: model.passivePairing?.paired == true, what: "现在是「我去连 hub」。被动模式的配对还在，不用重新配对。")
            HStack(spacing: 10) {
                Text("监听").font(.callout).foregroundStyle(.secondary)
                TextField(":18663", text: $listen)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(.callout, design: .monospaced))
                    .frame(width: 140)
                Spacer(minLength: 8)
                Button {
                    Task { await makeInvite() }
                } label: {
                    Text(busy ? "生成中…" : "生成配对链接").lineLimit(1)
                }
                .keyboardShortcut(.defaultAction)
                .disabled(busy)
            }
            if let invite {
                VStack(alignment: .leading, spacing: 8) {
                    Text("把这条链接粘贴到 hub 管理界面「电脑 › 添加电脑 › hub 来连」，10 分钟内有效，只能用一次：")
                        .font(.callout)
                        .fixedSize(horizontal: false, vertical: true)
                    HStack(alignment: .top, spacing: 10) {
                        Text(invite.link)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                            .padding(8)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
                        Button {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(invite.link, forType: .string)
                            copied = true
                        } label: {
                            Label(copied ? "已复制" : "复制", systemImage: copied ? "checkmark" : "doc.on.doc").lineLimit(1)
                        }
                    }
                    if !invite.note.isEmpty {
                        Text(invite.note)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            if let inviteError {
                Label {
                    Text(inviteError).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                } icon: {
                    Image(systemName: "xmark.octagon.fill").foregroundStyle(.red)
                }
                .font(.callout)
            }
        }
    }

    private func makeInvite() async {
        busy = true
        copied = false
        let r = await model.pairListen(listen: listen)
        busy = false
        if let l = r.link {
            invite = (l, Self.inviteNote(link: l, stderr: r.message))
            inviteError = nil
        } else {
            invite = nil
            inviteError = r.message
        }
    }

    /// Beside the link: which addresses are in it, and persona-link's line about the firewall.
    static func inviteNote(link: String, stderr: String) -> String {
        var lines: [String] = []
        let addrs = URLComponents(string: link)?.queryItems?.filter { $0.name == "addr" }.compactMap(\.value) ?? []
        if !addrs.isEmpty {
            lines.append("hub 按顺序试这些地址：\(addrs.joined(separator: "、"))；都连不到时在 hub 管理界面里改地址。")
        }
        lines += stderr.split(separator: "\n").map(String.init).filter { $0.hasPrefix("防火墙") }
        return lines.joined(separator: "\n")
    }

    private var pairing: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(model.pairedMachine == nil ? "配对这台电脑" : "重新配对").font(.title3.weight(.semibold))
            switchBack(to: "active", paired: model.pairedMachine != nil, what: "现在是「让 hub 来连」。主动模式的配对还在，不用重新配对。")
            if let notice = model.incomingNotice {
                Label {
                    Text(notice).fixedSize(horizontal: false, vertical: true)
                } icon: {
                    Image(systemName: "info.circle.fill").foregroundStyle(.orange)
                }
                .font(.callout)
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            }
            Text("在手机或网页的「添加电脑」里拷贝配对链接，粘贴到这里（或者直接点开那条链接）。链接 10 分钟内有效，只能用一次。")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            TextField("persona://link?…", text: $link)
                .textFieldStyle(.roundedBorder)
                .font(.system(.callout, design: .monospaced))
                .truncationMode(.middle)
            if let parsed = parsedLink {
                linkSummary(parsed)
            }
            if parsedLink?.machine == nil { // a link made for this computer names it: nothing to fill in then
                DisclosureGroup(isExpanded: $advanced) {
                    TextField("机器 id（链接里带了就不用填）", text: $machine)
                        .textFieldStyle(.roundedBorder)
                        .padding(.top, 6)
                } label: {
                    Text("高级").font(.callout).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            sshSection
            HStack {
                Spacer(minLength: 8)
                Button {
                    Task { await pair() }
                } label: {
                    Text(busy ? "配对中…" : "配对").lineLimit(1).frame(minWidth: 56)
                }
                .keyboardShortcut(.defaultAction)
                .disabled(busy || link.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            if let result {
                Label {
                    Text(result.message).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                } icon: {
                    Image(systemName: result.ok ? "checkmark.circle.fill" : "xmark.octagon.fill")
                        .foregroundStyle(result.ok ? Color.green : Color.red)
                }
                .font(.callout)
            }
        }
        .onChange(of: link) { _, _ in
            guard model.ssh == nil, let parsed = parsedLink else { return }
            if !sshTargetEdited { sshTarget = parsed.defaultSSHTarget ?? "" }
            if !sshAlwaysEdited { sshAlways = parsed.loopbackOnly }
        }
        .onChange(of: model.ssh) { _, saved in
            if let saved {
                sshExpanded = true
                sshServer = saved.server
                sshTarget = saved.target
                sshAlways = saved.isAlways
                sshTargetEdited = true
                sshAlwaysEdited = true
            } else if link.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                sshServer = ""
                sshTarget = ""
                sshAlways = false
                sshTargetEdited = false
                sshAlwaysEdited = false
            }
        }
        .onChange(of: model.incoming, initial: true) { _, incoming in
            guard let incoming else { return }
            fill(incoming)
            model.clearIncoming()
        }
    }

    private var sshServerTrimmed: String {
        sshServer.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private var sshTargetPlaceholder: String {
        parsedLink?.defaultSSHTarget ?? model.defaultSSHTarget
    }

    private var draftSSH: SSHConfig? {
        guard !sshServerTrimmed.isEmpty else { return nil }
        return SSHConfig(server: sshServerTrimmed,
                         target: sshTarget.trimmingCharacters(in: .whitespacesAndNewlines),
                         always: sshAlways)
    }

    private var sshChanged: Bool {
        guard let draft = draftSSH else { return false }
        guard let saved = model.ssh else { return true }
        let resolvedTarget = draft.target.isEmpty ? sshTargetPlaceholder : draft.target
        return draft.server != saved.server || resolvedTarget != saved.target || draft.isAlways != saved.isAlways
    }

    private var sshSection: some View {
        DisclosureGroup(isExpanded: $sshExpanded) {
            VStack(alignment: .leading, spacing: 8) {
                Text(linkText("Reach the hub through an SSH server using this Mac's ssh client, keys and ~/.ssh/config."))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                VStack(alignment: .leading, spacing: 4) {
                    Text(linkText("SSH server")).font(.caption).foregroundStyle(.secondary)
                    TextField(linkText("user@example.com or ~/.ssh/config alias"), text: $sshServer)
                        .textFieldStyle(.roundedBorder)
                        .font(.system(.callout, design: .monospaced))
                        .onChange(of: sshServer) { _, _ in sshNotice = nil }
                }
                VStack(alignment: .leading, spacing: 4) {
                    Text(linkText("Hub address as that server sees it")).font(.caption).foregroundStyle(.secondary)
                    TextField(sshTargetPlaceholder, text: Binding(
                        get: { sshTarget },
                        set: { next in
                            sshTarget = next
                            sshTargetEdited = true
                            sshNotice = nil
                        }
                    ))
                    .textFieldStyle(.roundedBorder)
                    .font(.system(.callout, design: .monospaced))
                }
                Toggle(linkText("Always use SSH"), isOn: Binding(
                    get: { sshAlways },
                    set: { next in
                        sshAlways = next
                        sshAlwaysEdited = true
                        sshNotice = nil
                    }
                ))
                .font(.callout)
                Text(linkText("When off, direct hub addresses are tried first and SSH is used only if they fail."))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 8) {
                    Button {
                        Task { await testSSH() }
                    } label: {
                        Text(sshTesting ? linkText("Testing…") : linkText("Test connection")).lineLimit(1)
                    }
                    .disabled(sshTesting || sshSaving || sshServerTrimmed.isEmpty)
                    Spacer(minLength: 8)
                    if model.ssh != nil {
                        Button(linkText("Remove tunnel")) {
                            Task { await removeSSH() }
                        }
                        .buttonStyle(.link)
                        .lineLimit(1)
                        .disabled(sshSaving || sshTesting)
                    }
                    if model.pairedMachine != nil {
                        Button {
                            Task { await saveSSH() }
                        } label: {
                            Text(sshSaving ? linkText("Saving…") : linkText("Save")).lineLimit(1)
                        }
                        .disabled(sshSaving || sshTesting || sshServerTrimmed.isEmpty || !sshChanged)
                    }
                }
                if let sshNotice {
                    Label {
                        Text(sshNotice.message).fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                    } icon: {
                        Image(systemName: sshNotice.ok ? "checkmark.circle.fill" : "xmark.octagon.fill")
                            .foregroundStyle(sshNotice.ok ? Color.green : Color.red)
                    }
                    .font(.callout)
                }
            }
            .padding(10)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            .padding(.top, 6)
        } label: {
            Text(linkText("SSH tunnel")).font(.callout).foregroundStyle(.secondary).lineLimit(1)
        }
    }

    /// The link in the field, when it is one for this app.
    private var parsedLink: PairLink? {
        if case .link(let l) = PairLink.parse(link) { return l }
        return nil
    }

    /// Which hub the link pairs with, said before anything is sent: the user checks it, then taps 「配对」 (Pair).
    private func linkSummary(_ l: PairLink) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Label {
                Text(l.hubHost.map { "要连的 hub：\($0)" } ?? "链接里没写 hub 的地址").textSelection(.enabled)
            } icon: {
                Image(systemName: "server.rack").foregroundStyle(.secondary)
            }
            if l.pinned {
                Label {
                    Text("已带安全指纹：只连配对时的那台 hub")
                } icon: {
                    Image(systemName: "lock.shield").foregroundStyle(.green)
                }
            }
            if let m = l.machine {
                Label {
                    Text("这台电脑在 hub 上叫「\(m)」")
                } icon: {
                    Image(systemName: "laptopcomputer").foregroundStyle(.secondary)
                }
            }
        }
        .font(.callout)
        .labelStyle(StatusLabelStyle())
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
    }

    /// Fills the window in from a link opened outside; never pairs.
    private func fill(_ l: PairLink) {
        tab = "active"
        link = l.raw
        machine = l.machine ?? ""
        result = nil
        if model.ssh == nil {
            if !sshTargetEdited { sshTarget = l.defaultSSHTarget ?? "" }
            if !sshAlwaysEdited { sshAlways = l.loopbackOnly }
        }
    }

    private struct AddressItem: Identifiable {
        var id: String
        var kindLabel: String
        var host: String
        var inUse: Bool
        var removableURL: String?
    }

    private var addressItems: [AddressItem] {
        let sshActive = model.status?.state == "connected" && model.status?.kind == "ssh"
        let direct = model.endpoints.map { e in
            AddressItem(id: e.id,
                        kindLabel: LinkText.kinds[e.kind] ?? e.kind,
                        host: LinkText.host(e.url),
                        inUse: !sshActive && model.status?.state == "connected" && model.status?.address == e.url,
                        removableURL: e.manual == true ? e.url : nil)
        }
        guard let ssh = model.ssh else { return direct }
        let sshRow = AddressItem(id: "ssh:\(ssh.server)",
                                 kindLabel: LinkText.kinds["ssh"] ?? linkText("SSH"),
                                 host: ssh.server,
                                 inUse: sshActive,
                                 removableURL: nil)
        return ssh.isAlways ? [sshRow] + direct : direct + [sshRow]
    }

    private var addresses: some View {
        let items = addressItems
        return VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text("hub 的地址").font(.headline)
                Text("按这个顺序尝试；连上以后 hub 会更新这份列表").font(.caption).foregroundStyle(.secondary)
            }
            if model.status?.systemTrust == true {
                Label("\(LinkText.systemTrust)：按系统信任的 CA 和域名校验，没有钉住 hub 的公钥", systemImage: "lock.shield")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if items.isEmpty {
                Text("还没有：配对以后会从链接和 hub 那里拿到。").font(.callout).foregroundStyle(.secondary)
            } else {
                VStack(spacing: 0) {
                    ForEach(items) { item in
                        HStack(spacing: 10) {
                            Text(item.kindLabel)
                                .font(.caption.weight(.medium))
                                .foregroundStyle(.secondary)
                                .frame(width: 64, alignment: .leading)
                            Text(item.host)
                                .font(.system(.callout, design: .monospaced))
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .textSelection(.enabled)
                            Spacer(minLength: 8)
                            if item.inUse {
                                Text("在用").font(.caption.weight(.semibold)).foregroundStyle(.green).lineLimit(1)
                            }
                            if let url = item.removableURL {
                                Button("移除") { Task { await model.removeAddress(url) } }
                                    .buttonStyle(.link)
                                    .lineLimit(1)
                            }
                        }
                        .padding(.vertical, 6)
                        .padding(.horizontal, 10)
                        if item.id != items.last?.id { Divider().padding(.leading, 84) }
                    }
                }
                .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            }
            HStack(spacing: 10) {
                TextField("再加一个地址，比如 hub.example.com", text: $newAddress)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { Task { await add() } }
                Button {
                    Task { await add() }
                } label: {
                    Text("添加").lineLimit(1).frame(minWidth: 40)
                }
                .disabled(model.pairedMachine == nil || newAddress.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            if let addressError {
                Text(addressError).font(.caption).foregroundStyle(.red)
            }
        }
    }

    private func pair() async {
        busy = true
        // A link made for this computer names it; the field is only for links that do not (「高级」, Advanced).
        result = await model.pair(link: link, machine: parsedLink?.machine == nil ? machine : "", ssh: draftSSH)
        busy = false
        if result?.ok == true { link = "" }
    }

    private func testSSH() async {
        sshTesting = true
        sshNotice = nil
        let r = await model.testSSH(server: sshServerTrimmed, target: sshTarget, link: link)
        sshTesting = false
        if let resolved = r.target, sshTarget.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            sshTarget = resolved
        }
        sshNotice = (r.ok, r.message)
    }

    private func saveSSH() async {
        guard let draft = draftSSH else { return }
        sshSaving = true
        sshNotice = nil
        let r = await model.setSSH(draft)
        sshSaving = false
        sshNotice = r
    }

    private func removeSSH() async {
        sshSaving = true
        sshNotice = nil
        let r = await model.setSSH(nil)
        sshSaving = false
        if r.ok {
            sshServer = ""
            sshTarget = ""
            sshAlways = false
            sshTargetEdited = false
            sshAlwaysEdited = false
        }
        sshNotice = r
    }

    private func add() async {
        let r = await model.addAddress(newAddress)
        addressError = r.ok ? nil : r.message
        if r.ok { newAddress = "" }
    }
}
