import Foundation
import PersonaKit
import Testing
@testable import Persona

/// Settings capture their assistant once; flat merged lists never show another assistant's copy.
@MainActor
struct SettingsResourceScopeTests {
    private static let header = APIClient.managerHeader
    private static let roster = #"{"default":"assistant","managers":[{"id":"assistant","name":"Personal","online":true,"default":true},{"id":"work","name":"Work","online":true}]}"#
    private static let connectorList = #"{"connectors":[{"id":"notes","name":"Personal Notes","status":"ok","manager":"assistant"},{"id":"notes","name":"Work Notes","status":"ok","manager":"work"}]}"#
    private static let extensionList = #"{"extensions":[{"id":"notes","name":"Personal Notes","kind":"connectors","status":"running","manager":"assistant"},{"id":"notes","name":"Work Notes","kind":"connectors","status":"running","manager":"work"}]}"#
    private static let channel = #"{"id":"chat","name":"Example Chat","kind":"im","capabilities":{},"target":{"kind":"main"},"waiting":{"answers":0,"asks":0},"conversations":[]}"#

    @Test func settingsClientFreezesThePickWithoutChangingLegacyExplicitRouting() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/kb/projects", json: #"{"items":[]}"#)
        hub.on("GET", "/api/v1/connectors", json: Self.connectorList)
        let current = hub.client.showing("work")
        let captured = current.currentAssistantSettings()
        _ = try await captured.kbList(.projects)
        _ = try await captured.connectors()
        #expect(hub.requests.allSatisfy { $0.headers[Self.header] == "work" })
        #expect(captured.shownManager == nil && captured.managerID == "work")
        #expect(current.manager(nil).routing("/api/v1/kb/projects", [:]).isEmpty)
        #expect(current.manager("other").currentAssistantSettings().managerID == "other")
        #expect(!captured.sameSettingsScope(as: current.showing(nil)))
    }

    @Test func connectorAndExtensionListsFilterTheSameDuplicateID() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/managers", json: Self.roster)
        hub.on("GET", "/api/v1/connectors", json: Self.connectorList)
        hub.on("GET", "/api/v1/extensions", json: Self.extensionList)
        var client = hub.client.showing("work")
        client.extensionsAPI = true
        let connectors = ConnectorsListModel(deps: .init(api: { client }))
        let extensions = ExtensionsModel(deps: .init(api: { client }, auth: ExtensionAuthStore()))
        await connectors.load(listed: true)
        await extensions.load()
        #expect(connectors.connectors.map(\.name) == ["Work Notes"])
        #expect(extensions.plugins?.map(\.name) == ["Work Notes"])
        #expect(connectors.groups == nil && extensions.groups == nil)
        #expect(connectors.manager(of: connectors.connectors[0]) == "work")
        #expect(hub.sent("GET", "/api/v1/managers").allSatisfy { $0.headers[Self.header] == nil })
        client = client.showing(nil)
        await connectors.load(listed: true)
        await extensions.load()
        #expect(connectors.connectors.map(\.name) == ["Personal Notes"])
        #expect(extensions.plugins?.map(\.name) == ["Personal Notes"])
    }

    @Test func aRememberedPickFiltersBeforeTheRosterArrives() {
        let items: [(String, String?)] = [("personal", nil), ("work", "work"), ("unknown", "gone")]
        let shown = ManagerRoster.settingsItems(items, roster: nil, manager: "work", stamped: { $0.1 })
        #expect(shown.map(\.0) == ["work"])
    }

    @Test func lateListRepliesDoNotRestoreThePreviousAssistant() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/managers", json: Self.roster)
        var client = hub.client.showing("work")
        hub.on("GET", "/api/v1/connectors") { _ in
            client = client.showing(nil)
            return Data(Self.connectorList.utf8)
        }
        let page = ConnectorsListModel(deps: .init(api: { client }))
        await page.load(listed: true)
        #expect(page.connectors.isEmpty)
        hub.on("GET", "/api/v1/connectors", json: Self.connectorList)
        await page.load(listed: true)
        #expect(page.connectors.map(\.name) == ["Personal Notes"])
    }

    @Test func mailSetupReadsAddsAndDeletesKeepTheirCapturedAssistant() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/mail/accounts", json: #"{"accounts":[]}"#)
        hub.on("GET", "/api/v1/mail/oauth/google/config", json: #"{"configured":false}"#)
        hub.on("POST", "/api/v1/mail/accounts", json: #"{"account":{"id":"example","email":"me@example.com"}}"#)
        hub.on("DELETE", "/api/v1/mail/accounts/example", json: "{}")
        let captured = hub.client.showing("work").currentAssistantSettings()
        _ = try await captured.mailAccounts()
        _ = try await captured.googleConfig()
        _ = try await captured.addMailAccount(.init(name: "Example", email: "me@example.com"))
        try await captured.deleteMailAccount("example")
        #expect(hub.requests.count == 4)
        #expect(hub.requests.allSatisfy { $0.headers[Self.header] == "work" })
    }

    @Test func channelDetailsAndBindingKeepTheirExplicitAssistant() async throws {
        let hub = FakeHub()
        let base = "/api/v1/channels/chat"
        hub.on("GET", base, json: Self.channel)
        hub.on("PUT", base, json: Self.channel)
        hub.on("DELETE", base + "/binding", json: Self.channel)
        hub.on("POST", base + "/test", json: #"{"ok":true}"#)
        hub.on("POST", base + "/bind", json: #"{"step":"done","done":true}"#)
        var client = hub.client.showing("work")
        let page = ChannelModel(id: "chat", manager: "work", deps: .init(api: { client }))
        await page.load()
        client = client.showing(nil)
        await page.setEnabled(false)
        await page.setTarget(.new)
        await page.setFeature("sync", on: true)
        await page.test()
        await page.unbind()
        let binding = ConnectorSignInModel(id: "chat", manager: "work",
            deps: ChannelModel.bindingDeps(.init(api: { client }), report: { _ in }))
        await binding.start()
        #expect(page.error == nil)
        #expect(hub.requests.count == 7)
        #expect(hub.requests.allSatisfy { $0.headers[Self.header] == "work" })
        #expect(ChannelText.page("settings/extensions/notes", manager: "work") == .plugin("notes", manager: "work"))
    }
}
