import AVFoundation
import Foundation
import os

/// The single owner of direct `AVAudioSession` calls in the Apple core (#13849, part of #13431 and #13432).
///
/// Every audio-session user across iOS, visionOS, watchOS and feature modules routes category configuration,
/// activation, route/input queries and session notifications through `AudioSessionBroker.shared`. Active leases are
/// recorded with the explicit ranking **call > voice conversation > meeting recording > playback**. Enforcing
/// preemptive arbitration across callers is a separate follow-up; today every call forwards directly to
/// `AVAudioSession` with no behavioral change.
///
/// Lives in `Shared/` with no `PersonaModuleAPI` import so watchOS and extensions compile it without linking the
/// module registry; `Persona/App/Modules.swift` bridges `ModuleRegistry.audioLeases` into `setDeclaredLeases(_:)`.
nonisolated final class AudioSessionBroker: @unchecked Sendable {
    static let shared = AudioSessionBroker()

    /// What may own the shared audio session, ranked highest first:
    /// `call > voiceConversation > meetingRecording > playback`.
    nonisolated enum LeaseKind: String, CaseIterable, Hashable, Comparable, Sendable {
        case call = "call"
        case voiceConversation = "voice_conversation"
        case meetingRecording = "meeting_recording"
        case playback = "playback"

        /// Priority rank: `call` (4) > `voiceConversation` (3) > `meetingRecording` (2) > `playback` (1).
        var rank: Int {
            switch self {
            case .playback: 1
            case .meetingRecording: 2
            case .voiceConversation: 3
            case .call: 4
            }
        }

        static func < (lhs: LeaseKind, rhs: LeaseKind) -> Bool {
            lhs.rank < rhs.rank
        }

        /// True when this lease kind outranks `other`.
        func outranks(_ other: LeaseKind) -> Bool {
            rank > other.rank
        }
    }

    /// Snapshot of one active audio-session lease recorded by the broker.
    nonisolated struct LeaseRecord: Identifiable, Hashable, Sendable {
        let token: UUID
        /// Logical lease id (e.g. `"call.system"`, `"voice.conversation"`, `"recording.capture"`,
        /// `"core.dictation"`, `"screen-share.pip"`, `"recording.replay"`, `"recording.speaker"`).
        let id: String
        /// Owning feature module id when declared by a module, or nil for core callers.
        let module: String?
        let kind: LeaseKind
        /// Monotonic acquisition sequence number used to order equal-rank leases deterministically.
        let sequence: UInt64
    }

    /// Snapshot of a lease declared by an active module through `ModuleSlot.audioLeases`.
    nonisolated struct DeclaredLease: Identifiable, Hashable, Sendable {
        let id: String
        let module: String
        let kind: LeaseKind
    }

    /// Handle for an active lease; releasing it drops only the broker record (no `AVAudioSession` side effect).
    nonisolated final class Lease: @unchecked Sendable {
        let record: LeaseRecord
        private weak var broker: AudioSessionBroker?
        private let released = OSAllocatedUnfairLock(initialState: false)

        init(record: LeaseRecord, broker: AudioSessionBroker) {
            self.record = record
            self.broker = broker
        }

        /// Releases this lease in the broker. Safe to call more than once.
        func release() {
            let shouldRelease = released.withLock { already -> Bool in
                if already { return false }
                already = true
                return true
            }
            if shouldRelease {
                broker?.release(token: record.token)
            }
        }

        deinit {
            release()
        }
    }

    /// Parsed audio-session interruption payload delivered to observers.
    nonisolated enum Interruption: Equatable, Sendable {
        case began(reason: AVAudioSession.InterruptionReason?)
        case ended(shouldResume: Bool)
    }

    /// Underlying `AVAudioSession` operations, injectable for unit tests.
    nonisolated struct Backend: Sendable {
        var setCategory: @Sendable (AVAudioSession.Category, AVAudioSession.Mode, AVAudioSession.CategoryOptions) throws -> Void
        var setActive: @Sendable (Bool, AVAudioSession.SetActiveOptions) throws -> Void
        var category: @Sendable () -> AVAudioSession.Category
        var currentRoute: @Sendable () -> AVAudioSessionRouteDescription
        var isOtherAudioPlaying: @Sendable () -> Bool
        #if os(iOS) || os(visionOS)
        var availableInputs: @Sendable () -> [AVAudioSessionPortDescription]
        var setPreferredInput: @Sendable (AVAudioSessionPortDescription?) throws -> Void
        var isInputAvailable: @Sendable () -> Bool
        var outputLatency: @Sendable () -> TimeInterval
        var ioBufferDuration: @Sendable () -> TimeInterval
        var overrideOutputAudioPort: @Sendable (AVAudioSession.PortOverride) throws -> Void
        #endif

        static var system: Backend {
            #if os(iOS) || os(visionOS)
            Backend(
                setCategory: { try AVAudioSession.sharedInstance().setCategory($0, mode: $1, options: $2) },
                setActive: { try AVAudioSession.sharedInstance().setActive($0, options: $1) },
                category: { AVAudioSession.sharedInstance().category },
                currentRoute: { AVAudioSession.sharedInstance().currentRoute },
                isOtherAudioPlaying: { AVAudioSession.sharedInstance().isOtherAudioPlaying },
                availableInputs: { AVAudioSession.sharedInstance().availableInputs ?? [] },
                setPreferredInput: { try AVAudioSession.sharedInstance().setPreferredInput($0) },
                isInputAvailable: { AVAudioSession.sharedInstance().isInputAvailable },
                outputLatency: { AVAudioSession.sharedInstance().outputLatency },
                ioBufferDuration: { AVAudioSession.sharedInstance().ioBufferDuration },
                overrideOutputAudioPort: { try AVAudioSession.sharedInstance().overrideOutputAudioPort($0) }
            )
            #else
            Backend(
                setCategory: { try AVAudioSession.sharedInstance().setCategory($0, mode: $1, options: $2) },
                setActive: { try AVAudioSession.sharedInstance().setActive($0, options: $1) },
                category: { AVAudioSession.sharedInstance().category },
                currentRoute: { AVAudioSession.sharedInstance().currentRoute },
                isOtherAudioPlaying: { AVAudioSession.sharedInstance().isOtherAudioPlaying }
            )
            #endif
        }
    }

    /// Plain value state held under `OSAllocatedUnfairLock` (no closures in lock state, docs/lessons/ios.md #12624).
    private nonisolated struct State: Sendable {
        var nextSequence: UInt64 = 0
        var active: [LeaseRecord] = []
        var declared: [DeclaredLease] = []
    }

    private let backend: Backend
    private let notificationCenter: NotificationCenter
    private let state = OSAllocatedUnfairLock(initialState: State())

    init(backend: Backend = .system, notificationCenter: NotificationCenter = .default) {
        self.backend = backend
        self.notificationCenter = notificationCenter
    }

    // MARK: - Leases

    /// Records an active audio-session lease and returns a handle that releases it.
    /// Does not reject or preempt other leases yet; enforcement is a separate follow-up (#13849).
    func acquire(id: String, module: String? = nil, kind: LeaseKind) -> Lease {
        let record = state.withLock { state -> LeaseRecord in
            state.nextSequence &+= 1
            let entry = LeaseRecord(token: UUID(), id: id, module: module, kind: kind, sequence: state.nextSequence)
            state.active.append(entry)
            return entry
        }
        return Lease(record: record, broker: self)
    }

    func release(token: UUID) {
        state.withLock { state in
            state.active.removeAll { $0.token == token }
        }
    }

    /// Active leases sorted highest priority first (`call > voiceConversation > meetingRecording > playback`),
    /// with earlier acquisitions first within the same kind.
    var activeLeases: [LeaseRecord] {
        state.withLock { Self.ranked($0.active) }
    }

    /// The highest-ranked active lease, or nil when no lease is held.
    var highestActiveLease: LeaseRecord? {
        activeLeases.first
    }

    /// The kind of the highest-ranked active lease, or nil when none is held.
    var highestActiveKind: LeaseKind? {
        highestActiveLease?.kind
    }

    /// True when at least one active lease has `kind`.
    func hasActiveLease(of kind: LeaseKind) -> Bool {
        state.withLock { state in state.active.contains { $0.kind == kind } }
    }

    /// True when at least one active lease has `id`.
    func hasActiveLease(id: String) -> Bool {
        state.withLock { state in state.active.contains { $0.id == id } }
    }

    /// True when no active lease is held or `kind` strictly outranks the highest active lease.
    func outranksActive(_ kind: LeaseKind) -> Bool {
        guard let highest = highestActiveKind else { return true }
        return kind.outranks(highest)
    }

    /// Active leases that `kind` strictly outranks, sorted highest priority first.
    func wouldPreempt(for kind: LeaseKind) -> [LeaseRecord] {
        activeLeases.filter { kind.outranks($0.kind) }
    }

    /// Updates the set of module audio leases declared by the current `ModuleRegistry`.
    func setDeclaredLeases(_ leases: [DeclaredLease]) {
        state.withLock { $0.declared = leases }
    }

    /// Audio leases declared by currently active feature modules (`ModuleSlot.audioLeases`).
    var declaredLeases: [DeclaredLease] {
        state.withLock { $0.declared }
    }

    /// Looks up a declared module audio lease by id.
    func declaredLease(id: String) -> DeclaredLease? {
        state.withLock { state in state.declared.first { $0.id == id } }
    }

    private static func ranked(_ leases: [LeaseRecord]) -> [LeaseRecord] {
        leases.sorted { lhs, rhs in
            if lhs.kind != rhs.kind { return lhs.kind > rhs.kind }
            return lhs.sequence < rhs.sequence
        }
    }

    // MARK: - AVAudioSession forwarding (no behavioral change)

    func setCategory(_ category: AVAudioSession.Category, mode: AVAudioSession.Mode = .default,
                     options: AVAudioSession.CategoryOptions = []) throws {
        try backend.setCategory(category, mode, options)
    }

    func setActive(_ active: Bool, options: AVAudioSession.SetActiveOptions = []) throws {
        try backend.setActive(active, options)
    }

    var category: AVAudioSession.Category {
        backend.category()
    }

    var currentRoute: AVAudioSessionRouteDescription {
        backend.currentRoute()
    }

    var isOtherAudioPlaying: Bool {
        backend.isOtherAudioPlaying()
    }

    #if os(iOS) || os(visionOS)
    var availableInputs: [AVAudioSessionPortDescription] {
        backend.availableInputs()
    }

    func setPreferredInput(_ inPort: AVAudioSessionPortDescription?) throws {
        try backend.setPreferredInput(inPort)
    }

    var isInputAvailable: Bool {
        backend.isInputAvailable()
    }

    var outputLatency: TimeInterval {
        backend.outputLatency()
    }

    var ioBufferDuration: TimeInterval {
        backend.ioBufferDuration()
    }

    func overrideOutputAudioPort(_ portOverride: AVAudioSession.PortOverride) throws {
        try backend.overrideOutputAudioPort(portOverride)
    }
    #endif

    /// Requests microphone recording permission (`AVAudioApplication`, available on iOS 17+, watchOS 10+, visionOS 1+).
    func requestRecordPermission() async -> Bool {
        await AVAudioApplication.requestRecordPermission()
    }

    // MARK: - Notifications & lifecycle observation

    func observeRouteChanges(queue: OperationQueue? = nil,
                             _ handler: @escaping @Sendable (AVAudioSession.RouteChangeReason?) -> Void) -> NSObjectProtocol {
        notificationCenter.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil, queue: queue) { @Sendable note in
            let reason = (note.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt)
                .flatMap(AVAudioSession.RouteChangeReason.init)
            handler(reason)
        }
    }

    func observeInterruptions(queue: OperationQueue? = .main,
                              _ handler: @escaping @Sendable (Interruption) -> Void) -> NSObjectProtocol {
        notificationCenter.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: queue) { @Sendable note in
            guard let event = Self.parseInterruption(note.userInfo) else { return }
            handler(event)
        }
    }

    func observeMediaServicesReset(queue: OperationQueue? = .main,
                                   _ handler: @escaping @Sendable () -> Void) -> NSObjectProtocol {
        notificationCenter.addObserver(forName: AVAudioSession.mediaServicesWereResetNotification, object: nil, queue: queue) { @Sendable _ in
            handler()
        }
    }

    func removeObserver(_ token: NSObjectProtocol) {
        notificationCenter.removeObserver(token)
    }

    func routeChangePublisher() -> NotificationCenter.Publisher {
        notificationCenter.publisher(for: AVAudioSession.routeChangeNotification)
    }

    func routeChangeNotifications() -> NotificationCenter.Notifications {
        notificationCenter.notifications(named: AVAudioSession.routeChangeNotification)
    }

    /// Observes audio-session lifecycle events for capture, using typed NotificationCenter messages on OS 27+
    /// and `AVAudioSession.interruptionNotification` on older runtimes (`RecordingSessionObserver`).
    @MainActor
    func observeRecordingSessionLifecycle(_ handle: @escaping @MainActor (RecordingSessionEvent) -> Void) -> [Any] {
        var tokens: [Any] = []
        #if compiler(>=6.4)
        if #available(iOS 27.0, watchOS 27.0, visionOS 27.0, *) {
            // `of: AVAudioSession.self`: from any session object, as `object: nil` was.
            tokens.append(notificationCenter.addObserver(of: AVAudioSession.self, for: .didBecomeInactive) { message in
                switch message.deactivationResult {
                case .systemInterruption(let context): handle(.interrupted(reason: context.reason))
                case .appDeactivated: handle(.deactivatedByApp)
                @unknown default: handle(.interrupted(reason: nil))
                }
            })
            tokens.append(notificationCenter.addObserver(of: AVAudioSession.self, for: .resumptionRecommendation) { message in
                handle(.interruptionEnded(shouldResume: message.recommendation == .shouldResume))
            })
            tokens.append(notificationCenter.addObserver(of: AVAudioSession.self, for: .didBecomeActive) { _ in handle(.activated) })
            return tokens
        }
        #endif
        tokens.append(observeInterruptions(queue: .main) { @Sendable event in
            MainActor.assumeIsolated {
                switch event {
                case .began(let reason): handle(.interrupted(reason: reason))
                case .ended(let shouldResume): handle(.interruptionEnded(shouldResume: shouldResume))
                }
            }
        })
        return tokens
    }

    static func parseInterruption(_ userInfo: [AnyHashable: Any]?) -> Interruption? {
        let info = userInfo ?? [:]
        guard let type = (info[AVAudioSessionInterruptionTypeKey] as? UInt).flatMap(AVAudioSession.InterruptionType.init) else {
            return nil
        }
        switch type {
        case .began:
            let reason = (info[AVAudioSessionInterruptionReasonKey] as? UInt).flatMap(AVAudioSession.InterruptionReason.init)
            return .began(reason: reason)
        case .ended:
            let options = AVAudioSession.InterruptionOptions(rawValue: info[AVAudioSessionInterruptionOptionKey] as? UInt ?? 0)
            return .ended(shouldResume: options.contains(.shouldResume))
        @unknown default:
            return nil
        }
    }
}
