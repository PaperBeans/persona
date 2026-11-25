import Foundation

/// This client's send attempt, independent of transcript echo and delayed runtime snapshots.
/// A trusted hub `message_send_state` receipt moves Sending to Processing early; the send API's HTTP answer remains
/// the fallback. Neither means delivery is complete or that the model has produced output.
nonisolated struct ChatSendFeedback: Equatable, Sendable {
    enum Phase: Equatable, Sendable {
        case sending, processing, finished

        var label: String {
            switch self {
            case .sending: String(localized: "Sending…")
            case .processing: String(localized: "Processing…")
            case .finished: ""
            }
        }
    }

    /// What moved the attempt from Sending to Processing: a trusted hub `message_send_state` receipt or the HTTP fallback.
    enum Acceptance: Equatable, Sendable {
        case receipt, http
    }

    let id: UUID
    let startedAt: Date
    let afterSequence: Int
    private(set) var phase: Phase = .sending
    private(set) var acceptedAt: Date?
    private(set) var acceptance: Acceptance?
    private(set) var echoedSequence: Int?

    /// Keep the old bounded wait, but start it at acceptance, not while a slow request is still sending.
    static let acceptanceWindow: TimeInterval = 60
    var deadline: Date? { acceptedAt?.addingTimeInterval(Self.acceptanceWindow) }

    /// Send → Processing latency, tracked separately from HTTP delivery and execution.
    var processingLatency: TimeInterval? { acceptedAt?.timeIntervalSince(startedAt) }

    mutating func accept(_ id: UUID, at: Date) {
        guard id == self.id, phase == .sending else { return }
        phase = .processing
        acceptedAt = at
        acceptance = .http
    }

    /// Apply a trusted `message_send_state` receipt for this send attempt.
    /// Receipt is transport feedback only: `delivered` does not finish execution, and a late receipt cannot revive a finished attempt.
    mutating func receive(_ id: UUID, state: SendReceipt.State, at: Date) {
        guard id == self.id, phase != .finished else { return }
        switch state {
        case .processing, .delivered:
            guard phase == .sending else { return }
            phase = .processing
            acceptedAt = at
            acceptance = .receipt
        case .failed, .unknown, .kept:
            phase = .finished
        }
    }

    mutating func finish(_ id: UUID) {
        guard id == self.id else { return }
        phase = .finished
    }

    mutating func echo(_ item: Item) {
        if item.seq > afterSequence, PendingMessage.tied(item, to: id) == true { echoedSequence = item.seq }
    }

    mutating func echo(sequence: Int) { echoedSequence = sequence }

    func belongs(to turn: Turn) -> Bool {
        guard let echoedSequence else { return false }
        return turn.users.contains { $0.userInput?.seq == echoedSequence }
    }

    /// A thought or tool is progress, not completion. A reply after this request's own echo ends the fallback.
    /// Sequence order avoids comparing the phone's clock with the hub's clock.
    func answered(by item: Item) -> Bool {
        guard let echoedSequence else { return false }
        return item.seq > echoedSequence && item.role == "assistant" && item.kind == .text
    }

    func visible(now: Date) -> Phase? {
        if phase == .sending { return .sending }
        guard phase == .processing else { return nil }
        if let deadline, now >= deadline { return nil }
        return .processing
    }
}
