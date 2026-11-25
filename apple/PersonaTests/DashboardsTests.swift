import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Dashboards (#15017) on the iPhone and iPad: the reserved `-pages` extension is drawn over HTTP only, `assistant_pages`
/// reads the places again, a page's header comes from its View and never shows a raw page id, and the switch in
/// Settings › Assistant follows the hub's feature. All hubs, pages and titles are made up.
@MainActor struct DashboardsTests {
    private static let page = "pg_3f9a1c0d5e7b2a41"

    /// A client that speaks the extensions API, as a hub with Dashboards does.
    private func client(_ hub: FakeHub) -> APIClient {
        var api = hub.client
        api.extensionsAPI = true
        return api
    }

    // MARK: 1. Reserved ids are HTTP only

    @Test func reservedIDsNeverOpenTheNativeStream() async throws {
        let hub = FakeHub()
        let api = client(hub)
        let path = api.extensionPath("/\(DashboardsUI.id)/-/ui/\(Self.page)")
        hub.on("POST", path, json: #"{"view":{"title":"Morning run","sections":[]}}"#)
        for (ext, ui) in [(DashboardsUI.id, DashboardsUI.index), (DashboardsUI.id, Self.page), ("-future-reserved", "home")] {
            #expect(ReservedExtensionUI.httpOnly(ext), "\(ext) is a reserved id")
            let model = ExtensionViewModel(plugin: ext, ui: ui, context: .here().forPage(),
                                           deps: .init(api: { api }, connection: { LiveConnection() }))
            #expect(model.stream == nil, "\(ext)/\(ui) must not build an ExtensionPageStream")
        }
        // An installed extension's page still negotiates the stream: the guard is for "-" ids alone.
        #expect(!ReservedExtensionUI.httpOnly("example-gateway"))
        let installed = ExtensionViewModel(plugin: "example-gateway", ui: "usage", context: .here().forPage(),
                                           deps: .init(api: { api }, connection: { LiveConnection() }))
        #expect(installed.stream != nil)
        installed.stream?.close()

        // The page is drawn with the ordinary UIRequest over HTTP.
        let model = ExtensionViewModel(plugin: DashboardsUI.id, ui: Self.page, context: .here().forPage(),
                                       deps: .init(api: { api }, connection: { LiveConnection() }))
        await model.load()
        #expect(model.view?.title == "Morning run" && model.error == nil)
        #expect(hub.sent("POST", path).count == 1)
    }

    // MARK: 2. assistant_pages reads the places again

    private func appModel(_ hub: FakeHub) throws -> AppModel {
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "dashboards-" + UUID().uuidString))
        model.linkHooksForTests = (transport: try #require(hub.client.transport), socket: { SyncSocket() })
        model.connectOnly(.demo)
        return model
    }

    /// Waits for `reads` to reach `count` (the reload runs in a Task the event starts).
    private func settle(_ reads: @escaping () -> Int, at count: Int) async {
        for _ in 0..<200 where reads() < count { try? await Task.sleep(for: .milliseconds(5)) }
        try? await Task.sleep(for: .milliseconds(20)) // and nothing more after it
    }

    @Test func assistantPagesDecodesAndReloadsTheSlots() async throws {
        for json in [#"{"type":"assistant_pages"}"#, #"{"type":"assistant_pages","payload":{}}"#] {
            guard case .assistantPages = try #require(ServerEvent.decode(Data(json.utf8))) else {
                Issue.record("\(json) did not decode as assistantPages"); return
            }
        }
        guard case .assistantPages = ServerEvent.assistantPages.stamped("another-hub") else {
            Issue.record("another hub's event keeps its kind"); return
        }

        let model = try appModel(FakeHub())
        var reads = 0
        model.reloadExtensionPlaces = { reads += 1 }
        let event = try #require(ServerEvent.decode(Data(#"{"type":"assistant_pages"}"#.utf8)))
        model.handle(event, group: "another-hub")
        await settle({ reads }, at: 1)
        #expect(reads == 0, "another hub's pages are not this sidebar's")
        model.handle(event, group: "")
        await settle({ reads }, at: 1)
        #expect(reads == 1)
    }

    @Test func aBurstOfAssistantPagesIsCoalesced() async throws {
        let model = try appModel(FakeHub())
        var reads = 0
        var release: CheckedContinuation<Void, Never>?
        model.reloadExtensionPlaces = {
            reads += 1
            if reads == 1 { await withCheckedContinuation { release = $0 } }
        }
        model.handle(.assistantPages, group: "")
        await settle({ reads }, at: 1)
        #expect(reads == 1)
        for _ in 0..<4 { model.handle(.assistantPages, group: "") }
        await settle({ reads }, at: 2)
        #expect(reads == 1, "one read at a time")
        release?.resume()
        await settle({ reads }, at: 2)
        #expect(reads == 2, "the events during the read make one more, not four")
        model.handle(.assistantPages, group: "")
        await settle({ reads }, at: 3)
        #expect(reads == 3, "a later change reads again")
    }

    @Test func theSlotsListDashboardsAsPlacesButSettingsKeepsThemOut() async throws {
        let hub = FakeHub()
        let api = client(hub)
        hub.on("GET", api.extensionPath("/-/ui"), json: #"""
        {"ui":[{"extension":"example-gateway","id":"usage","title":"Usage","placement":"nav_page","icon":"chart"},
               {"extension":"-pages","id":"index","title":"Dashboards","placement":"nav_page","icon":"chart"},
               {"extension":"-pages","id":"pg_3f9a1c0d5e7b2a41","title":"Morning run","placement":"nav_page","icon":"chart"}]}
        """#)
        let slots = ExtensionSlots(deps: .init(api: { api }), features: { ["extension_ui_slots"] })
        await slots.reload()
        #expect(slots.pages.map(\.scopedID) == ["example-gateway/usage", "-pages/index", "-pages/pg_3f9a1c0d5e7b2a41"],
                "the drawer and the sidebar list them in the hub's order")
        #expect(slots.pages.allSatisfy { $0.badge == nil })
        #expect(slots.extensionPages.map(\.scopedID) == ["example-gateway/usage"], "Settings › Extensions lists extensions only")
        #expect(ExtensionNavRoute(slots.pages[2]).title == "Morning run")
    }

    // MARK: 3. The header

    @Test func theHeaderTitleForDashboardsComesFromTheView() {
        let title = ExtensionPageView.headerTitle
        #expect(title(DashboardsUI.id, Self.page, "Morning run", "Pinned name") == "Morning run")
        #expect(title(DashboardsUI.id, Self.page, nil, "Morning run") == "Morning run", "the place's title until the View comes")
        #expect(title(DashboardsUI.id, Self.page, "  ", "Morning run") == "Morning run", "an empty View title is no title")
        #expect(title(DashboardsUI.id, Self.page, nil, Self.page) == DashboardsUI.name, "never the raw page id")
        #expect(title(DashboardsUI.id, Self.page, Self.page, "") == DashboardsUI.name)
        #expect(title(DashboardsUI.id, DashboardsUI.index, "Dashboards", "") == "Dashboards")
        // Another extension's page keeps what it had: its View's title as sent, else the one it was opened with.
        #expect(title("example-gateway", "usage", nil, "Usage") == "Usage")
        #expect(title("example-gateway", "usage", "", "Usage") == "")

        #expect(DashboardsUI.isPageID(Self.page) && !DashboardsUI.isPageID("pg_3F9A1C0D5E7B2A41") && !DashboardsUI.isPageID("index"))
        #expect(DashboardsUI.showsOwnership(DashboardsUI.id, ui: Self.page))
        #expect(!DashboardsUI.showsOwnership(DashboardsUI.id, ui: DashboardsUI.index), "the index has no ownership line")
        #expect(!DashboardsUI.showsOwnership("example-gateway", ui: Self.page))
    }

    @Test func aPageOpenedByALinkStartsWithItsPlaceTitle() throws {
        let places = try JSON.decoder.decode([ExtensionUIPlace].self, from: Data(#"""
        [{"extension":"-pages","id":"index","title":"仪表盘","placement":"nav_page","icon":"chart"},
         {"extension":"-pages","id":"pg_3f9a1c0d5e7b2a41","title":"晨跑打卡","placement":"nav_page","icon":"chart"}]
        """#.utf8))
        #expect(DashboardsUI.placeTitle(Self.page, in: places) == "晨跑打卡")
        #expect(DashboardsUI.placeTitle("pg_8c2e4b6d1a0f9e37", in: places) == "仪表盘", "a page not pinned: the index's")
        #expect(DashboardsUI.placeTitle(Self.page, in: []) == nil)
        // persona://open/extension/-pages/<id> before the places are read: Dashboards, never the id.
        #expect(DashboardsUI.title(view: nil, place: DashboardsUI.placeTitle(Self.page, in: []), ui: Self.page) == DashboardsUI.name)
        let link = try #require(HubPageLink.appPage(URL(string: "persona://open/extension/-pages/\(Self.page)")!))
        #expect(link.page == .extensionPage(extension: DashboardsUI.id, ui: Self.page, state: nil))
    }

    @Test func theHeaderUsesTheRenderedViewsTitle() async throws {
        let hub = FakeHub()
        let api = client(hub)
        hub.on("POST", api.extensionPath("/\(DashboardsUI.id)/-/ui/\(Self.page)"), json: #"{"view":{"title":"晨跑打卡","sections":[]}}"#)
        let model = ExtensionViewModel(plugin: DashboardsUI.id, ui: Self.page, context: .here().forPage(), deps: .init(api: { api }))
        #expect(ExtensionPageView.headerTitle(plugin: model.plugin, ui: model.ui, view: model.view?.title, opened: Self.page) == DashboardsUI.name)
        await model.load()
        #expect(ExtensionPageView.headerTitle(plugin: model.plugin, ui: model.ui, view: model.view?.title, opened: Self.page) == "晨跑打卡")
    }

    // MARK: 4. The switch

    @Test func theSwitchIsHiddenWithoutTheFeature() {
        #expect(!DashboardsUI.offered(features: [], manager: nil))
        #expect(!DashboardsUI.offered(features: ["extension_ui_slots", "next_suggestions"], manager: nil))
        #expect(DashboardsUI.offered(features: [DashboardsUI.feature], manager: nil))
        #expect(DashboardsUI.offered(features: [DashboardsUI.feature], manager: ""))
        #expect(!DashboardsUI.offered(features: [DashboardsUI.feature], manager: "m-work"), "only the default assistant has Dashboards")
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "dashboards-off-" + UUID().uuidString))
        #expect(!model.dashboardsOffered, "a model that heard no hello offers no switch")
    }

    @Test func theSettingIsReadAbsentAsOffAndWrittenAlone() throws {
        func state(_ field: String) -> String {
            #"{"machine_id":"box","launcher":"claude","cwd":"/assistant","conversations":[]\#(field)}"#
        }
        #expect(try JSON.decoder.decode(APIClient.AssistantState.self, from: Data(state(#","dashboards":true"#).utf8)).dashboards == true)
        #expect(try JSON.decoder.decode(APIClient.AssistantState.self, from: Data(state(#","dashboards":false"#).utf8)).dashboards == false)
        #expect(try JSON.decoder.decode(APIClient.AssistantState.self, from: Data(state("").utf8)).dashboards == nil)
        let body = try #require(try JSONSerialization.jsonObject(with: JSONEncoder().encode(APIClient.AssistantConfigBody(dashboards: true))) as? [String: Any])
        #expect(body["dashboards"] as? Bool == true && body.count == 1, "PUT {\"dashboards\": true}")
        let other = try #require(try JSONSerialization.jsonObject(with: JSONEncoder().encode(APIClient.AssistantConfigBody(recentTaskChats: true))) as? [String: Any])
        #expect(other["dashboards"] == nil)
    }

    @Test func turningItOnIsShownAtOnceThenReadsThePlaces() async throws {
        let hub = FakeHub()
        let base = #"{"machine_id":"box","launcher":"claude","cwd":"/assistant","conversations":[]"#
        hub.on("PUT", "/api/v1/assistant") { req in
            let sent = try JSON.decoder.decode(JSONValue.self, from: try #require(req.body))
            #expect(sent["dashboards"] == .bool(true))
            return Data((base + #","dashboards":true}"#).utf8)
        }
        let model = try appModel(hub)
        var reads = 0
        model.reloadExtensionPlaces = { reads += 1 }
        model.assistantSettingsSaved(try JSON.decoder.decode(APIClient.AssistantState.self, from: Data((base + "}").utf8)))
        #expect(model.assistantConfig?.dashboards == nil, "absent: off")
        await model.setDashboards(true)
        #expect(model.assistantConfig?.dashboards == true)
        #expect(hub.sent("PUT", "/api/v1/assistant").count == 1)
        await settle({ reads }, at: 1)
        #expect(reads == 1, "the places are read again once the hub has it")

        hub.on("PUT", "/api/v1/assistant") { _ in throw APIError(status: 500, message: "synthetic failure") }
        await model.setDashboards(false)
        #expect(model.assistantConfig?.dashboards == true, "a refused write is put back")
        await settle({ reads }, at: 2)
        #expect(reads == 1)
    }
}
