import Foundation
import Testing
@testable import Persona

/// A question card stays where it is, waiting, while its answer is on its way, and keeps its picks and an error with Retry
/// when the answer fails (#6279).
@MainActor
struct QuestionSendingTests {
    private let folder = FileManager.default.temporaryDirectory.appending(path: "question-sending-\(UUID().uuidString)")
    private let path = "/api/v1/interactions/ix_1/resolve"
    private let answer = Resolution(answers: ["用哪个库？": "pgx"])

    private func card(kind: InteractionKind = .question) -> Interaction {
        let questions = kind == .question ? [Question(question: "用哪个库？", header: nil, multiSelect: false, options: [QuestionOption(label: "pgx")])] : nil
        return Interaction(id: "ix_1", machineID: "m", sessionID: "", kind: kind, toolName: kind == .permission ? "Bash" : nil, questions: questions)
    }

    private func model(_ hub: FakeHub, _ ix: Interaction) -> AppModel {
        let model = AppModel(stateFolder: folder)
        model.forgetRestoredForTests()
        model.apiForTests = hub.client
        model.replaceInteractions("", [ix])
        return model
    }

    private final class Flag: @unchecked Sendable { var on = true }

    // MARK: The attempts

    @Test func aSecondAnswerWhileOneIsOnItsWayIsNotSent() {
        var a = AnswerAttempts()
        #expect(a.begin("ix", answer) != nil)
        #expect(a.progress("ix") == .sending)
        #expect(a.begin("ix", answer) == nil)
        #expect(a.begin("ix", Resolution(answers: ["用哪个库？": "sqlc"])) == nil)
    }

    @Test func theSameAnswerAgainKeepsItsOpAndAnotherOneIsNew() {
        var a = AnswerAttempts()
        let op = a.begin("ix", answer)
        a.fail("ix", "offline")
        #expect(a.progress("ix") == .failed("offline") && a.retryable("ix") == answer)
        #expect(a.begin("ix", answer) == op && a.progress("ix") == .sending)
        a.fail("ix", "offline")
        let other = a.begin("ix", Resolution(answers: ["用哪个库？": "sqlc"]))
        #expect(other != nil && other != op)
    }

    @Test func aFinishedCardLeavesNothing() {
        var a = AnswerAttempts()
        _ = a.begin("ix", answer)
        a.finish("ix")
        #expect(a.progress("ix") == nil && a.retryable("ix") == nil)
    }

    // MARK: The model

    @Test func theCardStaysSendingUntilTheHubHasTheAnswer() async {
        let hub = FakeHub()
        let ix = card()
        let model = model(hub, ix)
        var during: (card: Bool, progress: AnswerAttempts.Progress?)?
        hub.on("POST", path) { _ in
            during = (model.pendingInteractions.count == 1, model.answerProgress(ix.uid))
            return Data(#"{"ok":true}"#.utf8)
        }
        await model.resolve(ix, answer)
        #expect(during?.card == true && during?.progress == .sending)
        #expect(model.pendingInteractions.isEmpty && model.answerProgress(ix.uid) == nil)
    }

    @Test func aFailedAnswerKeepsTheCardWithItsErrorAndRetrySendsTheSameOp() async {
        let hub = FakeHub()
        let ix = card()
        let model = model(hub, ix)
        let failing = Flag()
        hub.on("POST", path) { _ in
            if failing.on { throw APIError(status: 400, message: "bad request") }
            return Data(#"{"ok":true}"#.utf8)
        }
        await model.resolve(ix, answer)
        #expect(model.pendingInteractions.count == 1)
        #expect(model.answerFailure(ix.uid) != nil && model.answerProgress(ix.uid) != .sending)
        #expect(model.toast == nil, "the card says it, not a toast")
        failing.on = false
        await model.retryAnswer(ix)
        #expect(model.pendingInteractions.isEmpty && model.answerProgress(ix.uid) == nil)
        let ops = hub.sent("POST", path).map { $0.headers[OpRetry.header] }
        #expect(ops.count == 2 && ops[0] != nil && ops[0] == ops[1])
    }

    @Test func aSecondTapWhileSendingSendsNothing() async {
        let hub = FakeHub()
        hub.on("POST", path, json: #"{"ok":true}"#)
        let ix = card()
        let model = model(hub, ix)
        // A hub that takes its time, so that the second tap comes while the first answer is on its way.
        var slow = hub.client
        let inner = slow.transport!
        slow.transport = { req in
            try await Task.sleep(for: .milliseconds(200))
            return try await inner(req)
        }
        model.apiForTests = slow
        async let first: Void = model.resolve(ix, answer)
        try? await Task.sleep(for: .milliseconds(50))
        #expect(model.answerProgress(ix.uid) == .sending && model.pendingInteractions.count == 1)
        await model.resolve(ix, answer)
        await first
        #expect(hub.sent("POST", path).count == 1)
        #expect(model.pendingInteractions.isEmpty)
    }

    @Test func theHubEndingTheCardWhileSendingTakesItAway() async {
        let hub = FakeHub()
        let ix = card()
        let model = model(hub, ix)
        hub.on("POST", path) { _ in
            model.handle(.interactionResolved("ix_1", assistant: nil), group: "")
            throw APIError(status: 409, message: "prompt_changed")
        }
        await model.resolve(ix, answer)
        #expect(model.pendingInteractions.isEmpty && model.answerProgress(ix.uid) == nil)
    }

    @Test func otherCardsStillGoAtOnce() async {
        let hub = FakeHub()
        let ix = card(kind: .permission)
        let model = model(hub, ix)
        var during: Int?
        hub.on("POST", path) { _ in
            during = model.pendingInteractions.count
            return Data(#"{"ok":true}"#.utf8)
        }
        await model.resolve(ix, Resolution(allow: true))
        #expect(during == 0)
    }
}
