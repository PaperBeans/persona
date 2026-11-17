#if os(iOS)
import AVFoundation
import Foundation
import LiveCommunicationKit
import os

// Between `PersonaCalls` and the system (#9290): LiveCommunicationKit on a device (`LCKCallSystem`), a stand-in on the
// Simulator (`SimulatedCallSystem`: it has no call service and ends every conversation in about 80 ms), a fake in the
// tests. LiveCommunicationKit's action objects cannot be made by anyone else, so each one reaches `PersonaCalls` as a
// `CallSystemAction` with its own fulfil and fail.

/// An action the system asks the app to carry out.
nonisolated enum CallSystemAction: Equatable, Sendable {
    case start, join, end
    case mute(Bool)
    case pause(Bool)
    case other
}

/// An action the app asks the system for.
nonisolated enum CallRequest: Equatable, Sendable {
    case join, end
    case mute(Bool)
    case pause(Bool)
}

/// Where the system's conversation is (LiveCommunicationKit's `Conversation.State`).
nonisolated enum CallSystemState: Equatable, Sendable {
    case idle, joining, joined, paused, leaving, left
}

/// What the system tells the app, on the main actor.
@MainActor protocol CallSystemHandler: AnyObject {
    func system(perform action: CallSystemAction, for id: UUID, fulfil: @escaping @MainActor () -> Void, fail: @escaping @MainActor () -> Void)
    func system(changed id: UUID, state: CallSystemState)
    func system(timedOut action: CallSystemAction, for id: UUID)
    func systemActivatedAudio()
    func systemDeactivatedAudio()
    func systemReset()
}

/// The system's side of the app's calls: one per app, made the first time a call needs it (never at launch, #9264).
@MainActor protocol CallSystem: AnyObject {
    var handler: (any CallSystemHandler)? { get set }
    /// Asks the system to start a call to Persona.
    func start(_ id: UUID) async throws
    /// An incoming call (an urgent call's ring), shown with `name`.
    func reportIncoming(_ id: UUID, name: String) async throws
    func perform(_ request: CallRequest, for id: UUID) async throws
    func report(_ report: CallReport, for id: UUID)
    /// The conversation is in the system's list (reports before that are dropped).
    func knows(_ id: UUID) -> Bool
    /// Activates the audio session itself, once, where the system did not: true when it did.
    func activateAudio() async -> Bool
}

/// The name the system shows and Recents keeps for Persona: the handle's value, not its display name (the system
/// ignores that, FB24933340), and what a Recents redial gives back. Readable and stable; any value redials the
/// current hub's main conversation.
nonisolated enum CallHandle {
    static let persona = "Persona" // i18n-ignore: the product's name, shown as is everywhere
}

/// An error from the system, by its domain and code. LiveCommunicationKit's errors keep CallKit's domain strings
/// (`com.apple.CallKit.error.requesttransaction`…), sometimes under `NSUnderlyingErrorKey`: they are compared as strings,
/// so the app never imports CallKit (App Store China rejects a binary that links it).
nonisolated struct CallSystemError: Equatable, Sendable {
    var domain: String
    var code: Int

    init(domain: String, code: Int) {
        self.domain = domain
        self.code = code
    }

    /// The innermost error of the system's call service, or the error itself.
    init(_ error: any Error) {
        var e = error as NSError
        while !e.domain.hasPrefix(Self.callService), let inner = e.userInfo[NSUnderlyingErrorKey] as? NSError { e = inner }
        self.init(domain: e.domain, code: e.code)
    }

    static let callService = "com.apple.CallKit.error"
    static let requestTransaction = callService + ".requesttransaction"

    /// The request came while the app was still coming to the front (code 6, `invalidAction`, seen on device): once more
    /// a moment later.
    var isTooEarly: Bool { domain == Self.requestTransaction && code == 6 }
    /// Every call group is taken (code 7).
    var isAtCapacity: Bool { domain == Self.requestTransaction && code == 7 }

    var description: String { "\(domain) \(code)" }
}

/// Runs `body` on the main actor: at once on the main thread, else as soon as the main queue can, in the order asked.
nonisolated enum CallMain {
    static func run(_ body: @escaping @MainActor @Sendable () -> Void) {
        if Thread.isMainThread {
            MainActor.assumeIsolated { body() }
        } else {
            DispatchQueue.main.async { MainActor.assumeIsolated { body() } }
        }
    }
}

// MARK: LiveCommunicationKit

/// The app's one `ConversationManager` (one per process: a second one takes every action from the first). Its
/// configuration is read-only, so it is decided here once (docs: #9290 "LCK configuration").
@MainActor final class LCKCallSystem: CallSystem {
    private static let log = Logger(subsystem: "persona", category: "call")
    weak var handler: (any CallSystemHandler)?
    /// Used only from the main actor (its async calls run on its own queue): held unchecked rather than sent between regions.
    nonisolated(unsafe) private let manager: ConversationManager
    private let delegate = PersonaCallsLCKDelegate()

    /// Two groups so an urgent call during a call to Persona offers Hold & Accept (with one the system offers only End &
    /// Accept and Decline, and a Start at capacity fails); calls in Recents for the redial; one handle type, `.generic`.
    static var configuration: ConversationManager.Configuration {
        ConversationManager.Configuration(ringtoneName: nil, iconTemplateImageData: nil, maximumConversationGroups: 2,
                                          maximumConversationsPerConversationGroup: 1, includesConversationInRecents: true,
                                          supportsVideo: false, supportedHandleTypes: [.generic])
    }

    init() {
        manager = ConversationManager(configuration: Self.configuration)
        manager.delegate = delegate
        delegate.system = self
    }

    private static var persona: Handle { Handle(type: .generic, value: CallHandle.persona, displayName: CallHandle.persona) }

    func start(_ id: UUID) async throws {
        nonisolated(unsafe) let lck = manager
        try await lck.perform([StartConversationAction(conversationUUID: id, handles: [Self.persona], isVideo: false)])
    }

    func reportIncoming(_ id: UUID, name: String) async throws {
        // The call's id stays out of the handle: it showed as "call_…" and would land in Recents. The title goes only in
        // the display name, which the system may show one day and Recents never keeps.
        let update = Conversation.Update(localMember: Handle(type: .generic, value: "owner"),
                                         members: [Handle(type: .generic, value: CallHandle.persona, displayName: name)],
                                         capabilities: [.pausing])
        nonisolated(unsafe) let lck = manager
        try await lck.reportNewIncomingConversation(uuid: id, update: update)
    }

    func perform(_ request: CallRequest, for id: UUID) async throws {
        let action: ConversationAction = switch request {
        case .join: JoinConversationAction(conversationUUID: id)
        case .end: EndConversationAction(conversationUUID: id)
        case .mute(let on): MuteConversationAction(conversationUUID: id, isMuted: on)
        case .pause(let on): PauseConversationAction(conversationUUID: id, isPaused: on)
        }
        nonisolated(unsafe) let lck = manager
        nonisolated(unsafe) let one = action
        try await lck.perform([one])
    }

    func report(_ report: CallReport, for id: UUID) {
        guard let conversation = manager.conversations.first(where: { $0.uuid == id }) else {
            Self.log.notice("call \(id.uuidString, privacy: .public): the system no longer has the conversation, report dropped")
            return
        }
        let event: Conversation.Event = switch report {
        case .startedConnecting(let at): .conversationStartedConnecting(at)
        case .connected(let at): .conversationConnected(at)
        case .pausable: .conversationUpdated(Conversation.Update(capabilities: [.pausing]))
        case .ended(let at, let why): .conversationEnded(at, Self.reason(why))
        }
        manager.reportConversationEvent(event, for: conversation)
    }

    func knows(_ id: UUID) -> Bool { manager.conversations.contains { $0.uuid == id } }

    func activateAudio() async -> Bool {
        await Task.detached {
            do {
                try AVAudioSession.sharedInstance().setActive(true)
                return true
            } catch {
                Logger(subsystem: "persona", category: "call").notice("call: activating the audio session failed: \(error.localizedDescription, privacy: .public)")
                return false
            }
        }.value
    }

    private static func reason(_ r: CallEndedReport) -> Conversation.EndedReason {
        switch r {
        case .failed: .failed
        case .remoteEnded: .remoteEnded
        case .unanswered: .unanswered
        case .joinedElsewhere: .joinedElsewhere
        case .declinedElsewhere: .declinedElsewhere
        }
    }

    // MARK: From the delegate, on the main actor

    fileprivate func received(_ action: ConversationAction) {
        let id = action.conversationUUID
        nonisolated(unsafe) let action = action
        let kind: CallSystemAction
        let fulfil: @MainActor () -> Void
        switch action {
        case let a as StartConversationAction:
            kind = .start
            fulfil = { a.fulfill(dateStarted: .now) }
        case let a as JoinConversationAction:
            kind = .join
            fulfil = { a.fulfill(dateConnected: .now) }
        case let a as EndConversationAction:
            kind = .end
            fulfil = { a.fulfill(dateEnded: .now) }
        case let a as MuteConversationAction:
            kind = .mute(a.isMuted)
            fulfil = { a.fulfill() }
        case let a as PauseConversationAction:
            kind = .pause(a.isPaused)
            fulfil = { a.fulfill() }
        default:
            kind = .other
            fulfil = { action.fulfill() }
        }
        Self.log.notice("call \(id.uuidString, privacy: .public): the system asks \(String(describing: kind), privacy: .public)")
        guard let handler else { return action.fail() }
        handler.system(perform: kind, for: id, fulfil: fulfil, fail: { action.fail() })
    }

    fileprivate func changed(_ id: UUID, _ lckState: Conversation.State) {
        let state: CallSystemState = switch lckState {
        case .idle: .idle
        case .joining: .joining
        case .joined: .joined
        case .paused: .paused
        case .leaving: .leaving
        case .left: .left
        @unknown default: .idle
        }
        Self.log.notice("call \(id.uuidString, privacy: .public): the system's conversation is \(String(describing: state), privacy: .public)")
        handler?.system(changed: id, state: state)
    }

    fileprivate func timedOut(_ action: ConversationAction) {
        let kind: CallSystemAction = switch action {
        case is StartConversationAction: .start
        case is JoinConversationAction: .join
        case is EndConversationAction: .end
        case let a as MuteConversationAction: .mute(a.isMuted)
        case let a as PauseConversationAction: .pause(a.isPaused)
        default: .other
        }
        Self.log.notice("call \(action.conversationUUID.uuidString, privacy: .public): the system gave up waiting for \(String(describing: kind), privacy: .public)")
        handler?.system(timedOut: kind, for: action.conversationUUID)
    }

    fileprivate func activated() {
        Self.log.notice("call: the system activated the audio session")
        handler?.systemActivatedAudio()
    }

    fileprivate func deactivated() {
        Self.log.notice("call: the system deactivated the audio session")
        handler?.systemDeactivatedAudio()
    }

    fileprivate func reset() {
        Self.log.notice("call: the conversation manager reset")
        handler?.systemReset()
    }
}

/// The conversation manager's delegate. LiveCommunicationKit calls it on its own queue, so every method is
/// `nonisolated` and hops to the main actor itself: in this main-actor-by-default target an unmarked method is the
/// main actor's, and the runtime's isolation check stops the app when the system calls it from another queue (the
/// TestFlight crash at launch of #8734's first build). Callbacks keep their order (`CallMain`).
nonisolated final class PersonaCallsLCKDelegate: ConversationManagerDelegate, @unchecked Sendable {
    /// Set once, before the manager has the delegate; read on the main actor only.
    @MainActor weak var system: LCKCallSystem?

    nonisolated func conversationManager(_ manager: ConversationManager, conversationChanged conversation: Conversation) {
        // Only Sendable values cross to the main actor: the conversation object need not be Sendable in every SDK.
        let id = conversation.uuid, lckState = conversation.state
        CallMain.run { [weak self] in self?.system?.changed(id, lckState) }
    }

    nonisolated func conversationManagerDidBegin(_ manager: ConversationManager) {}

    nonisolated func conversationManagerDidReset(_ manager: ConversationManager) {
        CallMain.run { [weak self] in self?.system?.reset() }
    }

    nonisolated func conversationManager(_ manager: ConversationManager, perform action: ConversationAction) {
        nonisolated(unsafe) let action = action
        CallMain.run { [weak self] in
            guard let system = self?.system else { return action.fail() }
            system.received(action)
        }
    }

    nonisolated func conversationManager(_ manager: ConversationManager, timedOutPerforming action: ConversationAction) {
        nonisolated(unsafe) let action = action
        CallMain.run { [weak self] in self?.system?.timedOut(action) }
    }

    nonisolated func conversationManager(_ manager: ConversationManager, didActivate audioSession: AVAudioSession) {
        CallMain.run { [weak self] in self?.system?.activated() }
    }

    nonisolated func conversationManager(_ manager: ConversationManager, didDeactivate audioSession: AVAudioSession) {
        CallMain.run { [weak self] in self?.system?.deactivated() }
    }
}

// MARK: The Simulator

#if targetEnvironment(simulator)
/// The Simulator has no call service: LiveCommunicationKit ends every conversation there in about 80 ms. This stands in
/// for it: it carries each request out at once, as the system would hand it back (`perform`), and activates and
/// deactivates the audio session itself where the system would (`didActivate` / `didDeactivate`). `-showSystemCallService
/// YES` takes the real one instead, to see what it does.
@MainActor final class SimulatedCallSystem: CallSystem {
    private static let log = Logger(subsystem: "persona", category: "call")
    weak var handler: (any CallSystemHandler)?
    private var known: Set<UUID> = []
    /// The call whose audio the session is: the one not paused.
    private var active: UUID?

    func start(_ id: UUID) async throws {
        known.insert(id)
        Self.log.notice("call \(id.uuidString, privacy: .public): the stand-in starts it")
        await Task.yield() // `perform` returns before the delegate hears the action, as on a device
        deliver(.start, id)
        await activate(id)
    }

    func reportIncoming(_ id: UUID, name: String) async throws {
        known.insert(id)
        Self.log.notice("call \(id.uuidString, privacy: .public): the stand-in rings (silently)")
    }

    func perform(_ request: CallRequest, for id: UUID) async throws {
        guard known.contains(id) else { throw CallSystemStandInError.unknownCall }
        switch request {
        case .join:
            deliver(.join, id)
            await activate(id)
        case .end:
            deliver(.end, id)
            known.remove(id)
            handler?.system(changed: id, state: .left)
            if active == id { await deactivate() }
        case .mute(let on):
            deliver(.mute(on), id)
        case .pause(let on):
            deliver(.pause(on), id)
            if on, active == id { await deactivate() } else if !on { await activate(id) }
        }
    }

    func report(_ report: CallReport, for id: UUID) {
        guard known.contains(id) else { return }
        if case .ended = report {
            known.remove(id)
            if active == id { Task { await deactivate() } }
        }
    }

    func knows(_ id: UUID) -> Bool { known.contains(id) }

    func activateAudio() async -> Bool { await Self.setActive(true) }

    private func deliver(_ action: CallSystemAction, _ id: UUID) {
        handler?.system(perform: action, for: id, fulfil: {}, fail: {
            Self.log.notice("call \(id.uuidString, privacy: .public): the app failed \(String(describing: action), privacy: .public)")
        })
    }

    private func activate(_ id: UUID) async {
        guard known.contains(id), active != id else { return }
        active = id
        guard await Self.setActive(true), active == id else { return }
        handler?.systemActivatedAudio()
    }

    private func deactivate() async {
        active = nil
        _ = await Self.setActive(false)
        handler?.systemDeactivatedAudio()
    }

    /// Off the main actor: activating and deactivating the session block for tens of milliseconds.
    private static func setActive(_ on: Bool) async -> Bool {
        await Task.detached {
            do {
                try AVAudioSession.sharedInstance().setActive(on, options: on ? [] : .notifyOthersOnDeactivation)
                return true
            } catch {
                Logger(subsystem: "persona", category: "call").notice("call: the stand-in could not \(on ? "activate" : "deactivate", privacy: .public) the audio session: \(error.localizedDescription, privacy: .public)")
                return false
            }
        }.value
    }
}

nonisolated enum CallSystemStandInError: Error {
    case unknownCall
}
#endif
#endif
