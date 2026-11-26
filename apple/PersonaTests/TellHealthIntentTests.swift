import AppIntents
import Foundation
import Testing
@testable import Persona

@MainActor struct TellHealthIntentTests {
    private let path = TellHealthFlow.path
    private let features: Set<String> = [TellHealthFlow.feature]
    @Test func oneOptionalTextParameterRunsInTheBackground() {
        #expect(TellHealthIntent().text == nil && TellHealthIntent(text: "Sample").text == "Sample")
        #expect(!TellHealthIntent.openAppWhenRun)
        if #available(iOS 26.0, *) { #expect(TellHealthIntent.supportedModes == .background) }
    }
    @Test(arguments: ["received", "delivered"]) func receiptAndDuplicateAreAcceptedWithoutClaimingExecution(status: String) async throws {
        let hub = FakeHub()
        hub.on("POST", path, json: "{\"id\":\"tell_sample\",\"status\":\"\(status)\",\"duplicate\":true}")
        let receipt = try await TellHealthFlow.send(hub.client.manager("another"), text: "  Sample sentence  ", features: features)
        #expect(receipt.id == "tell_sample" && receipt.duplicate == true && receipt.message == String(localized: "Sent to Health"))
        let request = try #require(hub.requests.first)
        #expect(request.path == path && request.query.isEmpty && request.headers[APIClient.managerHeader] == nil)
        let raw = try JSONSerialization.jsonObject(with: #require(request.body))
        let body = try #require(raw as? [String: String])
        #expect(Set(body.keys) == ["client_id", "text"] && body["text"] == "Sample sentence")
        #expect(body["client_id"]?.range(of: #"^[A-Za-z0-9._:-]{8,128}$"#, options: .regularExpression) != nil)
    }
    @Test func anInitialReceiptHasNoDuplicateFlag() async throws {
        let hub = FakeHub()
        hub.on("POST", path, json: #"{"id":"tell_initial","status":"received"}"#)
        let receipt = try await TellHealthFlow.send(hub.client, text: "Sample", features: features)
        #expect(receipt.id == "tell_initial" && receipt.duplicate == nil)
        #expect(receipt.message == String(localized: "Sent to Health"))
    }
    @Test func aLostReceiptRetriesTheSameIDAndTheNextInvocationGetsAnother() async throws {
        let hub = FakeHub()
        var calls = 0
        hub.on("POST", path) { _ in
            calls += 1
            if calls == 1 { throw URLError(.networkConnectionLost) }
            return Data(#"{"id":"tell_same","status":"received","duplicate":true}"#.utf8)
        }
        _ = try await TellHealthFlow.send(hub.client, text: "Sample", features: features, pause: { _ in })
        _ = try await TellHealthFlow.send(hub.client, text: "Sample", features: features, pause: { _ in })
        let ids = try hub.requests.map { request in
            let bytes = try #require(request.body)
            let raw = try JSONSerialization.jsonObject(with: bytes)
            let body = try #require(raw as? [String: String])
            return try #require(body["client_id"])
        }
        #expect(ids.count == 3 && ids[0] == ids[1] && ids[1] != ids[2])
    }
    @Test func retriesAreBoundedAndFailureIsHonest() async throws {
        let hub = FakeHub()
        hub.on("POST", path) { _ in throw APIError(status: 503, message: "Sample unavailable") }
        do { _ = try await TellHealthFlow.send(hub.client, text: "Sample", features: features, pause: { _ in }); Issue.record("failed send accepted") }
        catch {
            let reason = APIError(status: 503, message: "Sample unavailable").localizedDescription
            #expect(error is TellHealthFailure)
            #expect(error.localizedDescription == TellHealthFailure.uncertain(reason).localizedDescription)
        }
        #expect(hub.requests.count == 3)
        let bodies = try hub.requests.map { request in
            try JSONDecoder().decode([String: String].self, from: #require(request.body))
        }
        let first = try #require(bodies.first)
        #expect(bodies.allSatisfy { $0 == first })
    }
    @Test(arguments: [400, 401, 403, 404]) func refusalsNeverRetry(status: Int) async {
        let hub = FakeHub()
        hub.on("POST", path) { _ in throw APIError(status: status, message: "Sample refusal") }
        do { _ = try await TellHealthFlow.send(hub.client, text: "Sample", features: features, pause: { _ in }); Issue.record("refusal accepted") }
        catch { #expect((error as? APIError)?.status == status) }
        #expect(hub.requests.count == 1)
    }
    @Test func unsupportedOrEmptyInputNeverWritesAndFailedReceiptNeverSaysSent() async {
        let hub = FakeHub()
        for (text, features) in [("Sample", Set<String>()), (" \n ", features), (String(repeating: "a", count: 8193), features)] {
            do { _ = try await TellHealthFlow.send(hub.client, text: text, features: features); Issue.record("invalid request accepted") }
            catch { #expect(error is TellHealthFailure) }
        }
        #expect(hub.requests.isEmpty)
        hub.on("POST", path, json: #"{"id":"tell_failed","status":"failed","duplicate":true}"#)
        do { _ = try await TellHealthFlow.send(hub.client, text: "Sample", features: features); Issue.record("failed receipt accepted") }
        catch { #expect(error.localizedDescription == TellHealthFailure.rejected.localizedDescription) }
        #expect(hub.requests.count == 1)
    }
    @Test func anUnreadableReceiptIsUncertainAndNeverClaimsSuccess() async {
        let hub = FakeHub()
        hub.on("POST", path, json: #"{"id":"tell_sample"}"#)
        do { _ = try await TellHealthFlow.send(hub.client, text: "Sample", features: features); Issue.record("unreadable receipt accepted") }
        catch {
            guard let failure = error as? TellHealthFailure, case .uncertain = failure else {
                Issue.record("an unreadable receipt must ask the owner to check Health"); return
            }
        }
        #expect(hub.requests.count == 1)
    }
    @Test func selectedWatchCredentialKeepsItsHubAndUsesForwardedCapabilities() async throws {
        let credential = ExtensionSurfaceCredential(url: URL(string: "https://watch.invalid")!, token: "sample-watch", name: "Sample", answerOnly: true, capabilities: [TellHealthFlow.feature])
        let snapshot = ExtensionSurfaceSnapshot(groups: ["watch": .init(credential: credential)])
        let api = try TellHealthFlow.client(snapshot: snapshot, selectedGroup: "watch")
        #expect(api.group == "watch" && api.config.token == credential.token && api.config.url == credential.url)
        #expect(throws: LogWaterFailure.self) { try TellHealthFlow.client(snapshot: snapshot, selectedGroup: "removed") }
        let client = ExtensionSurfaceClient(credential: credential, transport: { request in
            #expect(request.url?.path == "/api/v1/health")
            return (Data(#"{"status":"ok"}"#.utf8), HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!)
        }, helloTransport: { _ in Issue.record("answer-only watch must not probe WebSocket"); return [] })
        #expect(try await client.features() == features)
    }
}
