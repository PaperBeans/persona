import Foundation
import SwiftUI
import Testing
@testable import Persona
import PersonaWire

/// Dashboards phase 2 (#13020) on the iPhone and iPad: a watch page's first section and its Dashboards index row, as the
/// manager draws them (spec/fixtures/dashboards/watch.json "draws"), decode into the components the app has and lay out
/// with the app's tones, icons and colors; the footer's Alert me switch shows a change at once and puts it back on a
/// failure; Settings never lists the hub's own (reserved, "-") Overview cards. All pages and readings are made up.
@MainActor struct DashboardsWatchTests {
    private static let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private struct Fixture: Decodable {
        var draws: [Draw]
    }

    private struct Draw: Decodable {
        var name: String
        var watch: Watch
        var readings: [JSONValue]
        var tripped: Bool
        var section: ExtensionSection
        var glanceRow: JSONValue

        enum CodingKeys: String, CodingKey { case name, watch, readings, tripped, section, glanceRow = "glance_row" }
    }

    private struct Watch: Decodable {
        var question: String
        var display: String
        var columns: [String]?
    }

    private func draws() throws -> [Draw] {
        let data = try Data(contentsOf: Self.repo.appending(path: "spec/fixtures/dashboards/watch.json"))
        return try JSON.decoder.decode(Fixture.self, from: data).draws
    }

    /// The index row the app draws for a kept watch page: the page's own row (title, subtitle, icon, opening it) with the
    /// glance's fields over it, as the manager sends it.
    private func indexRow(_ glance: JSONValue) throws -> ExtensionRowComponent {
        guard case .object(var fields) = glance else { throw DecodingError.dataCorrupted(.init(codingPath: [], debugDescription: "glance_row is an object")) }
        fields["type"] = .string("row")
        fields["key"] = .string("page:pg_1a2b3c4d5e6f7081")
        fields["title"] = .string("Elm Street rent")
        fields["subtitle"] = .string("Checked today 08:02")
        if fields["icon"] == nil { fields["icon"] = .string("money") }
        let data = try JSONEncoder().encode(JSONValue.object(fields))
        guard case .row(let row) = try JSON.decoder.decode(ExtensionComponent.self, from: data) else {
            throw DecodingError.dataCorrupted(.init(codingPath: [], debugDescription: "not a row"))
        }
        return row
    }

    /// Every one of the fixture's draws decodes into components this app draws (nothing left out as unknown), with the
    /// metric first, the table after it where the display is a table, and the tones, icons and colors mapped onto the
    /// app's own: a tripped number or trend in the caution tone, a status light in its named hue, the trend's spark in
    /// the caution tone when tripped.
    @Test func everyFixtureDrawDecodesIntoTheAppsComponents() throws {
        let all = try draws()
        #expect(all.count == 15, "the fixture has 15 draws")
        #expect(Set(all.map(\.watch.display)) == ["number", "status", "table", "trend"])
        for d in all {
            let s = d.section
            #expect(s.key == "persona.watch", "\(d.name): the watch section's key")
            let parts = ExtensionParts.of(ExtensionView(title: "Page", sections: [s]))
            #expect(parts.count == 1, "\(d.name): one section, nothing split or left out")
            #expect(parts.first?.items.count == s.items.count, "\(d.name): every component is one this app draws")
            guard case .metric(let m) = s.items.first else { Issue.record("\(d.name): the first component is a metric"); continue }
            #expect(m.key == "persona.reading")
            #expect(m.label == d.watch.question, "\(d.name): the label is the question")
            #expect(!m.value.isEmpty)
            if d.readings.isEmpty {
                #expect(m.value == "—" && m.detail == "Not checked yet", "\(d.name): empty")
            }
            // Tones: tripped number, table and trend are caution; a status light never has a tone with its color.
            switch d.watch.display {
            case "status":
                #expect(m.tone == nil, "\(d.name): a status light has no tone")
                let hue = try #require(ExtensionUIStyle.color(m.iconColor), "\(d.name): its light is a named hue")
                let expected: Color = switch m.iconColor {
                case "green": Theme.Hue.green
                case "amber": Theme.Hue.amber
                case "red": Theme.Hue.red
                default: Theme.Hue.gray
                }
                #expect(hue == expected)
                #expect(ExtensionUIStyle.icon(m.icon) != Theme.Icon.plugins, "\(d.name): its icon is one of the app's")
                #expect(ExtensionUIStyle.ink(m.tone) == .primary, "\(d.name): the value in the text's own color")
            default:
                if d.tripped {
                    #expect(m.tone == "caution" && m.icon == "warning", "\(d.name): tripped")
                    #expect(ExtensionUIStyle.ink(m.tone) == Theme.caution, "\(d.name): the value in the caution color")
                    #expect(ExtensionUIStyle.icon(m.icon) == Theme.Icon.error, "\(d.name): the warning icon")
                } else {
                    #expect(m.tone == nil && m.icon == nil, "\(d.name): not tripped, no tone or icon")
                    #expect(ExtensionUIStyle.ink(m.tone) == .primary)
                }
            }
            // The trend's spark: from two readings, filled, caution when tripped.
            if d.watch.display == "trend", d.readings.count >= 2 {
                let spark = try #require(m.spark, "\(d.name): a trend from two readings has a spark")
                #expect(spark.fill == true && spark.values.count == d.readings.count)
                #expect(ExtensionSparkLine.color(spark) == (d.tripped ? Theme.caution : Color.secondary))
            } else {
                #expect(m.spark == nil, "\(d.name): no spark")
            }
            // A table's rows after the metric, with its columns; none when there are no rows.
            if d.watch.display == "table", let reading = d.readings.last, case .object(let r) = reading,
               case .array(let rows)? = r["rows"], !rows.isEmpty {
                guard s.items.count == 2, case .table(let t) = s.items[1] else { Issue.record("\(d.name): a table after the metric"); continue }
                #expect(t.key == "persona.rows")
                #expect(t.columns.map(\.title) == d.watch.columns, "\(d.name): the watch's columns")
                #expect(t.rows.count == rows.count && t.rows.allSatisfy { $0.count == t.columns.count })
            } else {
                #expect(s.items.count == 1, "\(d.name): the metric alone")
            }
        }
    }

    /// The index row: the reading as its value ("—" before the first), the trend's spark, a status light's icon in its
    /// color, a tripped watch's warning in amber; an empty watch keeps the page's own icon in the accent.
    @Test func everyFixtureGlanceRowDecodesIntoTheIndexRow() throws {
        for d in try draws() {
            let row = try indexRow(d.glanceRow)
            guard case .object(let g) = d.glanceRow, case .string(let value)? = g["value"] else {
                Issue.record("\(d.name): a glance always has a value"); continue
            }
            #expect(row.value == value, "\(d.name): the row's value is the reading")
            #expect(row.badge == nil, "\(d.name): never a badge or a count")
            if d.readings.isEmpty {
                #expect(row.value == "—")
                #expect(row.icon == "money" && row.iconColor == nil, "\(d.name): the page's own icon")
            }
            if let color = row.iconColor {
                #expect(ExtensionUIStyle.color(color) != nil, "\(d.name): \(color) is a hue the app has")
                #expect(ExtensionUIStyle.icon(row.icon) != Theme.Icon.plugins)
            }
            if d.tripped, d.watch.display != "status" {
                #expect(row.icon == "warning" && row.iconColor == "amber", "\(d.name): tripped rows warn in amber")
            }
            if d.watch.display == "trend", d.readings.count >= 2 {
                let spark = try #require(row.spark)
                #expect(spark.values.count >= 2 && spark.fill == true)
            } else {
                #expect(row.spark == nil)
            }
        }
    }

    /// Drawn (as the share picture draws a View: everything laid out, no scroll views), every section has a size, a
    /// table's rows add to it, and a trend's spark adds to its metric.
    @Test func everyFixtureDrawLaysOut() throws {
        var heights: [String: CGFloat] = [:]
        for d in try draws() {
            let view = ExtensionView(title: "Page", sections: [d.section])
            let renderer = ImageRenderer(content:
                ExtensionViewStack(view: view, model: nil, link: .constant(nil))
                    .environment(\.extensionPicture, true)
                    .environment(AppModel.shared)
                    .frame(width: 390)
                    .fixedSize(horizontal: false, vertical: true))
            renderer.scale = 1
            let image = try #require(renderer.cgImage, "\(d.name) draws")
            #expect(image.width == 390 && image.height > 0, "\(d.name) has a size")
            heights[d.name] = CGFloat(image.height)
            let row = try indexRow(d.glanceRow)
            let rowImage = ImageRenderer(content:
                ExtensionItemRow(item: .row(row), model: nil, link: .constant(nil))
                    .environment(\.extensionPicture, true)
                    .environment(AppModel.shared)
                    .frame(width: 390))
            #expect(rowImage.cgImage != nil, "\(d.name)'s index row draws")
        }
        let h = { (name: String) in heights[name] ?? 0 }
        #expect(h("table_normal") > h("table_none_found"), "a table's rows are drawn under its metric")
        #expect(h("table_alert") > h("table_normal"), "one more row, a taller table")
        #expect(h("trend_normal") > h("trend_one"), "the spark is drawn under the trend's number")
    }

    // MARK: The footer's Alert me switch

    private static let page = "pg_1a2b3c4d5e6f7081"

    /// What a FakeHub route saw, on the main actor (a route cannot mutate a test's local variables).
    @MainActor private final class Seen {
        var model: ExtensionViewModel?
        var shown: [String?] = []
    }

    private static func footer(alert on: Bool) -> String {
        #"""
        {"view":{"title":"Elm Street rent","sections":[{"key":"persona.footer","items":[
          {"type":"text","key":"persona.updated","text":"Checked today 08:02 · next check today 20:00","style":"caption"},
          {"type":"button","key":"persona.refresh","title":"Refresh now","action":{"id":"persona.refresh"}},
          {"type":"button","key":"persona.pause","title":"Pause updates","action":{"id":"persona.pause"}},
          {"type":"row","key":"persona.alert","title":"Alert me","subtitle":"Below 2,000 USD",
           "control":{"kind":"switch","name":"on","on":\#(on),"action":{"id":"persona.alert"}}}]}]}}
        """#
    }

    private func alertControl(_ model: ExtensionViewModel) throws -> ExtensionRowControl {
        for s in model.view?.sections ?? [] {
            for case .row(let r) in s.items where r.key == "persona.alert" { return try #require(r.control) }
        }
        throw APIError(status: 0, message: "no Alert me row")
    }

    /// Turning Alert me on sends persona.alert with on=true and shows on at once, before the hub answers; the hub's
    /// answer then says what it is. Turning it off sends on=false.
    @Test func theAlertSwitchSendsItsValueAndShowsItAtOnce() async throws {
        let hub = FakeHub()
        var api = hub.client
        api.extensionsAPI = true
        let path = api.extensionPath("/\(DashboardsUI.id)/-/ui/\(Self.page)")
        let seen = Seen()
        hub.on("POST", path) { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: try #require(req.body))
            guard case .string(let action)? = body["action"] else { return Data(Self.footer(alert: false).utf8) }
            #expect(action == "persona.alert")
            seen.shown.append(seen.model?.controlValues["on"])
            return Data(Self.footer(alert: body["values"]?["on"] == .string("true")).utf8)
        }
        let model = ExtensionViewModel(plugin: DashboardsUI.id, ui: Self.page, context: .here().forPage(), deps: .init(api: { api }))
        seen.model = model
        await model.load()
        let off = try alertControl(model)
        #expect(off.on == false && off.wire == "false")

        let turnedOn = await model.change(off, to: "true")
        #expect(turnedOn.error == nil)
        #expect(seen.shown == ["true"], "on is shown while the request is on its way")
        #expect(try alertControl(model).on == true, "the hub's answer draws it on")
        #expect(model.controlValues["on"] == nil, "and from then on the hub's value is shown")

        let turnedOff = await model.change(try alertControl(model), to: "false")
        #expect(turnedOff.error == nil)
        #expect(seen.shown == ["true", "false"])
        #expect(try alertControl(model).on == false)
        let sent = hub.sent("POST", path).compactMap { $0.body }.compactMap { try? JSON.decoder.decode(JSONValue.self, from: $0) }
            .filter { $0["action"] == .string("persona.alert") }
        #expect(sent.map { $0["values"]?["on"] } == [.string("true"), .string("false")], "on=true, then on=false")
    }

    /// A refused change puts the old value back and says why.
    @Test func aRefusedAlertChangePutsTheOldValueBack() async throws {
        let hub = FakeHub()
        var api = hub.client
        api.extensionsAPI = true
        let path = api.extensionPath("/\(DashboardsUI.id)/-/ui/\(Self.page)")
        let seen = Seen()
        hub.on("POST", path) { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: try #require(req.body))
            guard body["action"] == .string("persona.alert") else { return Data(Self.footer(alert: false).utf8) }
            seen.shown.append(seen.model?.controlValues["on"])
            throw APIError(status: 500, message: "synthetic failure")
        }
        let model = ExtensionViewModel(plugin: DashboardsUI.id, ui: Self.page, context: .here().forPage(), deps: .init(api: { api }))
        seen.model = model
        await model.load()
        let control = try alertControl(model)
        let outcome = await model.change(control, to: "true")
        #expect(seen.shown == ["true"], "shown at once")
        #expect(outcome.error != nil, "the failure is said")
        #expect(model.controlValues["on"] == nil, "the user's value is dropped")
        #expect(try alertControl(model).wire == "false", "off again")
    }

    // MARK: Settings leaves out the hub's own Overview cards

    /// Overview cards of an id that starts with "-" (Dashboards' watch cards, `g_…`, which a hub lists only to the web)
    /// are never drawn in Settings home or Settings › Extensions, and no model is made for them; an extension's card is
    /// drawn as before, and Dashboards' places stay in the sidebar.
    @Test func settingsLeavesOutReservedOverviewCards() async throws {
        let hub = FakeHub()
        var api = hub.client
        api.extensionsAPI = true
        hub.on("GET", api.extensionPath("/-/ui"), json: #"""
        {"ui":[{"extension":"example-gateway","id":"today","title":"Today's usage","placement":"overview_card","size":"compact"},
               {"extension":"-pages","id":"g_1a2b3c4d5e6f7081","title":"Elm Street rent","placement":"overview_card","size":"compact","opens":"pg_1a2b3c4d5e6f7081"},
               {"extension":"-future-reserved","id":"card","title":"Reserved","placement":"overview_card"},
               {"extension":"example-gateway","id":"usage","title":"Usage","placement":"nav_page","icon":"chart"},
               {"extension":"-pages","id":"index","title":"Dashboards","placement":"nav_page","icon":"chart"}]}
        """#)
        let slots = ExtensionSlots(deps: .init(api: { api }), features: { ["extension_ui_slots"] })
        await slots.reload()
        #expect(slots.cards.map(\.scopedID) == ["example-gateway/today"], "only the extension's own card")
        #expect(slots.cardPages.map(\.plugin) == ["example-gateway"], "no model for the reserved cards")
        #expect(slots.pages.map(\.scopedID) == ["example-gateway/usage", "-pages/index"], "the sidebar keeps Dashboards")
        #expect(slots.extensionPages.map(\.scopedID) == ["example-gateway/usage"])
    }
}
