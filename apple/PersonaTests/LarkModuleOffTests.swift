import Foundation
import PersonaModuleAPI
import PersonaWire
import Testing
@testable import Persona

/// The iPhone follows the hub's `module.lark` flag for the Lark bot (#15995; the owner's rule, #13430): with the module
/// off the bot's entry points are not offered and its channel is not listed or counted; what the Lark extension offers
/// (`LarkFeatures`) is untouched. With the module on, or a hub that publishes no module flags, nothing changes.
@MainActor struct LarkModuleOffTests {
    private static let list = #"""
    {"channels":[
     {"id":"app","name":"Persona","kind":"app","source":"builtin","installed":true,"enabled":true,"state":"connected","bind":"none",
      "read_only":true,"surfaces":["iPhone"],"capabilities":{"format":"markdown","asks":"buttons"}},
     {"id":"feishu","name":"Lark Bot","kind":"im","source":"builtin","installed":true,"enabled":true,"state":"error",
      "error":"not connected","bind":"code","capabilities":{},"settings_url":"settings/connectors/feishu-bot"},
     {"id":"lark_agent","name":"Lark /agent","kind":"mention","source":"builtin","installed":true,"enabled":false,"state":"disabled",
      "bind":"config","read_only":true,"capabilities":{"asks":"none"}},
     {"id":"wechat","name":"WeChat","kind":"im","source":"plugin","plugin":"wechat","installed":true,"enabled":true,"state":"connected",
      "bind":"qr","capabilities":{}}]}
    """#

    private func hub() -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/channels", json: Self.list)
        return hub
    }

    /// The one rule (`HubModules.allows`), for the Lark flag: off only with `modules` and without `module.lark`.
    @Test func theEntryFollowsTheModuleFlagAndNothingElse() {
        #expect(LarkBotEntry(hub: .everyOn) == .offered, "nothing heard, or an older hub")
        #expect(LarkBotEntry(hub: HubModules(hello: ["file_upload", "lark", "lark_attachments"])) == .offered, "no marker: an older hub")
        #expect(LarkBotEntry(hub: HubModules(hello: ["modules", "module.lark"])) == .offered)
        #expect(LarkBotEntry(hub: HubModules(hello: ["modules", "module.mail"])) == .moduleOff)
        #expect(LarkBotEntry(hub: HubModules(hello: ["modules", "lark", "lark_moments"])) == .moduleOff, "the extension's features do not offer the bot")
        #expect(LarkBotEntry(hub: HubModules(stored: ["module.voice"])) == .moduleOff, "the remembered answer, before this launch's hello")
        #expect(LarkBotEntry(hub: HubModules(stored: nil)) == .offered)
        #expect(LarkBotEntry.offered.isOffered && !LarkBotEntry.moduleOff.isOffered)
        // And the module's flag does not change what the Lark extension offers.
        #expect(LarkFeatures(hub: ["modules", "lark", "lark_moments"]) == LarkFeatures(chats: true, moments: true))
        #expect(LarkFeatures(hub: ["modules", "module.lark"]) == LarkFeatures.none)
    }

    @Test func onlyTheBotsPagesAreHidden() {
        #expect(LarkBotEntry.moduleOff.hides(channel: "feishu"))
        #expect(!LarkBotEntry.moduleOff.hides(channel: "lark_agent"), "/agent in Lark is the extension's")
        #expect(!LarkBotEntry.moduleOff.hides(channel: "wechat"))
        #expect(!LarkBotEntry.offered.hides(channel: "feishu"))
    }

    @Test func withTheModuleOffTheBotsChannelIsNotListedOrCounted() async throws {
        let hub = hub()
        let page = ChannelsModel(deps: .init(api: { hub.client }, larkBot: { .moduleOff }))
        await page.load()
        let list = try #require(page.channels)
        #expect(list.map(\.id) == ["app", "lark_agent", "wechat"])
        #expect(page.sections.flatMap(\.channels).map(\.id) == ["app", "lark_agent", "wechat"])
        #expect(page.error == nil)
        // Settings' Channels row no longer counts a bot "with errors" that nobody can fix from here.
        #expect(!ChannelText.hasProblem(list))
        #expect(ChannelText.summary(list) == "1 个已连接")
        #expect(hub.requests.map(\.path) == ["/api/v1/channels"], "the same one read; nothing is asked of the bot")
    }

    @Test func withTheModuleOnTheListIsTheHubs() async throws {
        let hub = hub()
        let page = ChannelsModel(deps: .init(api: { hub.client }, larkBot: { .offered }))
        await page.load()
        let list = try #require(page.channels)
        #expect(list.map(\.id) == ["app", "feishu", "lark_agent", "wechat"])
        #expect(ChannelText.hasProblem(list))
        #expect(ChannelText.summary(list) == "1 个已连接 · 1 个出错")
        #expect(hub.requests.map(\.path) == ["/api/v1/channels"])
        // The default (tests, previews) offers it, as before this flag was read.
        let plain = ChannelsModel(deps: .init(api: { hub.client }))
        await plain.load()
        #expect(plain.channels?.map(\.id) == ["app", "feishu", "lark_agent", "wechat"])
        let all = try JSON.decoder.decode(ChannelList.self, from: Data(Self.list.utf8)).channels
        #expect(LarkBotEntry.offered.listing(all) == all, "offered, the very list")
    }

    private struct ChannelList: Decodable { var channels: [HubChannel] }

    /// One place reads the module's flag: everything else takes `LarkBotEntry`.
    @Test func onlyLarkModuleNamesTheFlag() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        var offenders: [String] = []
        for dir in ["Persona", "Shared"] {
            let files = FileManager.default.enumerator(at: root.appendingPathComponent(dir), includingPropertiesForKeys: nil)
            while let url = files?.nextObject() as? URL {
                guard url.pathExtension == "swift", url.lastPathComponent != "LarkModule.swift" else { continue }
                let text = try String(contentsOf: url, encoding: .utf8)
                if text.contains("LarkModule.descriptor") || text.contains("\"module.lark\"") { offenders.append(url.lastPathComponent) }
            }
        }
        #expect(offenders.isEmpty)
    }
}
