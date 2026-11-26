import AppKit
import Foundation
import Network
import ServiceManagement

/// A hub address persona-link may try, as it reports them (internal/link).
struct LinkEndpoint: Codable, Hashable, Identifiable {
    var url: String
    var kind: String
    var manual: Bool?
    var id: String { url }
}

/// Optional SSH tunnel settings in link.json and status (`ssh`, internal/link SSH).
struct SSHConfig: Codable, Equatable {
    var server: String
    var target: String
    var always: Bool?

    var isAlways: Bool { always == true }
}

/// Structured SSH failure code reported while waiting (`ssh_error`, internal/link SSHStatusError).
struct SSHStatusError: Decodable, Equatable {
    var code: String
    var server: String?
    var target: String?
}

/// One status line from `persona-link run` (internal/link Status).
struct LinkStatus: Decodable, Equatable {
    var state: String // unpaired | connecting | connected | waiting | refused
    var machine: String?
    var address: String?
    var kind: String?
    var since: Date?
    var node: String? // online | offline | unknown
    var nodeError: String?
    var error: String?
    var retry: Date?
    var endpoints: [LinkEndpoint]?
    var ssh: SSHConfig?
    var sshError: SSHStatusError?
    /// What the hub calls itself, and (while connected) how many hubs its mesh has; older hubs send neither.
    var hubName: String?
    var meshHubs: Int?
    /// What the hub calls this computer (docs/operations/link.md "The computer's name"); older hubs say nothing: `machine`,
    /// its id, is shown instead.
    var machineName: String?
    /// active (this Mac dials the hub) | passive (the hub dials this Mac); older helpers say nothing: active.
    var mode: String?
    /// Passive mode: where it listens, the addresses the hub may use, when the hub last came in and from where, and
    /// until when a pairing link is good. Connected: `address` is the hub's address.
    var listen: String?
    var addrs: [String]?
    var lastSeen: Date?
    var from: String?
    var codeExpires: Date?
    /// "system": the hub sits behind an HTTPS reverse proxy and is checked with the system's CAs and its host name, not
    /// a pin (docs/operations/link.md「Hub behind a reverse proxy」); nil when pinned or with older helpers.
    var trust: String?
    /// The hub still waits for someone to confirm this computer in the phone App or on the web (「是我的电脑」,
    /// docs/assistant/onboarding.md 4.2). Helpers and hubs that do not say yet send nothing: shown as connected.
    var pending: Bool?
    /// The persona-link that wrote link.status.json, and its version ("stopped" when it exited normally).
    var pid: Int?
    var version: String?

    var passive: Bool { mode == "passive" }
    var systemTrust: Bool { trust == "system" }
    /// The name to show for this computer: the hub's, else its id.
    var shownMachine: String? { machineName.flatMap { $0.isEmpty ? nil : $0 } ?? machine.flatMap { $0.isEmpty ? nil : $0 } }
    /// This computer can be renamed from here: active mode, connected to a hub that renames computers (it says the name),
    /// and confirmed (a computer still waiting to be confirmed cannot rename itself).
    var renamable: Bool { !passive && state == "connected" && !(machineName ?? "").isEmpty && pending != true }

    enum CodingKeys: String, CodingKey {
        case state, machine, address, kind, since, node, error, retry, endpoints, ssh, mode, listen, addrs, from, trust, pending, pid, version
        case sshError = "ssh_error"
        case nodeError = "node_error"
        case hubName = "hub_name"
        case meshHubs = "mesh_hubs"
        case machineName = "machine_name"
        case lastSeen = "last_seen"
        case codeExpires = "code_expires"
    }
}

/// Passive mode's part of link.json, as far as the app shows it (never the credential's hash).
struct PassivePairing: Decodable, Equatable {
    var listen: String?
    var hubName: String?
    var machine: String?
    var paired: Bool

    enum CodingKeys: String, CodingKey {
        case listen, machine
        case hubName = "hub_name"
        case tokenHash = "token_sha256"
    }

    init(listen: String? = nil, hubName: String? = nil, machine: String? = nil, paired: Bool) {
        self.listen = listen
        self.hubName = hubName
        self.machine = machine
        self.paired = paired
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        listen = try c.decodeIfPresent(String.self, forKey: .listen)
        hubName = try c.decodeIfPresent(String.self, forKey: .hubName)
        machine = try c.decodeIfPresent(String.self, forKey: .machine)
        paired = !(try c.decodeIfPresent(String.self, forKey: .tokenHash) ?? "").isEmpty
    }
}

/// The resident node's service, as `persona-link node status --json` says.
struct NodeService: Decodable, Equatable {
    var installed: Bool
    var file: String?
    var hasBin: Bool
    var running: String?

    enum CodingKeys: String, CodingKey {
        case installed, file, running
        case hasBin = "has_bin"
    }
}

/// What the menu bar shows: the four states of the link, a link that is up while the node is not or while the hub
/// waits for the user to confirm this computer, and passive mode listening for the hub. The icon is the Persona helper
/// (MenuBarIcon), as the Windows tray draws it (server/internal/linktray/icons.go): faded while the link is not up, with
/// a dot on the states that need the user.
enum LinkLook: CaseIterable {
    case connected, awaitingConfirm, nodeMissing, connecting, listening, down, unpaired

    /// The helper is drawn faded: the link is not up.
    var faded: Bool {
        switch self {
        case .connecting, .down, .unpaired: true
        default: false
        }
    }

    /// A dot in the icon's corner: something needs the user.
    var badge: Bool {
        switch self {
        case .awaitingConfirm, .nodeMissing, .down: true
        default: false
        }
    }
}

enum LinkText {
    static var kinds: [String: String] {
        ["lan": linkText("LAN"), "ipv6": linkText("Public IPv6"), "name": linkText("Domain"), "ipv4": linkText("Public IPv4"), "ssh": linkText("SSH")]
    }

    static func sshLoginCommand(_ server: String) -> String {
        let s = server.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return "ssh <server>" }
        let user: String
        let hostPort: Substring
        if let at = s.lastIndex(of: "@") {
            user = String(s[..<at])
            hostPort = s[s.index(after: at)...]
        } else {
            user = ""
            hostPort = s[...]
        }
        if hostPort.hasPrefix("["), let close = hostPort.firstIndex(of: "]") {
            let host = String(hostPort[hostPort.index(after: hostPort.startIndex)..<close])
            let rest = hostPort[hostPort.index(after: close)...]
            let dest = user.isEmpty ? host : "\(user)@\(host)"
            if rest.hasPrefix(":") {
                let port = String(rest.dropFirst())
                if !port.isEmpty && port != "22" { return "ssh -p \(port) \(dest)" }
            }
            return "ssh \(dest)"
        }
        if let colon = hostPort.lastIndex(of: ":"), hostPort.firstIndex(of: ":") == colon {
            let host = String(hostPort[..<colon])
            let port = String(hostPort[hostPort.index(after: colon)...])
            if let p = Int(port), (1...65535).contains(p) {
                let dest = user.isEmpty ? host : "\(user)@\(host)"
                return port == "22" ? "ssh \(dest)" : "ssh -p \(port) \(dest)"
            }
        }
        return "ssh \(s)"
    }

    static func sshError(_ e: SSHStatusError) -> String {
        let server = e.server ?? ""
        let target = e.target ?? ""
        let cmd = sshLoginCommand(server)
        switch e.code {
        case "no_ssh":
            return linkText("There is no ssh command on this computer; install an OpenSSH client first.")
        case "auth_refused":
            return linkText("The SSH server (\(server)) refused authentication; check your key, ssh-agent, or ~/.ssh/config.")
        case "host_key":
            return linkText("This SSH server's host key isn't trusted yet; run “\(cmd)” once in a terminal to check and accept it.")
        case "forward_refused":
            return linkText("The SSH server refused port forwarding to \(target); check AllowTcpForwarding and permitopen on the server.")
        case "target_unreachable":
            return linkText("The hub isn't answering at \(target) through the SSH server; check that the hub is running and the target port is right.")
        case "invalid":
            return linkText("Enter an SSH server as [user@]host[:port] or an alias, and a target as host:port.")
        default:
            return linkText("Can't reach the SSH server (\(server)); check the server address and this computer's network.")
        }
    }

    /// The host and port of a hub URL, and the path a reverse proxy puts the hub under.
    static func host(_ url: String?) -> String {
        guard let url, let c = URLComponents(string: url), let host = c.host else { return url ?? "" }
        let shown = host.contains(":") && !host.hasPrefix("[") ? "[\(host)]" : host
        return (c.port.map { "\(shown):\($0)" } ?? shown) + c.percentEncodedPath
    }

    /// How a hub behind an HTTPS reverse proxy is checked.
    static var systemTrust: String { linkText("Via reverse proxy · system certificate") }

    static func duration(since: Date, now: Date) -> String {
        let s = max(0, Int(now.timeIntervalSince(since)))
        switch s {
        case ..<60: return linkText("Under 1 minute")
        case ..<3600: return linkText("\(s / 60) min")
        case ..<86400: return linkText("\(s / 3600) h \(s % 3600 / 60) min")
        default: return linkText("\(s / 86400) d \(s % 86400 / 3600) h")
        }
    }

    /// The hub line: its name, and how many hubs its mesh has when that is known.
    static func hub(name: String, mesh: Int?) -> String {
        switch mesh {
        case let n? where n > 1: linkText("Hub “\(name)” · \(n) hubs in its mesh")
        case 1: linkText("Hub “\(name)” · not meshed with other hubs")
        default: linkText("Hub “\(name)”")
        }
    }

    static func until(_ date: Date, now: Date) -> String {
        let s = Int(date.timeIntervalSince(now).rounded(.up))
        if s <= 0 { return linkText("Retry now") }
        if s < 60 { return linkText("Retry in \(s) s") }
        return linkText("Retry in \(s / 60) min")
    }

    /// How long ago, in a few words.
    static func ago(_ date: Date, now: Date) -> String {
        let s = max(0, Int(now.timeIntervalSince(date)))
        switch s {
        case ..<60: return linkText("Just now")
        case ..<3600: return linkText("\(s / 60) min ago")
        case ..<86400: return linkText("\(s / 3600) h ago")
        default: return linkText("\(s / 86400) d ago")
        }
    }

    /// How long a pairing link is still good, in a few words.
    static func left(_ date: Date, now: Date) -> String {
        let s = Int(date.timeIntervalSince(now).rounded(.up))
        if s < 60 { return linkText("Under 1 minute") }
        return linkText("\(s / 60) min")
    }

    static func mode(_ passive: Bool) -> String { passive ? linkText("The hub connects to me (passive)") : linkText("I connect to the hub (active)") }
}

/// What the menu bar App knows about the independently installed persona-link (launchd `com.persona.link`,
/// docs/operations/link.md "Tray and menu bar App"): it reads link.status.json and link.json, and does everything
/// through the persona-link command, as the Windows tray does. It never runs the connector itself, so quitting the App
/// leaves the connection up.
@MainActor @Observable final class LinkModel {
    static let shared = LinkModel()

    private(set) var status: LinkStatus?
    /// A persona-link keeps link.status.json now: its pid is alive and it has not said it stopped.
    private(set) var running = false
    /// link.status.json and link.json have been read at least once.
    private(set) var loaded = false
    /// The launchd agent persona-link install writes (~/Library/LaunchAgents/com.persona.link.plist).
    private(set) var serviceInstalled = false
    /// Installing or restarting the background service, and what came of it when it did not work.
    private(set) var serviceBusy = false
    private(set) var serviceNote: String?
    private(set) var launchAtLogin = SMAppService.mainApp.status == .enabled
    private(set) var loginNote: String?
    /// The candidates in link.json (read here, written only by persona-link).
    private(set) var endpoints: [LinkEndpoint] = []
    private(set) var ssh: SSHConfig?
    private(set) var pairedMachine: String?
    /// The mode link.json is in, and passive mode's pairing (kept while in active mode, for switching back).
    private(set) var configMode: String?
    private(set) var passivePairing: PassivePairing?
    /// The resident node's service; nil until asked.
    private(set) var nodeService: NodeService?
    private(set) var nodeNote: String?
    /// A pairing link opened from outside (`persona://link?…`), for the pairing window to fill in; it never pairs by
    /// itself. `incomingNotice`: a link of ours that is not for this app, said in the window instead.
    private(set) var incoming: PairLink?
    private(set) var incomingNotice: String?
    /// How many times pairing ran (`persona-link pair`): only the 「配对」 (Pair) button does it.
    private(set) var pairRuns = 0

    @ObservationIgnored private var poller: Task<Void, Never>?
    @ObservationIgnored private let monitor = NWPathMonitor()
    @ObservationIgnored private var sawFirstPath = false

    /// The programs the App carries: persona-link, to install the background service, and persona-node, the resident
    /// node it sets up with it (docs/operations/link.md "The device's program").
    nonisolated static let bundledHelperURL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/persona-link")
    nonisolated static let nodeURL = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/persona-node")
    nonisolated static let stateEnvironment = LinkStatePaths.serviceEnvironment(
        home: FileManager.default.homeDirectoryForCurrentUser,
        environment: ProcessInfo.processInfo.environment
    )
    nonisolated static let stateURL = LinkStatePaths.root(
        home: FileManager.default.homeDirectoryForCurrentUser,
        environment: stateEnvironment
    )
    /// The installed persona-link the background service runs (persona-link install puts it there).
    nonisolated static let installedHelperURL = stateURL.appendingPathComponent("bin/persona-link")
    nonisolated static let configURL = stateURL.appendingPathComponent("link.json")
    nonisolated static let statusURL = stateURL.appendingPathComponent("link.status.json")
    nonisolated static let serviceURL = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/LaunchAgents/com.persona.link.plist")
    nonisolated static var helperEnvironment: [String: String] {
        var environment = stateEnvironment
        environment["PERSONA_HOME"] = stateURL.path
        environment[LegacyNames.product.uppercased() + "_HOME"] = stateURL.path
        // persona-link's messages in the App's language (it reads PERSONA_LANG, server/internal/i18n).
        environment["PERSONA_LANG"] = linkLanguage
        return environment
    }
    /// The installed persona-link, else the App's own.
    nonisolated static var helperURL: URL {
        FileManager.default.isExecutableFile(atPath: installedHelperURL.path) ? installedHelperURL : bundledHelperURL
    }

    var look: LinkLook {
        guard loaded else { return .connecting }
        guard running else { return paired ? .down : .unpaired }
        switch status?.state {
        case "connected", "listening":
            if status?.pending == true { return .awaitingConfirm }
            if status?.node == "offline" { return .nodeMissing }
            return status?.state == "connected" ? .connected : .listening
        case "connecting", nil: return .connecting
        case "unpaired": return .unpaired
        default: return .down
        }
    }

    /// This computer has a pairing, in either mode.
    var paired: Bool { pairedMachine != nil || passivePairing?.paired == true }

    /// Passive mode: what persona-link says, else what link.json says.
    var passive: Bool { (running ? status?.mode : nil).map { $0 == "passive" } ?? (configMode == "passive") }

    /// The status to show: persona-link's while it runs; without it, nothing it said still holds.
    var liveStatus: LinkStatus? { running ? status : nil }

    /// Default SSH tunnel target (`127.0.0.1:<TLS port>`) from saved endpoints, or the hub's usual TLS port.
    var defaultSSHTarget: String {
        for e in endpoints {
            guard let c = URLComponents(string: e.url), c.scheme?.lowercased() == "https", !(c.host ?? "").isEmpty else { continue }
            let port = c.port ?? 443
            if (1...65535).contains(port) { return "127.0.0.1:\(port)" }
        }
        return "127.0.0.1:18662"
    }

    func start() {
        // Every 1.5 s, off the main actor: two small files and a kill(pid, 0) (docs/apple/performance.md P1).
        poller = Task.detached(priority: .utility) { [weak self] in
            while !Task.isCancelled {
                let snapshot = Self.read()
                await self?.apply(snapshot)
                try? await Task.sleep(for: .milliseconds(1500))
            }
        }
        Task { await refreshNodeService() }
        monitor.pathUpdateHandler = { _ in
            Task { @MainActor in LinkModel.shared.networkChanged() }
        }
        monitor.start(queue: .main)
    }

    /// Read now rather than at the next tick (after a command changed something).
    func refresh() {
        Task.detached(priority: .userInitiated) { [weak self] in
            let snapshot = Self.read()
            await self?.apply(snapshot)
        }
    }

    /// Used by the snapshots and tests: a state without reading any file.
    func show(_ status: LinkStatus?, running: Bool = true, serviceInstalled: Bool = true, endpoints: [LinkEndpoint] = [],
              machine: String? = nil, mode: String? = nil, passive: PassivePairing? = nil, node: NodeService? = nil,
              ssh: SSHConfig? = nil) {
        self.status = status
        self.running = running && status != nil
        self.loaded = true
        self.serviceInstalled = serviceInstalled
        self.endpoints = endpoints
        self.ssh = ssh ?? status?.ssh
        self.pairedMachine = machine
        self.configMode = mode ?? status?.mode
        self.passivePairing = passive
        self.nodeService = node
    }

    /// What the files say now: read off the main thread (docs/apple/performance.md P1).
    struct Snapshot: Sendable, Equatable {
        var status: LinkStatus?
        var running: Bool
        var serviceInstalled: Bool
        var config: Config?
    }

    /// link.json as far as the App shows it (never the credential itself).
    struct Config: Decodable, Sendable, Equatable {
        var machine: String?
        var token: String?
        var endpoints: [LinkEndpoint]?
        var ssh: SSHConfig?
        var mode: String?
        var passive: PassivePairing?

        static func == (a: Config, b: Config) -> Bool {
            a.machine == b.machine && (a.token ?? "").isEmpty == (b.token ?? "").isEmpty && a.endpoints == b.endpoints
                && a.ssh == b.ssh && a.mode == b.mode && a.passive == b.passive
        }
    }

    nonisolated static func read() -> Snapshot {
        let status = (try? Data(contentsOf: statusURL)).flatMap { try? decoder.decode(LinkStatus.self, from: $0) }
        let config = (try? Data(contentsOf: configURL)).flatMap { try? JSONDecoder().decode(Config.self, from: $0) }
        return Snapshot(status: status, running: alive(status), serviceInstalled: FileManager.default.fileExists(atPath: serviceURL.path),
                        config: config)
    }

    /// The persona-link that wrote `status` still runs: it did not say it stopped, and its pid is a live process.
    nonisolated static func alive(_ status: LinkStatus?) -> Bool {
        guard let status, status.state != "stopped", let pid = status.pid, pid > 0 else { return false }
        return kill(pid_t(pid), 0) == 0 || errno == EPERM
    }

    private func apply(_ s: Snapshot) {
        if !loaded { loaded = true }
        if status != s.status { status = s.status }
        if running != s.running { running = s.running }
        if serviceInstalled != s.serviceInstalled { serviceInstalled = s.serviceInstalled }
        let c = s.config
        let next = Self.ranked(c?.endpoints ?? [])
        if endpoints != next { endpoints = next }
        if ssh != c?.ssh { ssh = c?.ssh }
        let machine = (c?.token ?? "").isEmpty ? nil : c?.machine
        if pairedMachine != machine { pairedMachine = machine }
        if configMode != c?.mode { configMode = c?.mode }
        if passivePairing != c?.passive { passivePairing = c?.passive }
    }

    private func signal(_ sig: Int32) {
        if running, let pid = status?.pid, pid > 0 { kill(pid_t(pid), sig) }
    }

    /// The network changed: persona-link looks at its link at once instead of waiting for the next heartbeat.
    private func networkChanged() {
        if sawFirstPath { signal(SIGUSR1) }
        sawFirstPath = true
    }

    /// `persona-link reconnect`: the running connector drops its link and dials again.
    func reconnect() {
        Task {
            _ = await Self.runHelper(["reconnect"], stdin: nil)
            refresh()
        }
    }

    func setLaunchAtLogin(_ on: Bool) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            loginNote = nil
        } catch {
            loginNote = linkText("Can't change it: \(error.localizedDescription)")
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
        if on && SMAppService.mainApp.status == .requiresApproval {
            loginNote = linkText("Allow it in System Settings › General › Login Items")
        }
    }

    /// The folder with link.log and the node's log.
    func openLogs() {
        Task.detached(priority: .userInitiated) { NSWorkspace.shared.open(Self.stateURL) }
    }

    /// Quits the App only: the background service keeps the connection.
    func quit() { NSApp.terminate(nil) }

    // MARK: the background service

    /// `persona-link install`, from the program the App carries (so an updated App updates the service), with the
    /// App's persona-node for the resident node when this Mac has none. It waits for the link, up to 20 seconds.
    func installService() async {
        serviceBusy = true
        serviceNote = nil
        let r = await Self.runInstall()
        if !r.ok { serviceNote = r.message }
        serviceBusy = false
        refresh()
        await refreshNodeService()
    }

    /// `persona-link install` as installService runs it, looked up and run off the main actor.
    nonisolated static func runInstall() async -> (ok: Bool, message: String) {
        let fm = FileManager.default
        let exe = fm.isExecutableFile(atPath: bundledHelperURL.path) ? bundledHelperURL : installedHelperURL
        var args = ["install"]
        args += fm.isExecutableFile(atPath: nodeURL.path) ? ["--node-bin", nodeURL.path] : ["--no-node"]
        let r = await runHelper(executable: exe, arguments: args, stdin: nil, environment: helperEnvironment, split: true)
        return (r.ok, r.message)
    }

    /// Down, or up without its node: set it up again. The node's service when that is what is missing, else the
    /// connector's own.
    func restartService() async {
        if look == .nodeMissing, nodeService?.installed == true {
            await reinstallNode()
        } else {
            await installService()
        }
    }

    // MARK: pairing and addresses: one-off runs of persona-link, which owns link.json

    /// A `persona://` link opened from outside: the pairing window fills in a link for this app and says what a link
    /// for another one is. Nothing is sent to any hub here.
    func openLink(_ url: URL) {
        let parsed = PairLink.parse(url)
        if case .link(let l) = parsed {
            incoming = l
            incomingNotice = nil
        } else if let notice = PairLink.notice(for: parsed) {
            incoming = nil
            incomingNotice = notice
        }
    }

    /// The pairing window took the incoming link into its field.
    func clearIncoming() { incoming = nil }

    /// The notice about a link for another app goes once the user pairs or opens a link for this one.
    func clearNotice() { incomingNotice = nil }

    /// `persona-link pair` with the link on stdin; a Mac without the background service gets it set up after.
    func pair(link: String, machine: String, ssh: SSHConfig? = nil) async -> (ok: Bool, message: String) {
        pairRuns += 1
        incomingNotice = nil
        var args = ["pair"]
        let id = machine.trimmingCharacters(in: .whitespacesAndNewlines)
        if !id.isEmpty { args += ["--machine", id] }
        if let ssh {
            let srv = ssh.server.trimmingCharacters(in: .whitespacesAndNewlines)
            if !srv.isEmpty {
                args += ["--ssh", srv]
                let tgt = ssh.target.trimmingCharacters(in: .whitespacesAndNewlines)
                if !tgt.isEmpty { args += ["--ssh-target", tgt] }
                args.append(ssh.isAlways ? "--ssh-always" : "--no-ssh-always")
            }
        }
        args.append("-")
        let r = await Self.runHelper(args, stdin: link)
        refresh()
        if r.ok && !serviceInstalled { await installService() }
        return r
    }

    func setSSH(_ next: SSHConfig?) async -> (ok: Bool, message: String) {
        let args: [String]
        if let next {
            let srv = next.server.trimmingCharacters(in: .whitespacesAndNewlines)
            var a = ["ssh", srv]
            let tgt = next.target.trimmingCharacters(in: .whitespacesAndNewlines)
            if !tgt.isEmpty { a += ["--target", tgt] }
            a.append(next.isAlways ? "--always" : "--no-always")
            a.append("--json")
            args = a
        } else {
            args = ["ssh", "--off", "--json"]
        }
        let r = await Self.runHelper(args, stdin: nil, split: true)
        refresh()
        if r.ok {
            reconnect()
            return (true, next == nil ? linkText("SSH tunnel removed.") : linkText("SSH tunnel saved. Reconnecting…"))
        }
        return (false, r.message)
    }

    func testSSH(server: String, target: String, link: String) async -> (ok: Bool, message: String, target: String?) {
        struct Reply: Decodable {
            var ok: Bool
            var code: String?
            var server: String?
            var target: String?
            var message: String
        }
        var args = ["ssh", server.trimmingCharacters(in: .whitespacesAndNewlines), "--test", "--json"]
        let tgt = target.trimmingCharacters(in: .whitespacesAndNewlines)
        if !tgt.isEmpty { args += ["--target", tgt] }
        let raw = link.trimmingCharacters(in: .whitespacesAndNewlines)
        if !raw.isEmpty { args += ["--link", raw] }
        let r = await Self.runHelper(args, stdin: nil, split: true)
        if let reply = try? JSONDecoder().decode(Reply.self, from: Data(r.out.utf8)) {
            if reply.ok {
                let resolved = reply.target ?? tgt
                return (true, linkText("SSH tunnel reached the hub at \(resolved)."), reply.target)
            }
            if let code = reply.code, !code.isEmpty {
                let se = SSHStatusError(code: code, server: reply.server ?? server, target: reply.target ?? tgt)
                return (false, LinkText.sshError(se), nil)
            }
            return (false, reply.message, nil)
        }
        return (r.ok, r.message, nil)
    }

    func addAddress(_ raw: String) async -> (ok: Bool, message: String) {
        let r = await Self.runHelper(["address", "add", raw.trimmingCharacters(in: .whitespacesAndNewlines)], stdin: nil)
        refresh()
        return r
    }

    func removeAddress(_ url: String) async {
        _ = await Self.runHelper(["address", "remove", url], stdin: nil)
        refresh()
    }

    // MARK: passive mode

    /// `persona-link pair --listen`: the link for the hub's admin page (stdout), or why not.
    func pairListen(listen: String) async -> (ok: Bool, message: String, link: String?) {
        var args = ["pair", "--listen"]
        let l = listen.trimmingCharacters(in: .whitespacesAndNewlines)
        if !l.isEmpty { args.append(l) }
        let r = await Self.runHelper(args, stdin: nil, split: true)
        refresh()
        guard r.ok, PairLink.isConnectorOutput(r.out) else { return (false, r.message, nil) }
        if !serviceInstalled { await installService() }
        return (true, r.err, r.out)
    }

    /// `persona-link name <name>` ("" is the id again: `--reset`): the running connector asks the hub over its link,
    /// and says what the hub calls this computer now, or why not.
    func rename(_ name: String) async -> (ok: Bool, message: String) {
        let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return await Self.runHelper(["name", n.isEmpty ? "--reset" : n], stdin: nil)
    }

    /// `persona-link mode <mode>`: switches without pairing again; the connector follows within a second.
    func setMode(_ mode: String) async -> (ok: Bool, message: String) {
        let r = await Self.runHelper(["mode", mode], stdin: nil)
        refresh()
        return (r.ok, r.message)
    }

    // MARK: the resident node

    func refreshNodeService() async {
        let r = await Self.runHelper(["node", "status", "--json"], stdin: nil, split: true)
        nodeService = try? JSONDecoder().decode(NodeService.self, from: Data(r.out.utf8))
    }

    /// `persona-link node install --node-bin <the App's persona-node>`: the resident node's launchd agent, unless there
    /// is one.
    func installNode() async {
        nodeNote = nil
        let r = await Self.runNodeInstall()
        if !r.ok { nodeNote = r.message }
        await refreshNodeService()
    }

    /// `persona-link node install`, with the App's persona-node when it carries one.
    nonisolated static func runNodeInstall() async -> (ok: Bool, message: String) {
        var args = ["node", "install"]
        if FileManager.default.isExecutableFile(atPath: nodeURL.path) { args += ["--node-bin", nodeURL.path] }
        return await runHelper(args, stdin: nil)
    }

    /// The resident node's service is there but not running: set it up again (`node uninstall` keeps the node program
    /// in ~/.persona/bin, `node install` loads the service anew).
    func reinstallNode() async {
        nodeNote = nil
        _ = await Self.runHelper(["node", "uninstall"], stdin: nil)
        await installNode()
        if nodeNote == nil { signal(SIGUSR1) }
    }

    /// The order persona-link tries them in (internal/link ranked).
    nonisolated static func ranked(_ eps: [LinkEndpoint]) -> [LinkEndpoint] {
        // Same URL may arrive both manually and from the hub; keep the first occurrence like the Go reference (manual
        // ones are ordered first upstream), then rank.
        var seen = Set<String>()
        let unique = eps.filter { seen.insert($0.url).inserted }
        let rank = ["lan": 0, "ipv6": 1, "name": 2]
        return unique.enumerated().sorted { a, b in
            let ra = rank[a.element.kind] ?? 3, rb = rank[b.element.kind] ?? 3
            if ra != rb { return ra < rb }
            let ma = a.element.manual == true, mb = b.element.manual == true
            if ma != mb { return ma }
            return a.offset < b.offset
        }.map(\.element)
    }

    nonisolated static func runHelper(_ args: [String], stdin: String?) async -> (ok: Bool, message: String) {
        let r = await runHelper(args, stdin: stdin, split: true)
        return (r.ok, r.message)
    }

    /// Runs persona-link once: `message` is its stdout when it worked, else its stderr (or stdout); `out` and `err` apart.
    nonisolated static func runHelper(_ args: [String], stdin: String?, split: Bool) async
        -> (ok: Bool, message: String, out: String, err: String) {
        await runHelper(executable: helperURL, arguments: args, stdin: stdin, environment: helperEnvironment, split: split)
    }

    /// Runs an executable once with the given environment (testable seam): `message` is its stdout when it worked,
    /// else its stderr (or stdout); `out` and `err` apart.
    nonisolated static func runHelper(executable: URL, arguments: [String], stdin: String?, environment: [String: String],
                                     split: Bool) async
        -> (ok: Bool, message: String, out: String, err: String) {
        await withCheckedContinuation { done in
            let p = Process()
            p.executableURL = executable
            p.environment = environment
            p.arguments = arguments
            let out = Pipe(), err = Pipe(), input = Pipe()
            p.standardOutput = out
            p.standardError = err
            p.standardInput = input
            // The termination handler runs on a background queue while launch/stdin errors throw on the caller's
            // queue, so both finishing paths go through this once-guard: the continuation resumes exactly once.
            let once = Once()
            let finish: @Sendable (Bool, String, String, String) -> Void = { ok, message, o, e in
                guard once.claim() else { return }
                done.resume(returning: (ok, message, o, e))
            }
            p.terminationHandler = { proc in
                let o = (String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                let e = (String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                let ok = proc.terminationStatus == 0
                finish(ok, ok ? o : (e.isEmpty ? o : e), o, e)
            }
            do {
                try p.run()
            } catch {
                finish(false, linkText("Can't start persona-link: \(error.localizedDescription)"), "", "")
                return
            }
            if let stdin {
                do {
                    try Self.writeAll(input.fileHandleForWriting, Data(stdin.utf8))
                } catch {
                    // persona-link quit before reading stdin (broken pipe). Its termination handler may still win
                    // the race; if it does not, say the send failed rather than blaming the launch.
                    finish(false, linkText("Couldn't send data to persona-link: \(error.localizedDescription)"), "", "")
                }
            }
            try? input.fileHandleForWriting.close()
        }
    }

    /// GUI apps launched from the Finder already run with SIGPIPE ignored; a CLI launch (debugger, xctest) does
    /// not, where a broken-pipe write would kill the process. Install the GUI disposition once.
    nonisolated private static let installIgnoredSigpipe: Void = {
        Darwin.signal(SIGPIPE, SIG_IGN)
    }()

    /// Writes every byte to a pipe.
    ///
    /// `FileHandle.write` raises an uncatchable ObjC exception on EPIPE (with SIGPIPE ignored, the write fails
    /// with EPIPE rather than raising a signal), so go through `write(2)` and let a persona-link that exits before
    /// reading its stdin fail as a normal Swift error.
    nonisolated static func writeAll(_ handle: FileHandle, _ data: Data) throws {
        _ = installIgnoredSigpipe
        let fd = handle.fileDescriptor
        try data.withUnsafeBytes { raw in
            guard let base = raw.bindMemory(to: UInt8.self).baseAddress else { return }
            var sent = 0
            while sent < raw.count {
                let written = Darwin.write(fd, base.advanced(by: sent), raw.count - sent)
                if written < 0 {
                    if errno == EINTR { continue }
                    throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno))
                }
                sent += written
            }
        }
    }

    nonisolated static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        nonisolated(unsafe) let precise = ISO8601DateFormatter()
        precise.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        nonisolated(unsafe) let plain = ISO8601DateFormatter()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            // Go writes up to nanoseconds; ISO8601DateFormatter takes at most milliseconds.
            let trimmed = s.replacingOccurrences(of: #"(\.\d{3})\d+"#, with: "$1", options: .regularExpression)
            if let date = precise.date(from: trimmed) ?? plain.date(from: trimmed) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
        }
        return d
    }()
}

/// Text from this app's String Catalog (Resources/Localizable.xcstrings, en / zh-Hans / ja).
func linkText(_ key: String.LocalizationValue) -> String {
    String(localized: key, bundle: linkTextBundle)
}

/// Where `linkText` looks; tests point it at one language's `.lproj` to check a translation.
nonisolated(unsafe) var linkTextBundle: Bundle = .module

/// The language the App shows, for persona-link's own messages: en, zh-Hans or ja.
nonisolated let linkLanguage: String = Bundle.module.preferredLocalizations.first ?? "en"

/// A thread-safe one-shot gate: only the first claim wins.
///
/// A `Process` runs its `terminationHandler` on a background queue while a launch or stdin error throws on the
/// caller's queue, so code that must finish exactly once (a checked continuation) claims it through this gate.
final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var claimed = false

    /// Returns true for exactly one caller, ever; every later claim returns false.
    func claim() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        if claimed { return false }
        claimed = true
        return true
    }
}
