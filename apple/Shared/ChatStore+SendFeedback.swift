import Foundation

extension ChatStore {
    /// A restored sent outbox entry already has an acceptance receipt. Its creation time is a conservative bound.
    func restoreSendFeedbackIfNeeded(now: Date = .now) {
        guard sendFeedback == nil, let message = pending.last(where: { $0.state == .sent }),
              let at = message.createdAt, now.timeIntervalSince(at) < ChatSendFeedback.acceptanceWindow else { return }
        beginSendFeedback(message, now: at)
        settleSendFeedback(message.id, state: .sent, now: at)
    }

    func beginSendFeedback(_ message: PendingMessage, now: Date = .now) {
        guard sendFeedback?.id != message.id || sendFeedback?.phase != .sending else { return }
        sendFeedback = ChatSendFeedback(id: message.id, startedAt: now, afterSequence: items.last?.seq ?? 0)
    }

    /// Update even after the pending bubble was replaced by its transcript echo.
    func settleSendFeedback(_ id: UUID, state: PendingMessage.State, now: Date = .now) {
        guard var feedback = sendFeedback, feedback.id == id else { return }
        if state == .sent { feedback.accept(id, at: now) }
        else if state == .failed || state == .waiting { feedback.finish(id) }
        if feedback != sendFeedback { sendFeedback = feedback }
    }

    /// Only fresh session changes terminate a local attempt; an old idle/failure snapshot is not its result.
    func updateSendFeedback(_ session: Session, previous: Session?) {
        // Runtime snapshots do not decide whether an in-flight request was accepted.
        guard var feedback = sendFeedback, feedback.phase == .processing else { return }
        let terminal = (session.status == .blocked || session.status == .offline) && session.status != previous?.status
        let failed = session.failureReason != nil &&
            (session.failureReason != previous?.failureReason || session.stateSince != previous?.stateSince)
        let ended = feedback.echoedSequence != nil && previous?.status.isActive == true && !session.status.isActive
        if terminal || failed || ended {
            feedback.finish(feedback.id)
            sendFeedback = feedback
        }
    }

    #if DEBUG
    func traceSendFeedback(_ phase: ChatSendFeedback.Phase) {
        guard let feedback = sendFeedback else { return }
        print(String(format: "SEND_FEEDBACK_DRAW phase=%@ seconds=%.3f", String(describing: phase),
                     Date.now.timeIntervalSince(feedback.startedAt)))
    }
    #endif
}
