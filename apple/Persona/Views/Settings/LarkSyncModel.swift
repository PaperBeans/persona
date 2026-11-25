import Foundation
import Observation
import PersonaKit
import PersonaWire

/// Settings › Connectors › Lark Bot › "Daily Sync to Your Knowledge Base" (docs/extensions/bots.md "Reading as you:
/// daily sync", #15392): which Feishu / Lark accounts Persona reads every day, through which bot's app, and how each
/// one is doing. The same hub routes as the web's section (web/src/lib/lark-sync.ts), under /api/v1/bots/feishu/sync.
nonisolated struct LarkSyncView: Decodable, Equatable, Sendable {
    var available: Bool
    /// The local hour the daily run starts at.
    var scheduleHour: Int
    var accounts: [Account]
    var bots: [Bot]
    var bindings: [Binding]

    nonisolated struct Account: Decodable, Equatable, Sendable {
        var id: String
        var label: String?
        var brand: String?
        var name: String?
    }

    nonisolated struct Bot: Decodable, Equatable, Sendable, Identifiable {
        var botID: String
        var label: String?
        var appID: String?
        var domain: String?
        /// chat_and_sync (the chat bot) or sync_only.
        var role: String?
        var tier: String?
        /// ready | awaiting_approval | degraded; absent: nothing known.
        var approval: String?
        /// The chat bot of bots.feishu, listed first.
        var primary: Bool?
        var hasSecret: Bool?
        /// A registration (its QR step) is under way: asked again with the register route.
        var registering: Bool?
        var id: String { botID }

        enum CodingKeys: String, CodingKey {
            case label, domain, role, tier, approval, primary, registering
            case botID = "bot_id", appID = "app_id", hasSecret = "has_secret"
        }
    }

    nonisolated struct Binding: Decodable, Equatable, Sendable, Identifiable {
        var key: String
        var botID: String
        var accountID: String
        var label: String?
        var tier: String?
        var enabled: Bool
        /// ok | unauthorized | needs_reauth | rate_limited | scope_missing | failing | awaiting_approval.
        var health: String
        var error: String?
        var grantedScopes: [String]?
        var authorizedAt: String?
        /// Days left of the authorization; absent when unknown.
        var reauthDueDays: Int?
        var lastRunAt: String?
        var nextRunAt: String?
        var lastMessages: Int?
        var lastDocs: Int?
        var running: Bool?
        var id: String { key }

        enum CodingKeys: String, CodingKey {
            case key, label, tier, enabled, health, error, running
            case botID = "bot_id", accountID = "account_id", grantedScopes = "granted_scopes", authorizedAt = "authorized_at"
            case reauthDueDays = "reauth_due_days", lastRunAt = "last_run_at", nextRunAt = "next_run_at"
            case lastMessages = "last_messages", lastDocs = "last_docs"
        }
    }

    enum CodingKeys: String, CodingKey {
        case available, accounts, bots, bindings
        case scheduleHour = "schedule_hour"
    }

    init(available: Bool, scheduleHour: Int, accounts: [Account], bots: [Bot], bindings: [Binding]) {
        (self.available, self.scheduleHour, self.accounts, self.bots, self.bindings) = (available, scheduleHour, accounts, bots, bindings)
    }

    /// Lenient like the web's `asSyncView`: a missing list is empty, an hour out of range is 03:00.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        available = (try? c.decodeIfPresent(Bool.self, forKey: .available)) == true
        let hour = (try? c.decodeIfPresent(Int.self, forKey: .scheduleHour)) ?? 3
        scheduleHour = (0..<24).contains(hour) ? hour : 3
        accounts = (try? c.decodeIfPresent([Account].self, forKey: .accounts)) ?? []
        bots = try c.decodeIfPresent([Bot].self, forKey: .bots) ?? []
        bindings = try c.decodeIfPresent([Binding].self, forKey: .bindings) ?? []
    }

    /// What a bot's app may read as the owner (larksyncwire TierDocs / TierFull).
    static let tierDocs = "docs_standard"
    static let tierFull = "full_with_im"
    static let tiers = [tierDocs, tierFull]
    /// Re-authorizing is suggested once this many days or fewer are left of the platform's one-year authorization.
    static let reauthSoonDays = 14
    /// The path the open platform sends the owner back to; the hub accepts only this one.
    static let oauthReturnPath = "/lark-oauth"

    /// The bots in the order the page lists them: the chat bot first, then the sync bots as the hub sent them.
    var orderedBots: [Bot] { bots.filter { $0.primary == true } + bots.filter { $0.primary != true } }

    func bindings(of botID: String) -> [Binding] { bindings.filter { $0.botID == botID } }

    /// A binding's name: its own label, else its account's, else the account's name, else a numbered fallback.
    func title(_ b: Binding) -> String {
        let at = accounts.firstIndex { $0.id == b.accountID }
        let account = at.map { accounts[$0] }
        if let s = [b.label, account?.label, account?.name].compactMap({ $0 }).first(where: { !$0.isEmpty }) { return s }
        let n = at.map { $0 + 1 } ?? ((bindings.firstIndex { $0.key == b.key } ?? 0) + 1)
        return String(localized: "Account \(max(1, n))")
    }
}

/// How the hub lets the owner authorize: a browser redirect, a code on screen (device flow), or the redirect URL to add
/// by hand in the app's console first.
nonisolated struct LarkSyncAuthorize: Decodable, Equatable, Sendable {
    var mode: String
    var url: String?
    var prompt: ConnectorPrompt?
    var redirectURI: String?
    var consoleURL: String?

    enum CodingKeys: String, CodingKey {
        case mode, url, prompt
        case redirectURI = "redirect_uri", consoleURL = "console_url"
    }
}

/// The words, tones and small decisions of the section, kept apart from the view so tests can read them.
enum LarkSyncText {
    enum Tone: Equatable { case positive, caution, danger, accent, neutral }

    static func botTitle(_ b: LarkSyncView.Bot) -> String {
        if b.primary == true { return String(localized: "Chat Bot [Lark Sync]") }
        if let l = b.label, !l.isEmpty { return l }
        return String(localized: "Sync Bot [Lark Sync]")
    }

    /// The plain words and the tone of a binding's state; a running sync wins over the last run's outcome.
    static func health(_ b: LarkSyncView.Binding) -> (text: String, tone: Tone) {
        if b.running == true { return (String(localized: "Syncing Now [Lark Sync]"), .accent) }
        switch b.health {
        case "ok": return b.enabled ? (String(localized: "Up to Date [Lark Sync]"), .positive) : (String(localized: "Paused [Lark Sync]"), .neutral)
        case "unauthorized": return (String(localized: "Not Connected Yet [Lark Sync]"), .neutral)
        case "needs_reauth": return (String(localized: "Authorize Again [Lark Sync state]"), .danger)
        case "rate_limited": return (String(localized: "Continues Next Time [Lark Sync]"), .caution)
        case "scope_missing": return (String(localized: "Reauthorize to Add Missing Permissions [Lark Sync]"), .caution)
        case "awaiting_approval": return (String(localized: "Waiting for Admin Approval [Lark Sync]"), .caution)
        case "failing": return (String(localized: "Last Sync Failed [Lark Sync]"), .danger)
        default: return (String(localized: "Unknown [Lark Sync]"), .neutral)
        }
    }

    /// The chip for a bot's app, when it says something: waiting for the admin, or set up without some permissions.
    static func approval(_ b: LarkSyncView.Bot) -> (text: String, tone: Tone)? {
        switch b.approval ?? "" {
        case "awaiting_approval": (String(localized: "Waiting for Admin Approval [Lark Sync]"), .caution)
        case "degraded": (String(localized: "Some Permissions Missing [Lark Sync]"), .caution)
        default: nil
        }
    }

    static func tier(_ tier: String?) -> String {
        tier == LarkSyncView.tierFull ? String(localized: "Everything, Including Chat History [Lark Sync]")
            : String(localized: "Documents and Meeting Minutes [Lark Sync]")
    }

    static func tierHint(_ tier: String) -> String {
        tier == LarkSyncView.tierFull
            ? String(localized: "Also the chats you are in and their messages. Work accounts usually need an admin to approve this first.")
            : String(localized: "Your documents, sheets, wiki pages and meeting minutes. Work accounts may need an admin to approve this first.")
    }

    /// The binding has to be authorized (again) before it can sync.
    static func needsAuthorize(_ b: LarkSyncView.Binding) -> Bool { b.health == "needs_reauth" || b.health == "unauthorized" || b.health == "scope_missing" }

    /// Days left when the authorization ends soon enough to say so; nil otherwise.
    static func reauthSoon(_ b: LarkSyncView.Binding) -> Int? {
        guard let days = b.reauthDueDays, days <= LarkSyncView.reauthSoonDays, !needsAuthorize(b) else { return nil }
        return max(0, days)
    }

    /// What a bot's row says under its name.
    static func botHint(_ b: LarkSyncView.Bot, accounts: Int) -> String {
        if b.hasSecret == false {
            if b.primary == true { return String(localized: "Set up the chat bot above first.") }
            return b.registering == true ? String(localized: "Setup isn't finished yet. [Lark Sync]")
                : String(localized: "Setup wasn't finished. Remove this bot and add it again.")
        }
        return accounts > 0 ? tier(b.tier) : String(localized: "No account connected yet.")
    }

    /// "Synced 3 hours ago · 12 messages · 4 documents · Next: 03:17", only the parts the hub knows.
    static func statusLine(_ b: LarkSyncView.Binding, now: Date = .now) -> String {
        var parts: [String] = []
        if let last = date(b.lastRunAt) {
            parts.append(String(localized: "Synced \(Fmt.relative(last, now: now)) [Lark Sync]"))
            parts.append(String(localized: "Messages: \(b.lastMessages ?? 0) [Lark Sync]"))
            parts.append(String(localized: "Documents: \(b.lastDocs ?? 0) [Lark Sync]"))
        } else {
            parts.append(String(localized: "Not Synced Yet [Lark Sync]"))
        }
        if b.enabled, !needsAuthorize(b), let next = date(b.nextRunAt) {
            parts.append(String(localized: "Next: \(next.formatted(date: .omitted, time: .shortened)) [Lark Sync]"))
        }
        return parts.joined(separator: " · ")
    }

    /// "03:00" in this device's language and clock.
    static func hour(_ h: Int) -> String {
        let c = DateComponents(year: 2000, month: 1, day: 1, hour: h, minute: 0)
        return (Calendar.current.date(from: c) ?? .now).formatted(date: .omitted, time: .shortened)
    }

    /// The hub's RFC 3339 times, with or without fractions of a second.
    static func date(_ s: String?) -> Date? {
        guard let s, !s.isEmpty else { return nil }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s)
    }
}

/// The browser authorization's way back: the address the platform returns to (the hub's own `/lark-oauth`, which the
/// hub accepts), and what the return carries. The app opens the platform's page in its own web view and stops at that
/// address, so the hub's web page never loads: the app hands `state` and `code` to the hub itself.
enum LarkOAuthReturn: Equatable {
    case code(state: String, code: String)
    case declined(String)

    /// Where the platform sends the owner back: the hub's address the app talks to, under its sub-path if any.
    static func redirectURI(hub: URL) -> String {
        var s = hub.absoluteString
        if let q = s.firstIndex(where: { $0 == "?" || $0 == "#" }) { s = String(s[..<q]) }
        while s.hasSuffix("/") { s.removeLast() }
        return s + LarkSyncView.oauthReturnPath
    }

    /// The return in `url` when it is the redirect address (same scheme, host, port and path); nil for any other page.
    static func parse(_ url: URL, redirect: String) -> LarkOAuthReturn? {
        guard let want = URLComponents(string: redirect), let got = URLComponents(url: url, resolvingAgainstBaseURL: false),
              got.scheme?.lowercased() == want.scheme?.lowercased(), got.host?.lowercased() == want.host?.lowercased(),
              got.port == want.port, got.path == want.path else { return nil }
        let q = Dictionary((got.queryItems ?? []).map { ($0.name, $0.value ?? "") }, uniquingKeysWith: { a, _ in a })
        if let e = q["error"], !e.isEmpty { return .declined(e) }
        guard let code = q["code"], !code.isEmpty, let state = q["state"], !state.isEmpty else { return .declined("") }
        return .code(state: state, code: code)
    }
}

extension APIClient {
    private static let larkSync = "/api/v1/bots/feishu/sync"

    private func larkSyncCall(_ method: String, _ path: String, body: (any Encodable)? = nil) async throws -> LarkSyncView {
        try JSON.decoder.decode(LarkSyncView.self, from: await request(method, Self.larkSync + path, body: body, timeout: 40))
    }

    private func larkSyncPrompt(_ path: String, body: (any Encodable)? = nil) async throws -> ConnectorPrompt {
        try JSON.decoder.decode(ConnectorPrompt.self, from: await request("POST", Self.larkSync + path, body: body, timeout: 40))
    }

    private static func seg(_ s: String) -> String { s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(.init(charactersIn: "-._~"))) ?? s }

    func larkSyncView() async throws -> LarkSyncView { try await larkSyncCall("GET", "") }
    func setLarkSyncSchedule(_ hour: Int) async throws -> LarkSyncView { try await larkSyncCall("PUT", "", body: ["schedule_hour": hour]) }

    /// Starts creating a sync-only bot's app by scanning: the prompt carries the QR code.
    func createLarkSyncBot(label: String, tier: String, region: String) async throws -> (botID: String, prompt: ConnectorPrompt) {
        struct Answer: Decodable { var bot_id: String; var prompt: ConnectorPrompt }
        let a = try JSON.decoder.decode(Answer.self, from: await request("POST", Self.larkSync + "/bots",
                                                                         body: ["label": label, "tier": tier, "region": region], timeout: 40))
        return (a.bot_id, a.prompt)
    }

    func registerLarkSyncBot(_ id: String, step: String) async throws -> ConnectorPrompt {
        try await larkSyncPrompt("/bots/\(Self.seg(id))/register", body: ["step": step])
    }

    /// Adds a tier's read permissions to an existing app (the chat bot's), by scanning.
    func addLarkSyncScopes(_ id: String, tier: String) async throws -> ConnectorPrompt {
        try await larkSyncPrompt("/bots/\(Self.seg(id))/scopes", body: ["tier": tier])
    }

    func removeLarkSyncBot(_ id: String) async throws -> LarkSyncView { try await larkSyncCall("DELETE", "/bots/\(Self.seg(id))") }

    func authorizeLarkSyncBot(_ id: String, redirectURI: String, tier: String) async throws -> LarkSyncAuthorize {
        try JSON.decoder.decode(LarkSyncAuthorize.self, from: await request("POST", Self.larkSync + "/bots/\(Self.seg(id))/authorize",
                                                                             body: ["redirect_uri": redirectURI, "tier": tier], timeout: 40))
    }

    func pollLarkSyncAuthorize(_ id: String) async throws -> ConnectorPrompt { try await larkSyncPrompt("/bots/\(Self.seg(id))/authorize/poll") }

    /// Hands the platform's code back to the hub (what the web's return page does).
    func finishLarkSyncOAuth(state: String, code: String) async throws -> LarkSyncView {
        try await larkSyncCall("POST", "/oauth", body: ["state": state, "code": code])
    }

    func setLarkSyncBinding(_ key: String, enabled: Bool) async throws -> LarkSyncView {
        try await larkSyncCall("PUT", "/bindings/\(Self.seg(key))", body: ["enabled": enabled])
    }

    func removeLarkSyncBinding(_ key: String) async throws -> LarkSyncView { try await larkSyncCall("DELETE", "/bindings/\(Self.seg(key))") }
    func runLarkSyncBinding(_ key: String) async throws -> LarkSyncView { try await larkSyncCall("POST", "/bindings/\(Self.seg(key))/run") }
}

/// The platform's authorization page open in the app's web view, and the address it returns to.
struct LarkOAuthBrowser: Identifiable, Equatable {
    let id = UUID()
    var url: URL
    var redirect: String
}

/// The section's state: the hub's view, the change in flight, and where adding or authorizing an account is.
@Observable final class LarkSyncModel {
    struct Deps {
        var api: () -> APIClient?
        var toast: (Toast) -> Void

        static var live: Deps {
            let model = AppModel.shared
            return Deps(api: { model.api }, toast: { model.toast = $0 })
        }
    }

    /// Where adding or authorizing is: choosing, step 1 (an app that may read as the owner), or step 2 (allowing it).
    enum Flow: Equatable {
        case choose
        case register(bot: String, tier: String, prompt: ConnectorPrompt, scopes: Bool)
        case authorize(bot: String, tier: String, again: Bool, answer: LarkSyncAuthorize?, prompt: ConnectorPrompt?, error: String?)
    }

    /// nil: nothing to show (an older hub, one without the Lark module, or not loaded yet).
    private(set) var view: LarkSyncView?
    private(set) var loadError: String?
    private(set) var error: String?
    /// What a change in flight is about: "schedule", "bot:<id>", a binding's key, "add", "authorize".
    private(set) var busy = ""
    var flow: Flow?
    /// A browser authorization open in the app's web view.
    var browser: LarkOAuthBrowser?
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var capturedAPI: APIClient?

    init(deps: Deps = .live) { self.deps = deps }

    private func api() -> APIClient? {
        if capturedAPI == nil { capturedAPI = deps.api()?.currentAssistantSettings() }
        return capturedAPI
    }

    /// The address the platform returns to; nil without a hub.
    var redirectURI: String? { api().map { LarkOAuthReturn.redirectURI(hub: $0.config.url) } }

    var running: Bool { view?.bindings.contains { $0.running == true } == true }
    var primary: LarkSyncView.Bot? { view?.bots.first { $0.primary == true } }

    /// An older hub has no such route (404, 405, 501): the section hides. Anything else is a real failure.
    static func isAbsent(_ e: Error) -> Bool { [404, 405, 501].contains((e as? APIError)?.status ?? 0) }

    func load() async {
        guard let api = api() else { return }
        do {
            let v = try await api.larkSyncView()
            view = v.available ? v : nil
            loadError = nil
        } catch {
            if Self.isAbsent(error) { view = nil; loadError = nil } else if view == nil {
                loadError = error.shown.map { String(localized: "Couldn't load the daily sync: \($0)") }
            }
        }
    }

    /// A change whose answer is the new view. `what` names it in the error.
    @discardableResult
    private func change(_ what: String, _ key: String, done: String? = nil, _ call: (APIClient) async throws -> LarkSyncView) async -> Bool {
        guard let api = api() else { return false }
        busy = key
        defer { busy = "" }
        do {
            let v = try await call(api)
            view = v.available ? v : view
            error = nil
            if let done { deps.toast(Toast(done, style: .success)) }
            return true
        } catch {
            self.error = error.shown.map { String(localized: "\(what): \($0)", comment: "What failed, and why") }
            return false
        }
    }

    func setSchedule(_ hour: Int) async {
        await change(String(localized: "Couldn't save"), "schedule") { try await $0.setLarkSyncSchedule(hour) }
    }

    func setEnabled(_ b: LarkSyncView.Binding, _ on: Bool) async {
        await change(on ? String(localized: "Couldn't turn on syncing") : String(localized: "Couldn't turn off syncing"), b.key) {
            try await $0.setLarkSyncBinding(b.key, enabled: on)
        }
    }

    func run(_ b: LarkSyncView.Binding) async {
        await change(String(localized: "Couldn't start syncing"), b.key, done: String(localized: "Syncing started")) { try await $0.runLarkSyncBinding(b.key) }
    }

    func remove(_ b: LarkSyncView.Binding) async {
        await change(String(localized: "Couldn't remove the account"), b.key) { try await $0.removeLarkSyncBinding(b.key) }
    }

    func removeBot(_ b: LarkSyncView.Bot) async {
        await change(String(localized: "Couldn't remove the bot"), "bot:\(b.botID)") { try await $0.removeLarkSyncBot(b.botID) }
    }

    // MARK: Adding and authorizing

    func startAuthorize(bot: String, tier: String, again: Bool = false) async {
        flow = .authorize(bot: bot, tier: tier, again: again, answer: nil, prompt: nil, error: nil)
        await prepareAuthorize()
    }

    /// Step 1: a new app by scanning (`new`), or the chat bot's app given the permission.
    func startAdd(new: Bool, label: String, tier: String, region: String) async {
        guard let api = api() else { return }
        busy = "add"
        defer { busy = "" }
        do {
            let started: (bot: String, prompt: ConnectorPrompt)
            if new {
                let r = try await api.createLarkSyncBot(label: label.trimmingCharacters(in: .whitespaces), tier: tier, region: region)
                started = (r.botID, r.prompt)
            } else if let p = primary {
                started = (p.botID, try await api.addLarkSyncScopes(p.botID, tier: tier))
            } else { return }
            let (bot, prompt) = started
            error = nil
            if prompt.done == true {
                await startAuthorize(bot: bot, tier: tier)
            } else {
                flow = .register(bot: bot, tier: tier, prompt: prompt, scopes: !new)
            }
        } catch {
            let what = String(localized: "Couldn't continue the setup")
            self.error = error.shown.map { String(localized: "\(what): \($0)", comment: "What failed, and why") }
        }
    }

    /// A sync bot whose setup was left half way: its QR step, asked again.
    func resume(_ b: LarkSyncView.Bot) async {
        guard let api = api() else { return }
        busy = "bot:\(b.botID)"
        defer { busy = "" }
        do {
            let prompt = try await api.registerLarkSyncBot(b.botID, step: "register")
            let tier = b.tier ?? LarkSyncView.tierDocs
            error = nil
            if prompt.done == true {
                await startAuthorize(bot: b.botID, tier: tier)
                await load()
            } else {
                flow = .register(bot: b.botID, tier: tier, prompt: prompt, scopes: false)
            }
        } catch {
            let what = String(localized: "Couldn't continue the setup")
            self.error = error.shown.map { String(localized: "\(what): \($0)", comment: "What failed, and why") }
        }
    }

    /// Step 1, asked again while its QR code is on screen. True once it is done (and step 2 has begun) or it failed for good.
    func pollRegister() async -> Bool {
        guard case let .register(bot, tier, prompt, scopes) = flow, let api = api() else { return true }
        guard let next = try? await api.registerLarkSyncBot(bot, step: prompt.step ?? "register") else { return false }
        guard case .register = flow else { return true }
        if next.done == true {
            await startAuthorize(bot: bot, tier: tier)
            return true
        }
        flow = .register(bot: bot, tier: tier, prompt: next, scopes: scopes)
        return next.error?.isEmpty == false
    }

    /// A failed registration starts again from the choice; a new bot that never got its app is not kept.
    func restartRegistration() async {
        guard case let .register(bot, _, _, scopes) = flow else { return }
        flow = .choose
        if scopes { return }
        if let v = try? await api()?.removeLarkSyncBot(bot) { view = v.available ? v : view }
    }

    /// Step 2: the hub prepares the authorization; its answer says which way it goes.
    func prepareAuthorize() async {
        guard case let .authorize(bot, tier, again, _, _, _) = flow, let api = api(), let redirect = redirectURI else { return }
        busy = "authorize"
        defer { busy = "" }
        do {
            let answer = try await api.authorizeLarkSyncBot(bot, redirectURI: redirect, tier: tier)
            guard case .authorize(bot, _, _, _, _, _) = flow else { return }
            flow = .authorize(bot: bot, tier: tier, again: again, answer: answer, prompt: answer.prompt, error: nil)
        } catch {
            guard case .authorize(bot, _, _, _, _, _) = flow else { return }
            flow = .authorize(bot: bot, tier: tier, again: again, answer: nil, prompt: nil, error: error.shown ?? error.localizedDescription)
        }
    }

    /// The browser way: the platform's page in the app's own web view.
    func openBrowser() {
        guard case let .authorize(_, _, _, answer?, _, _) = flow, let s = answer.url, let url = URL(string: s),
              ["https", "http"].contains(url.scheme?.lowercased() ?? ""), let redirect = answer.redirectURI ?? redirectURI else { return }
        browser = LarkOAuthBrowser(url: url, redirect: redirect)
    }

    /// The web view reached the return address.
    func finishBrowser(_ r: LarkOAuthReturn) async {
        browser = nil
        guard case let .authorize(bot, tier, again, answer, prompt, _) = flow else { return }
        switch r {
        case .declined:
            flow = .authorize(bot: bot, tier: tier, again: again, answer: answer, prompt: prompt,
                              error: String(localized: "Persona wasn't allowed to read as you. You can try again."))
        case let .code(state, code):
            guard let api = api() else { return }
            busy = "authorize"
            defer { busy = "" }
            do {
                let v = try await api.finishLarkSyncOAuth(state: state, code: code)
                view = v.available ? v : view
                finished()
            } catch {
                flow = .authorize(bot: bot, tier: tier, again: again, answer: answer, prompt: prompt, error: error.shown ?? error.localizedDescription)
            }
        }
    }

    /// The device flow, asked again while its code is on screen. True once done or failed for good.
    func pollDevice() async -> Bool {
        guard case let .authorize(bot, tier, again, answer, prompt, _) = flow, let api = api() else { return true }
        guard let p = try? await api.pollLarkSyncAuthorize(bot) else { return false }
        guard case .authorize(bot, _, _, _, _, _) = flow else { return true }
        if p.done == true {
            finished()
            await load()
            return true
        }
        if let e = p.error, !e.isEmpty {
            var shown = prompt
            shown?.error = e
            flow = .authorize(bot: bot, tier: tier, again: again, answer: answer, prompt: shown, error: nil)
            return true
        }
        return false
    }

    private func finished() {
        flow = nil
        deps.toast(Toast(String(localized: "Account connected. The first sync starts soon."), style: .success))
    }

    func cancelFlow() {
        flow = nil
        browser = nil
    }
}
