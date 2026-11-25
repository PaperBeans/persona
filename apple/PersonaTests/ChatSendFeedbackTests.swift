import Foundation
import Testing
@testable import Persona
import PersonaWire

@MainActor
@Suite(.serialized)
struct ChatSendFeedbackTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)
    private let key = SessionKey(machineID: "example-computer", sessionID: "feedback")

    private func item(_ seq: Int, role: String, kind: ItemKind = .text, id: UUID? = nil) -> Item {
        var item = Item(id: "\(seq)#0", seq: seq, ts: now, role: role, kind: kind, text: "example",
                        toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil)
        item.clientMsgID = id?.uuidString
        return item
    }

    private func session(status: String, failure: String? = nil) throws -> Session {
        var fields: [String: Any] = ["machine_id": "example-computer", "session_id": "feedback",
                                     "status": status, "state": status]
        if let failure { fields["failure_reason"] = failure }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    @Test func sendingIsImmediateAndDoesNotExpireWhileTheRequestRuns() {
        let feedback = ChatSendFeedback(id: UUID(), startedAt: now, afterSequence: 2)
        #expect(feedback.visible(now: now) == .sending)
        #expect(feedback.visible(now: now.addingTimeInterval(90)) == .sending)
        #expect(feedback.deadline == nil)
    }

    @Test func acceptanceDoesNotNeedRuntimeOrOutputAndStartsItsOwnDeadline() {
        let id = UUID()
        var feedback = ChatSendFeedback(id: id, startedAt: now.addingTimeInterval(-90), afterSequence: 2)
        feedback.accept(id, at: now)
        #expect(feedback.visible(now: now) == .processing)
        #expect(feedback.deadline == now.addingTimeInterval(60))
        #expect(feedback.visible(now: now.addingTimeInterval(60)) == nil)
    }

    @Test func oldResultsCannotReplaceANewerSend() {
        let old = UUID(), newest = UUID()
        var feedback = ChatSendFeedback(id: newest, startedAt: now, afterSequence: 2)
        feedback.accept(old, at: now)
        feedback.finish(old)
        #expect(feedback.phase == .sending)
        feedback.accept(newest, at: now)
        #expect(feedback.phase == .processing)
    }

    @Test func echoBeforeAcknowledgementKeepsTheAcceptanceBoundary() {
        let id = UUID()
        var feedback = ChatSendFeedback(id: id, startedAt: now, afterSequence: 2)
        feedback.echo(item(3, role: "user", id: id))
        #expect(feedback.phase == .sending)
        feedback.accept(id, at: now)
        #expect(feedback.phase == .processing)
        #expect(!feedback.answered(by: item(4, role: "assistant", kind: .thinking)))
        #expect(!feedback.answered(by: item(4, role: "assistant", kind: .toolUse)))
        #expect(feedback.answered(by: item(5, role: "assistant")))
    }

    @Test func lateAcknowledgementCannotRestartAFinishedTurn() {
        let id = UUID()
        var feedback = ChatSendFeedback(id: id, startedAt: now, afterSequence: 2)
        feedback.finish(id)
        feedback.accept(id, at: now)
        #expect(feedback.visible(now: now) == nil)
    }

    @Test func inFlightSnapshotsCannotCancelSendingOrItsLaterAcceptance() throws {
        let store = ChatStore(key: key)
        let pending = PendingMessage(text: "example", images: [])
        store.beginSendFeedback(pending)
        store.updateSendFeedback(try session(status: "blocked"), previous: try session(status: "idle"))
        #expect(store.sendFeedback?.phase == .sending)
        store.settleSendFeedback(pending.id, state: .sent)
        #expect(store.sendFeedback?.phase == .processing)
    }

    @Test func staleSnapshotsDoNotSuppressAcceptanceButFreshFailuresDo() throws {
        let store = ChatStore(key: key)
        let pending = PendingMessage(text: "example", images: [])
        let old = try session(status: "idle", failure: "previous failure")
        store.beginSendFeedback(pending)
        store.updateSendFeedback(old, previous: old)
        store.settleSendFeedback(pending.id, state: .sent)
        #expect(store.sendFeedback?.phase == .processing)
        store.updateSendFeedback(try session(status: "offline"), previous: old)
        #expect(store.sendFeedback?.phase == .finished)
    }

    @Test(arguments: [PendingMessage.State.failed, .waiting])
    func refusalOrNetworkWaitEndsTheOptimisticActivity(_ state: PendingMessage.State) {
        let store = ChatStore(key: key)
        let pending = PendingMessage(text: "example", images: [])
        store.beginSendFeedback(pending)
        store.settleSendFeedback(pending.id, state: state)
        #expect(store.sendFeedback?.phase == .finished)
        store.beginSendFeedback(pending)
        #expect(store.sendFeedback?.phase == .sending)
    }

    @Test func restoredAcceptedMessagesKeepProcessingWithoutAnotherRequest() {
        let store = ChatStore(key: key)
        var accepted = PendingMessage(text: "example", images: [])
        accepted.state = .sent
        accepted.createdAt = .now
        store.pending = [accepted]
        #expect(store.sendFeedback?.phase == .processing)
        #expect(store.sendFeedback?.id == accepted.id)
    }

    @Test func acknowledgementStillUpdatesActivityAfterEchoRemovedTheOutboxEntry() {
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "send-feedback-\(UUID().uuidString)"))
        model.forgetRestoredForTests()
        let store = model.chat(key)
        let pending = PendingMessage(text: "example", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)
        store.merge([item(3, role: "user", id: pending.id)])
        #expect(model.settleSend(key, pending.id, .sent) == nil)
        #expect(store.sendFeedback?.phase == .processing)
        store.merge([item(4, role: "assistant")])
        model.settleSend(key, pending.id, .sent)
        #expect(store.sendFeedback?.phase == .finished)
    }

    @Test func storeRetainsFeedbackWhenEchoRemovesThePendingBubble() {
        let store = ChatStore(key: key)
        let pending = PendingMessage(text: "example", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)
        store.merge([item(3, role: "user", id: pending.id)])
        #expect(store.pending.isEmpty)
        store.settleSendFeedback(pending.id, state: .sent)
        #expect(store.sendFeedback?.phase == .processing)
        store.merge([item(4, role: "assistant")])
        #expect(store.sendFeedback?.phase == .finished)
        store.settleSendFeedback(pending.id, state: .sent)
        #expect(store.sendFeedback?.phase == .finished)
    }

    // MARK: Hub message_send_state receipts

    private static let trustingHello = #"{"type":"hello","features":["sessions_list"],"message_send_state":1,"todo_send_state":1}"#
    private static let legacyHello = #"{"type":"hello","features":["message_send_state"]}"#

    private func receiptFrame(_ state: String, id: String, machine: String = "example-computer", session: String = "feedback") -> Data {
        Data(#"{"type":"message_send_state","machine_id":"\#(machine)","session_id":"\#(session)","client_msg_id":"\#(id)","state":"\#(state)","at":"2026-11-23T01:02:03Z"}"#.utf8)
    }

    @MainActor
    private final class PendingHTTP {
        private var entered: CheckedContinuation<Void, Never>?
        private var finish: CheckedContinuation<Data, Error>?
        private var hasEntered = false

        func handle(_ req: LocalRequest) async throws -> Data {
            hasEntered = true
            entered?.resume()
            entered = nil
            return try await withCheckedThrowingContinuation { finish = $0 }
        }

        func waitUntilSent() async {
            if hasEntered { return }
            await withCheckedContinuation { entered = $0 }
        }

        func respond(_ data: Data = Data(#"{"ok":true}"#.utf8)) {
            finish?.resume(returning: data)
            finish = nil
        }

        func fail(_ error: Error) {
            finish?.resume(throwing: error)
            finish = nil
        }
    }

    private func connectedApp(hub: FakeHub, sessionKey: SessionKey? = nil) throws -> (AppModel, SyncSocket) {
        let k = sessionKey ?? key
        let transport = try #require(hub.client.transport)
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "send-feedback-receipt-\(UUID().uuidString)"))
        model.forgetRestoredForTests()
        let socket = SyncSocket()
        model.linkHooksForTests = (transport: transport, socket: { socket })
        model.connectOnly(.demo)
        let row = try session(status: "idle")
        model.applySessions([MachineRef(group: k.group, id: k.machineID): [row]])
        return (model, socket)
    }

    @Test func trustedReceiptMovesToProcessingBeforeHttpReturns() async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let (model, socket) = try connectedApp(hub: hub)
        defer { withExtendedLifetime(model) {} }
        socket.deliver?(Data(Self.trustingHello.utf8))
        #expect(model.live.receiptTrust.messages)

        let store = model.chat(key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)
        #expect(store.sendFeedback?.phase == .sending)

        let task = Task { await model.sendPending(key, pending) }
        await http.waitUntilSent()
        #expect(store.sendFeedback?.phase == .sending)

        // Hub sends lowercase client_msg_id on the trusted socket before HTTP responds.
        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString.lowercased()))
        #expect(store.sendFeedback?.phase == .processing)
        #expect(store.sendFeedback?.acceptance == .receipt)
        let receiptAcceptedAt = try #require(store.sendFeedback?.acceptedAt)
        let processingLatency = try #require(store.sendFeedback?.processingLatency)
        #expect(processingLatency >= 0)
        #expect(store.pending.first?.state == .sending, "receipt is not durable delivery")
        #expect(model.session(key)?.status == .idle, "receipt does not alter execution state")

        http.respond()
        let outcome = await task.value
        #expect(outcome == .sent)
        model.settleSend(key, pending.id, outcome)
        #expect(store.sendFeedback?.phase == .processing)
        #expect(store.sendFeedback?.acceptance == .receipt, "HTTP settlement preserves receipt acceptance")
        #expect(store.sendFeedback?.acceptedAt == receiptAcceptedAt, "HTTP settlement does not overwrite receipt timestamp")
        #expect(store.pending.first?.state == .sent)
        #expect(model.sendReceipts.waiting == 0)
    }

    @Test func untrustedOldHubFallsBackToHttpResponse() async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let (model, socket) = try connectedApp(hub: hub)
        defer { withExtendedLifetime(model) {} }
        // Feature array entry alone is not proof; no top-level message_send_state: 1.
        socket.deliver?(Data(Self.legacyHello.utf8))
        #expect(!model.live.receiptTrust.messages)

        let store = model.chat(key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)

        let task = Task { await model.sendPending(key, pending) }
        await http.waitUntilSent()

        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .sending, "untrusted receipt must not move Sending to Processing")

        http.respond()
        let outcome = await task.value
        model.settleSend(key, pending.id, outcome)
        #expect(store.sendFeedback?.phase == .processing, "HTTP answer remains the fallback")
        #expect(store.sendFeedback?.acceptance == .http)
    }

    @Test func droppedSocketClearsTrustAndFallsBackToHttp() async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let (model, socket) = try connectedApp(hub: hub)
        defer { withExtendedLifetime(model) {} }
        socket.deliver?(Data(Self.trustingHello.utf8))
        #expect(model.live.receiptTrust.messages)

        let store = model.chat(key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)

        let task = Task { await model.sendPending(key, pending) }
        await http.waitUntilSent()

        socket.drop?()
        #expect(!model.live.receiptTrust.messages)
        model.live.reconnectNow()
        // Frame arrives before the new socket's authenticated hello.
        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .sending)

        http.respond()
        let outcome = await task.value
        model.settleSend(key, pending.id, outcome)
        #expect(store.sendFeedback?.phase == .processing)
    }

    @Test func mismatchedClientMsgIdOrSessionDoesNotMoveToProcessing() async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let (model, socket) = try connectedApp(hub: hub)
        defer { withExtendedLifetime(model) {} }
        socket.deliver?(Data(Self.trustingHello.utf8))
        let store = model.chat(key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)

        let task = Task { await model.sendPending(key, pending) }
        await http.waitUntilSent()

        socket.deliver?(receiptFrame("processing", id: UUID().uuidString))
        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString, session: "other-session"))
        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString, machine: "other-computer"))
        #expect(store.sendFeedback?.phase == .sending)

        http.respond()
        let outcome = await task.value
        model.settleSend(key, pending.id, outcome)
        #expect(store.sendFeedback?.phase == .processing)
    }

    @Test func terminalFailedReceiptEndsProcessingBeforeHttpError() async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let (model, socket) = try connectedApp(hub: hub)
        defer { withExtendedLifetime(model) {} }
        socket.deliver?(Data(Self.trustingHello.utf8))
        let store = model.chat(key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)

        let task = Task { await model.sendPending(key, pending) }
        await http.waitUntilSent()

        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .processing)

        socket.deliver?(receiptFrame("failed", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .finished)
        // Late/out-of-order processing cannot revive a finished attempt.
        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .finished)

        http.fail(APIError(status: 502, message: "refused", fields: ["code": "send_failed", "delivered": "none"]))
        let outcome = await task.value
        #expect(outcome == .failed)
        model.settleSend(key, pending.id, outcome)
        #expect(store.sendFeedback?.phase == .finished)
        #expect(store.pending.first?.state == .failed)
        #expect(model.sendReceipts.waiting == 0)
    }

    @Test func receiptTracksProcessingLatencySeparatelyFromDeliveryAndExecution() {
        let id = UUID()
        let receiptTime = now.addingTimeInterval(0.125)
        let httpTime = now.addingTimeInterval(1.5)
        var feedback = ChatSendFeedback(id: id, startedAt: now, afterSequence: 2)
        #expect(feedback.processingLatency == nil && feedback.acceptance == nil)

        feedback.receive(id, state: .processing, at: receiptTime)
        #expect(feedback.phase == .processing)
        #expect(feedback.acceptance == .receipt)
        #expect(feedback.acceptedAt == receiptTime)
        #expect(feedback.processingLatency == 0.125)
        #expect(feedback.deadline == receiptTime.addingTimeInterval(ChatSendFeedback.acceptanceWindow))
        #expect(feedback.echoedSequence == nil)

        // Duplicate processing, delivered receipt, or later HTTP acceptance keep the original receipt timing
        // and never conclude execution.
        feedback.receive(id, state: .processing, at: httpTime)
        feedback.receive(id, state: .delivered, at: httpTime)
        feedback.accept(id, at: httpTime)
        #expect(feedback.phase == .processing)
        #expect(feedback.acceptance == .receipt)
        #expect(feedback.acceptedAt == receiptTime)
        #expect(feedback.processingLatency == 0.125)

        feedback.echo(item(3, role: "user", id: id))
        #expect(!feedback.answered(by: item(4, role: "assistant", kind: .thinking)))
        #expect(feedback.answered(by: item(5, role: "assistant")))
    }

    @Test func deliveredReceiptWithoutPriorProcessingStillMovesToProcessing() {
        let id = UUID()
        let deliveredAt = now.addingTimeInterval(0.25)
        var feedback = ChatSendFeedback(id: id, startedAt: now, afterSequence: 2)
        feedback.receive(id, state: .delivered, at: deliveredAt)
        #expect(feedback.phase == .processing)
        #expect(feedback.acceptance == .receipt)
        #expect(feedback.processingLatency == 0.25)
    }

    @Test(arguments: [SendReceipt.State.failed, .kept, .unknown])
    func terminalReceiptsEndFeedbackAndIgnoreMismatchedOrLateEvents(_ state: SendReceipt.State) {
        let id = UUID(), other = UUID()
        var feedback = ChatSendFeedback(id: id, startedAt: now, afterSequence: 2)
        feedback.receive(other, state: .processing, at: now.addingTimeInterval(0.1))
        feedback.receive(other, state: state, at: now.addingTimeInterval(0.2))
        #expect(feedback.phase == .sending)

        feedback.receive(id, state: .processing, at: now.addingTimeInterval(0.1))
        #expect(feedback.phase == .processing)
        feedback.receive(id, state: state, at: now.addingTimeInterval(0.3))
        #expect(feedback.phase == .finished)
        #expect(feedback.visible(now: now.addingTimeInterval(0.4)) == nil)

        feedback.receive(id, state: .processing, at: now.addingTimeInterval(0.5))
        feedback.receive(id, state: .delivered, at: now.addingTimeInterval(0.6))
        #expect(feedback.phase == .finished)
    }

    @Test(arguments: ["kept", "unknown"])
    func keptOrUnknownReceiptEndsProcessingOverSocket(_ wireState: String) async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let (model, socket) = try connectedApp(hub: hub)
        defer { withExtendedLifetime(model) {} }
        socket.deliver?(Data(Self.trustingHello.utf8))
        let store = model.chat(key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)

        let task = Task { await model.sendPending(key, pending) }
        await http.waitUntilSent()

        socket.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .processing)

        socket.deliver?(receiptFrame(wireState, id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .finished)

        if wireState == "kept" {
            http.respond(Data(#"{"delivered":"later"}"#.utf8))
            let outcome = await task.value
            #expect(outcome == .kept)
            model.settleSend(key, pending.id, outcome)
        } else {
            http.fail(APIError(status: 502, message: "unknown", fields: ["delivered": "unknown"]))
            let outcome = await task.value
            #expect(outcome == .failed)
            model.settleSend(key, pending.id, outcome)
            #expect(store.pending.first?.mayHaveArrived == true)
        }
        #expect(store.sendFeedback?.phase == .finished)
        #expect(model.sendReceipts.waiting == 0)
    }

    @Test func multiHubReceiptOnlyMovesItsOwnHubGroupChat() async throws {
        let hub = FakeHub()
        let http = PendingHTTP()
        let g2Key = SessionKey(group: "g2", machineID: key.machineID, sessionID: key.sessionID)
        hub.onAsync("POST", "/api/v1/sessions/\(key.machineID)/\(key.sessionID)/messages") { req in
            try await http.handle(req)
        }
        let transport = try #require(hub.client.transport)
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "send-feedback-groups-\(UUID().uuidString)"))
        model.forgetRestoredForTests()
        let sockets = SendReceiptsTests.Sockets()
        model.linkHooksForTests = (transport: transport, socket: { sockets.make() })
        model.registry = HubRegistry(hubs: [PairedHub(id: "a", config: .demo, group: ""), PairedHub(id: "b", config: .demo, group: "g2")])
        model.buildLinks()
        defer { withExtendedLifetime(model) {} }
        for link in model.links { link.live.connect(.demo) }
        let home = try #require(sockets.all.first), other = try #require(sockets.all.last)
        home.deliver?(Data(Self.trustingHello.utf8))
        other.deliver?(Data(Self.trustingHello.utf8))
        let row = try session(status: "idle")
        model.applySessions([MachineRef(group: "g2", id: g2Key.machineID): [row]])

        let store = model.chat(g2Key)
        let pending = PendingMessage(text: "hello", images: [])
        store.pending = [pending]
        store.beginSendFeedback(pending)

        let task = Task { await model.sendPending(g2Key, pending) }
        await http.waitUntilSent()

        // Default hub group receipt with the same id and session must not move g2's chat.
        home.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .sending)

        // Matching g2 receipt moves g2's chat to Processing.
        other.deliver?(receiptFrame("processing", id: pending.id.uuidString))
        #expect(store.sendFeedback?.phase == .processing)
        #expect(store.sendFeedback?.acceptance == .receipt)

        http.respond()
        let outcome = await task.value
        model.settleSend(g2Key, pending.id, outcome)
        #expect(store.sendFeedback?.phase == .processing)
        #expect(model.sendReceipts.waiting == 0)
    }
}
