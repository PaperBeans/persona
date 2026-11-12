import Foundation
import UserNotifications
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// Registers for APNs and tells the hub where to push (interactions, finished turns). The iPhone and the Mac app the
/// same way, as kind `iphone` / `mac`: same bundle id, so the same APNs topic, and both through the relay for a hub
/// without the app's key. While the Mac app is connected the hub leaves the agents' alerts to it (MacAttentionWatcher
/// posts them); a Mac never gets the phone's wake pushes.
final class PushRegistrar {
    static let shared = PushRegistrar()

    /// What the registrar asks of the system; a test passes its own.
    struct System {
        /// Paired with a hub that takes pushes (not the demo, not simulator automation).
        var paired: @MainActor () -> Bool
        /// Asks for permission, with the system's prompt the first time.
        var request: @MainActor () async -> Bool
        /// Whether notifications are allowed now, without asking (nil: not asked yet).
        var allowed: @MainActor () async -> Bool?
        /// Registers with APNs; the token comes back in `didRegister(token:)`.
        var register: @MainActor () -> Void
    }

    private let system: System
    private let health: PushHealthStore
    /// Registered with APNs in this process. A denial at launch or pairing leaves it false until Settings allows
    /// notifications and the app comes back (`foreground()`).
    private(set) var registered = false
    /// The last ask or check: the next one waits for it, so a prompt still open is never answered twice.
    private var last: Task<Void, Never>?

    init(system: System = .live, health: PushHealthStore = .shared) {
        self.system = system
        self.health = health
    }

    /// Asks for permission and registers if it is given: at launch, after pairing and on a sign-out that leaves a hub.
    /// Registers each time, so a newly paired hub learns this device's token.
    @discardableResult
    func registerIfPossible() -> Task<Void, Never>? {
        guard system.paired() else { return nil }
        return queue { [self] in
            // Asked here, not when the first notification is due: a push that comes while the app is closed shows
            // only if this was allowed before.
            let granted = await system.request()
            health.update { $0.notificationsAllowed = granted }
            if granted { register() }
        }
    }

    /// The app came back to the front (#1861). Notifications allowed in Settings after a denial take effect now: this
    /// registers, which tells every hub this device's token, as a launch does. Before, nothing registered until the app
    /// was quit and opened again, so the hub had no token for it. Never prompts: a permission not asked yet is left to
    /// the launch's or the pairing's request.
    @discardableResult
    func foreground() -> Task<Void, Never>? {
        guard !registered, system.paired() else { return nil }
        return queue { [self] in
            guard !registered, let allowed = await system.allowed() else { return }
            health.update { $0.notificationsAllowed = allowed }
            if allowed { register() }
        }
    }

    private func queue(_ work: @escaping @MainActor () async -> Void) -> Task<Void, Never> {
        let previous = last
        let task = Task {
            await previous?.value
            await work()
        }
        last = task
        return task
    }

    private func register() {
        registered = true
        system.register()
    }

    /// Registers with every group: at its first hub, the one that issued the credential (docs/archive/operations/plan-hub-mesh.md 2.6: the
    /// group's other hubs pass their pushes to it, one push per device), each group sealing to a key of its own
    /// (docs/protocol/multi-hub.md「Push and notifications」).
    func didRegister(token: Data) {
        let hex = token.map { String(format: "%02x", $0) }.joined()
        let registry = HubStore.load()
        let clients = registry.groups.compactMap { g in registry.primary(g).map { (group: g, api: APIClient(config: $0.config, group: g)) } }
            .filter { !$0.api.config.isDemo }
        guard !clients.isEmpty else { return }
        #if DEBUG
        let env = "sandbox"
        #else
        let env = "production"
        #endif
        #if os(macOS)
        let kind = "mac"
        let features: [String]? = nil
        #else
        let kind = "iphone"
        let features: [String]? = Self.features(RequestClientMetadata.current)
        #endif
        let base = APIClient.DeviceBody(deviceID: Self.deviceID, name: UIDevice.current.name, kind: kind, apnsToken: hex,
                                        apnsEnv: env, topic: Bundle.main.bundleIdentifier, features: features)
        Task {
            // A hub without the app's APNs key pushes through the relay, sealed to this group's key (docs/operations/push-relay.md);
            // the notification service extension opens it. The hub decides: with its own key it ignores these.
            var grant: String?
            var outcome = PushHealth.Relay.notConfigured
            if let relay = PushRelay.url, let topic = base.topic {
                do {
                    grant = try await PushRelay.grant(relay: relay, token: hex, env: env, topic: topic)
                    outcome = .registered
                } catch {
                    PushRelay.log.error("relay registration: \(error.localizedDescription, privacy: .public)")
                    outcome = PushRelay.outcome(of: error)
                }
            }
            let relayError = PushHealth(relay: outcome).relayError
            await PushHealthStore.shared.update { $0.apnsError = nil; $0.relay = outcome }
            for c in clients {
                var body = base
                if let grant, let relay = PushRelay.url, let key = PushCrypto.ensureKey(group: c.group) {
                    body.relayGrant = grant
                    body.relayURL = relay.absoluteString
                    body.pushKey = PushCrypto.publicKey(key)
                } else {
                    body.relayError = relayError
                }
                do {
                    let route = try await c.api.register(device: body) // nil: an older hub, which does not say
                    await PushHealthStore.shared.update { $0.routes[c.group] = route }
                } catch {}
            }
        }
    }

    /// APNs gave no token (no network, a build without the push entitlement): Settings › Notifications says so.
    func didFail(_ error: any Error) {
        let text = error.localizedDescription
        Task { @MainActor in PushHealthStore.shared.update { $0.apnsError = text } }
    }

    /// What this app takes in pushes beyond the first ones (`features`, docs/protocol/protocol.md devices): withdrawals
    /// (AppDelegate removes the notifications they name, #4349), and MCP forms and links (`INTERACTION_ELICITATION`,
    /// #2433) where `client_hello` declares them, the iOS app alone (`LiveConnection.helloFeatures`): the hub pushes
    /// those cards only to such a phone, and waits for it while no app that shows them is open. An older hub drops the name.
    nonisolated static func features(_ client: RequestClientMetadata) -> [String] {
        ["withdraw"] + LiveConnection.helloFeatures(client).filter { $0 == "elicitation" }
    }

    /// This device's id at the hub: its push token and its notification settings hang off it.
    static var deviceID: String {
        if let id = UserDefaults.standard.string(forKey: "deviceID") { return id }
        let id = UUID().uuidString
        UserDefaults.standard.set(id, forKey: "deviceID")
        return id
    }
}

extension PushRegistrar.System {
    static let live = PushRegistrar.System(
        paired: {
            guard AppModel.shared.isPaired, !AppModel.shared.isDemo else { return false }
            #if DEBUG
            // Simulator and snapshot automation: no permission alert covering the screenshots.
            if UserDefaults.standard.string(forKey: "pairURL") != nil { return false }
            #endif
            return true
        },
        request: {
            (try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge, .timeSensitive])) ?? false
        },
        allowed: { await UNUserNotificationCenter.current().notificationSettings().authorizationStatus.allowsNotifications },
        register: {
            #if os(macOS)
            NSApplication.shared.registerForRemoteNotifications()
            #else
            UIApplication.shared.registerForRemoteNotifications()
            #endif
        })
}

extension UNAuthorizationStatus {
    /// Notifications allowed by the system's switch (provisional and an App Clip's ephemeral count); nil: not asked yet.
    nonisolated var allowsNotifications: Bool? {
        switch self {
        case .notDetermined: return nil
        case .authorized, .provisional: return true
        #if os(iOS) || os(visionOS)
        case .ephemeral: return true
        #endif
        default: return false
        }
    }
}
