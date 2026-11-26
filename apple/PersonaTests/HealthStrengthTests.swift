import Foundation
import Testing
import PersonaWire
@testable import Persona

@MainActor struct HealthStrengthTests {
    private let available = ["water", "protein", "activity", "trends", "strength"]
    private func items(_ ids: [String], saved: Bool = true) -> HealthItems {
        .init(catalog: available.map { .init(id: $0, source: "persona") }, items: ids, revision: 3, saved: saved)
    }
    private func page(_ hub: FakeHub) -> HealthItemsModel {
        .init(deps: .init(api: { hub.client }, report: { _ in }, toast: { _ in }, open: { _ in }))
    }
    @Test func strengthRequiresBothSelectionAndCapabilityAndKeepsItsPosition() async throws {
        let hub = FakeHub(), page = page(hub)
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["trends", "strength", "protein"])) }
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.sections == [.trends, .today] && !page.canShowStrength)
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: available)
        #expect(page.sections == [.trends, .strength, .today] && page.canShowStrength)
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["protein"])) }
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: available)
        #expect(page.sections == [.today])
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: ["water", "protein"])
        #expect(!page.canShowStrength && page.sections == [.today], "Vision keeps only Today")
    }

    @Test(arguments: [false, true]) func editorKeepsHiddenStrengthOrLetsItMove(supported: Bool) async throws {
        let hub = FakeHub(), page = page(hub)
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["water", "strength", "protein"])) }
        let features: Set<String> = supported ? [HealthItems.feature, HealthStrengthUI.feature] : [HealthItems.feature]
        await page.load(features: features, available: available)
        page.beginEditing()
        #expect(page.editOrder.contains("strength") == supported)
        if supported { page.moveUp("strength") }
        page.setAdded("water", false)
        hub.on("PUT", "/api/v1/assistant/health/items") { request in
            let body = try JSON.decoder.decode(JSONValue.self, from: #require(request.body))
            let proposed = body["items"]?.array?.compactMap(\.string)
            let result = try #require(proposed)
            #expect(result == (supported ? ["strength", "protein"] : ["protein", "strength"]))
            return try JSON.encoder.encode(items(result))
        }
        await page.saveEditor()
        #expect(!page.editing && page.error == nil)
    }

    @Test func losingTheCapabilityDuringEditingPreservesItsNowHiddenSlot() async throws {
        let hub = FakeHub(), page = page(hub)
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["water", "strength", "protein"])) }
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: available)
        page.beginEditing()
        page.setAdded("water", false)
        page.setAdded("strength", false)
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.editing && !page.editOrder.contains("strength") && !page.canShowStrength)
        hub.on("PUT", "/api/v1/assistant/health/items") { request in
            let body = try JSON.decoder.decode(JSONValue.self, from: #require(request.body))
            #expect(body["items"]?.array == [.string("protein"), .string("strength")])
            return try JSON.encoder.encode(items(["protein", "strength"]))
        }
        await page.saveEditor()
        #expect(!page.editing && page.error == nil)
    }

    @Test func firstQuestionCanAddStrengthAndDoesNotAskAnExistingStrengthOwner() async throws {
        let hub = FakeHub(), page = page(hub)
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["activity"], saved: false)) }
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: available)
        #expect(page.question && page.canShowStrength)
        page.wantsStrength = true
        hub.on("PUT", "/api/v1/assistant/health/items") { request in
            let body = try JSON.decoder.decode(JSONValue.self, from: #require(request.body))
            #expect(body["items"]?.array == [.string("activity"), .string("strength")])
            return try JSON.encoder.encode(items(["activity", "strength"]))
        }
        await page.answerQuestion(add: true)
        #expect(!page.question && page.sections == [.activity, .strength])
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["strength"], saved: false)) }
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: available)
        #expect(!page.question)
    }

    @Test func hiddenStrengthKeepsTheExistingFirstQuestion() async throws {
        let hub = FakeHub(), page = page(hub)
        hub.on("GET", "/api/v1/assistant/health/items") { _ in try JSON.encoder.encode(items(["strength"], saved: false)) }
        await page.load(features: [HealthItems.feature], available: available)
        #expect(page.question && !page.canShowStrength && page.sections.isEmpty)
        await page.load(features: [HealthItems.feature, HealthStrengthUI.feature], available: ["water", "protein"])
        #expect(page.question && !page.canShowStrength && page.sections.isEmpty)
    }

    @Test func reservedPagesUseTheirServerTitlesOpaqueStateAndRefreshForEventsAndHello() async throws {
        let hub = FakeHub()
        let states = [HealthStrengthUI.home, HealthStrengthUI.overview, #"{"page":"strength.exercise","exercise":"sample"}"#, #"{"page":"strength.edit","draft":"sample"}"#]
        hub.on("POST", HealthIntakeUI.path, json: #"{"view":{"title":"Strength training","sections":[],"state":"opaque-strength"}}"#)
        let pages = states.map { state in ExtensionViewModel(plugin: HealthIntakeUI.id, ui: HealthIntakeUI.page,
            context: .here().forPage(), state: state, deps: .init(api: { hub.client }, connection: { LiveConnection() })) }
        for page in pages {
            await page.load()
            #expect(page.stream == nil && page.view?.title == "Strength training")
        }
        let folder = FileManager.default.temporaryDirectory.appending(path: "strength-" + UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let model = AppModel(stateFolder: folder)
        model.linkHooksForTests = (transport: try #require(hub.client.transport), socket: { SyncSocket() })
        model.connectOnly(.demo)
        let revision = model.healthIntakeRevision
        let event = try #require(ServerEvent.decode(Data(#"{"type":"health_strength"}"#.utf8)))
        guard case .healthStrength = event else { Issue.record("missing strength event"); return }
        model.handle(event, group: "other")
        #expect(pages.allSatisfy { $0.invalidationVersion == 0 })
        model.handle(event, group: "")
        #expect(model.healthIntakeRevision == revision + 1)
        #expect(pages.allSatisfy { $0.stale })
        model.handle(.hello(HubHello(features: [HealthStrengthUI.feature])), group: "")
        #expect(pages.allSatisfy { $0.invalidationVersion == 2 })
        hub.on("POST", HealthIntakeUI.path, json: #"{"view":{"title":"Updated strength","sections":[],"state":"next"}}"#)
        for page in pages { await page.refresh(); #expect(page.view?.title == "Updated strength" && !page.stale) }
        let request = try #require(hub.requests.last)
        #expect(request.query == ["space": "health"] && request.headers[APIClient.managerHeader] == nil)
        let body = try JSON.decoder.decode(JSONValue.self, from: #require(request.body))
        #expect(body["state"]?.string == "opaque-strength")
    }

    @Test(arguments: ["cancel", "save"]) func managedCardAdjustOpensTheReservedEditorAndReturns(action: String) async throws {
        let hub = FakeHub()
        let state = #"{"draft":"sample-draft","page":"strength.edit","rev":1}"#
        var url = URLComponents(string: "https://fake.invalid/extension/-health-intake/pages")!
        url.queryItems = [URLQueryItem(name: "state", value: state)]
        #expect(HubPageLink.page(try #require(url.url), hubs: [hub.url]) == .extensionPage(extension: HealthIntakeUI.id, ui: HealthIntakeUI.page, state: state))
        let folder = FileManager.default.temporaryDirectory.appending(path: "strength-route-" + UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let app = AppModel(stateFolder: folder)
        app.linkHooksForTests = (transport: try #require(hub.client.transport), socket: { SyncSocket() })
        app.connectOnly(ServerConfig(url: hub.url, token: "sample"))
        let adjust = ExtensionAction(id: "adjust", open: try #require(url.url?.absoluteString))
        let card = ExtensionView(sections: [.init(items: [.button(.init(title: "Adjust", action: adjust))])], fallback: "Sample strength")
        let cardModel = AssistantViewModel(key: "health-strength-sample", scope: .init(group: "", conversation: "sample-chat"), view: card,
            store: AssistantViewsStore(), deps: .init(send: { _ in throw APIError(status: 400, message: "Adjust must open, not send") }))
        let opened = try #require(await cardModel.perform(adjust).url)
        #expect(app.openOwnLink(opened) == .opened)
        #expect(app.extensionOpen?.link.owner == HealthIntakeUI.id && app.extensionOpen?.link.state == state)
        hub.on("POST", HealthIntakeUI.path, json: #"{"pop":true}"#)
        let page = ExtensionViewModel(plugin: HealthIntakeUI.id, ui: HealthIntakeUI.page, state: state, deps: .init(api: { hub.client }))
        _ = await page.perform(.init(id: action))
        #expect(page.popped)
    }
}
