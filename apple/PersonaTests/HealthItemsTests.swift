import Foundation
import Testing
import PersonaWire
@testable import Persona

@MainActor struct HealthItemsTests {
    private let path = "/api/v1/assistant/health/items"
    private let available = ["water", "protein", "activity", "trends"]
    private func value(_ items: [String], revision: Int = 0, saved: Bool = false) -> HealthItems {
        .init(catalog: available.map { .init(id: $0, source: "persona") }, items: items, revision: revision, saved: saved)
    }
    private func page(_ hub: FakeHub) -> HealthItemsModel {
        .init(deps: .init(api: { hub.client }, report: { _ in }, toast: { _ in }, open: { _ in }))
    }
    private func route(_ hub: FakeHub, _ value: HealthItems) throws {
        let bytes = try JSON.encoder.encode(value)
        hub.on("GET", path) { _ in bytes }
    }

    @Test func itemBroadcastInvalidatesOnlyItsHub() throws {
        let event = try #require(ServerEvent.decode(Data(#"{"type":"health_items","revision":9}"#.utf8)))
        guard case .healthItems = event else { Issue.record("missing health items event"); return }
        let folder = FileManager.default.temporaryDirectory.appending(path: "health-items-" + UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let model = AppModel(stateFolder: folder)
        let hub = FakeHub()
        model.linkHooksForTests = (transport: try #require(hub.client.transport), socket: { SyncSocket() })
        model.connectOnly(.demo)
        let revision = model.healthIntakeRevision
        model.handle(event, group: "another-hub")
        #expect(model.healthIntakeRevision == revision)
        model.handle(event, group: "")
        #expect(model.healthIntakeRevision == revision + 1)
    }

    @Test func bundledDemoSavesRevisionsAndReturnsTheCurrentConflictState() async throws {
        DemoHub.install()
        DemoHub.shared.reset()
        defer { DemoHub.shared.reset() }
        let api = APIClient(config: .demo)
        let before = try await api.healthItems()
        #expect(before.items == available && before.revision == 0 && !before.saved)
        let intake = try await api.healthIntake()
        let saved = try await api.saveHealthItems(["protein"], revision: before.revision)
        #expect(saved.items == ["protein"] && saved.revision == 1 && saved.saved)
        let after = try await api.healthIntake()
        #expect(!after.contains(.water) && after.contains(.protein))
        #expect(after.water.entries == intake.water.entries)
        do {
            _ = try await api.saveHealthItems(["water"], revision: before.revision)
            Issue.record("stale demo selection must conflict")
        } catch let error as APIError {
            #expect(error.status == 409 && error.details?["revision"]?.int == 1)
            #expect(error.details?["items"]?.array == [.string("protein")])
        }
    }

    @Test func capabilityGatesRequestsAndKeepsTheOldSections() async throws {
        let hub = FakeHub(), page = page(hub)
        await page.load(features: [], available: available)
        #expect(hub.requests.isEmpty && !page.supported && !page.question)
        #expect(page.sections == [.today, .activity, .trends])
        #expect(page.todayKinds == [.water, .protein])
        try route(hub, value(["trends", "protein", "activity", "water"]))
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.sections == [.trends, .today, .activity])
        #expect(page.todayKinds == [.protein, .water])
        await page.load(features: [], available: available)
        #expect(page.sections == [.today, .activity, .trends])
    }

    @Test(arguments: [false, true]) func aPendingOrFailedFirstReadKeepsTheLocalProjection(hasIntake: Bool) async throws {
        let hub = FakeHub(), page = page(hub)
        let started = AsyncStream<Void>.makeStream()
        var release: CheckedContinuation<Void, Never>?
        hub.onAsync("GET", path) { _ in
            started.continuation.yield()
            started.continuation.finish()
            await withCheckedContinuation { release = $0 }
            throw APIError(status: 503, message: "Sample items unavailable")
        }
        let local = hasIntake ? ["protein"] : nil
        let read = Task { await page.load(features: [HealthItems.feature], available: available, intakeItems: local) }
        for await _ in started.stream { break }
        let expected: [HealthItemsModel.SectionID] = hasIntake ? [.today] : [.today, .activity, .trends]
        #expect(page.sections == expected && !page.question && page.state == nil)
        #expect(page.todayKinds == (hasIntake ? [.protein] : [.water, .protein]))
        page.useIntakeItems(["protein"])
        #expect(page.sections == [.today] && page.todayKinds == [.protein] && !page.question)
        page.useIntakeItems(local)
        release?.resume()
        await read.value
        #expect(page.sections == expected && !page.question && page.error != nil)
        hub.onAsync("GET", path) { _ in try JSON.encoder.encode(value(["trends", "water"], revision: 1, saved: true)) }
        await page.load(features: [HealthItems.feature], available: available, intakeItems: local)
        #expect(page.sections == [.trends, .today] && page.todayKinds == [.water] && !page.question)
    }

    @Test func intakeAfterAFailedItemsReadUpdatesOnlyItsOwnScope() async throws {
        let first = FakeHub(), second = FakeHub(url: "https://second.invalid")
        var active: APIClient? = first.client
        let page = HealthItemsModel(deps: .init(api: { active }, report: { _ in }, toast: { _ in }, open: { _ in }))
        first.on("GET", path) { _ in throw APIError(status: 503, message: "Sample unavailable") }
        await page.load(features: [HealthItems.feature], available: available)
        page.useIntakeItems(["protein"])
        #expect(page.sections == [.today] && page.todayKinds == [.protein] && !page.question)
        active = second.client
        page.useIntakeItems(["water"])
        #expect(page.todayKinds == [.protein], "a response from an old scope cannot replace its projection")
        second.on("GET", path) { _ in throw APIError(status: 503, message: "Sample unavailable") }
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.sections == [.today, .activity, .trends], "another hub cannot inherit the first intake list")
        try route(second, value([], revision: 2, saved: true))
        await page.load(features: [HealthItems.feature], available: available)
        page.useIntakeItems(["protein"])
        #expect(page.sections.isEmpty && page.todayKinds.isEmpty && !page.question, "saved empty state wins")
    }

    @Test func closingTheEditorClearsItsSaveError() async throws {
        let hub = FakeHub(), page = page(hub)
        try route(hub, value(available, revision: 1, saved: true))
        await page.load(features: [HealthItems.feature], available: available)
        page.beginEditing()
        page.setAdded("water", false)
        hub.on("PUT", path) { _ in throw URLError(.notConnectedToInternet) }
        await page.saveEditor()
        #expect(page.error != nil && page.editing)
        page.closeEditor()
        #expect(page.error == nil && !page.editing)
    }

    @Test func aReadDuringEditingKeepsTheOpeningRevisionAndConflictsOnlyOnce() async throws {
        let hub = FakeHub(), page = page(hub)
        try route(hub, value(available, revision: 1, saved: true))
        await page.load(features: [HealthItems.feature], available: available)
        page.beginEditing()
        page.setAdded("water", false)
        let newer = value(["water", "trends"], revision: 2, saved: true)
        try route(hub, newer)
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.state == newer && !page.selected.contains("water"))
        let details = try JSON.decoder.decode(JSONValue.self, from: JSON.encoder.encode(newer))
        hub.on("PUT", path) { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: req.body!)
            #expect(body["revision"]?.int == 1)
            #expect(body["items"]?.array?.contains(.string("water")) == false)
            throw APIError(status: 409, message: "Conflict", details: details)
        }
        await page.saveEditor()
        #expect(page.state == newer && page.selected == ["water", "trends"] && page.error != nil)
        #expect(hub.sent("PUT", path).count == 1)
    }

    @Test func removingTheHubClearsItsProjectionBeforeAnotherRead() async throws {
        let hub = FakeHub()
        var active: APIClient? = hub.client
        let page = HealthItemsModel(deps: .init(api: { active }, report: { _ in }, toast: { _ in }, open: { _ in }))
        try route(hub, value(["activity", "trends"]))
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.question && !page.sections.isEmpty)
        active = nil
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.state == nil && page.sections.isEmpty && page.todayKinds.isEmpty && !page.question)
    }

    @Test func ipadAndVisionOnlyProjectTheItemsTheyCanDisplay() async throws {
        let hub = FakeHub(), page = page(hub)
        try route(hub, value(["activity", "trends", "protein"]))
        await page.load(features: [HealthItems.feature], available: ["water", "protein", "trends"])
        #expect(page.sections == [.trends, .today])
        await page.load(features: [HealthItems.feature], available: ["water", "protein"])
        #expect(page.sections == [.today] && page.todayKinds == [.protein])
    }

    @Test func savingReordersVisibleSlotsAndKeepsHiddenAndUnknownIDs() async throws {
        let hub = FakeHub(), page = page(hub)
        let original = ["water", "future-one", "activity", "protein", "future-two", "trends"]
        try route(hub, value(original, revision: 7, saved: true))
        await page.load(features: [HealthItems.feature], available: ["water", "protein", "trends"])
        page.beginEditing()
        page.move(from: IndexSet(integer: 2), to: 0)
        page.setAdded("water", false)
        hub.on("PUT", path) { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: req.body!)
            #expect(body["items"]?.array?.compactMap(\.string) == ["trends", "future-one", "activity", "protein", "future-two"])
            #expect(body["revision"]?.int == 7)
            #expect(req.headers[APIClient.managerHeader] == nil)
            return try JSON.encoder.encode(value(["trends", "future-one", "activity", "protein", "future-two"], revision: 8, saved: true))
        }
        await page.saveEditor()
        #expect(!page.editing && !page.busy && page.state?.revision == 8)
    }

    @Test func conflictUsesTheReturnedStateAndFailureKeepsTheDraft() async throws {
        let hub = FakeHub(), page = page(hub)
        try route(hub, value(available, revision: 1, saved: true))
        await page.load(features: [HealthItems.feature], available: available)
        page.beginEditing()
        page.setAdded("water", false)
        hub.on("PUT", path) { _ in throw URLError(.notConnectedToInternet) }
        await page.saveEditor()
        #expect(page.editing && !page.selected.contains("water") && page.error != nil && !page.busy)
        let latest = value(["water"], revision: 5, saved: true)
        let details = try JSON.decoder.decode(JSONValue.self, from: JSON.encoder.encode(latest))
        hub.on("PUT", path) { _ in throw APIError(status: 409, message: "Conflict", details: details) }
        await page.saveEditor()
        #expect(page.state == latest && page.selected == ["water"] && page.editing)
        #expect(page.error == String(localized: "Changed elsewhere. Updated to the latest health items."))
        #expect(hub.sent("PUT", path).count == 2)
    }

    @Test(arguments: [true, false]) func aFirstAnswerSavesOnceAndNeverAsksAgain(add: Bool) async throws {
        let hub = FakeHub(), page = page(hub)
        try route(hub, value(["activity", "trends"]))
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.question)
        page.wantsProtein = true
        hub.on("PUT", path) { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: req.body!)
            let proposed = body["items"]?.array?.compactMap(\.string)
            let ids = try #require(proposed)
            #expect(ids == (add ? ["activity", "trends", "protein"] : ["activity", "trends"]))
            return try JSON.encoder.encode(value(ids, revision: 1, saved: true))
        }
        await page.answerQuestion(add: add)
        #expect(!page.question)
        await page.answerQuestion(add: add)
        #expect(hub.sent("PUT", path).count == 1)
        try route(hub, #require(page.state))
        await page.load(features: [HealthItems.feature], available: available)
        #expect(!page.question)
    }

    @Test func oldOwnersAndSavedEmptyListsNeverSeeTheQuestion() async throws {
        for state in [value(available), value([], revision: 2, saved: true)] {
            let hub = FakeHub(), page = page(hub)
            try route(hub, state)
            await page.load(features: [HealthItems.feature], available: available)
            #expect(!page.question)
        }
    }
}
