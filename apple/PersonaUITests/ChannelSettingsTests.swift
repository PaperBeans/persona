import XCTest

/// Settings › Channels (docs/extensions/channels.md): the Channels row before Connectors, the list in its three sections, WeChat's page
/// (bound: its account, Send Messages To, its features), binding a channel with a token a step at a time, the Feishu bot's
/// pairing code, and 「来自 微信」 under the main conversation's first message. Against scripts/mock/mock_hub.py with
/// MOCK_CHANNELS=1 MOCK_EXTENSIONS=1: `TEST_RUNNER_PERSONA_CHANNELS_HUB=http://127.0.0.1:<port>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the
/// screenshots, named by `TEST_RUNNER_PERSONA_CHANNELS_APPEARANCE` (light / dark: set with `simctl ui … appearance`).
final class ChannelSettingsTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let look = env["PERSONA_CHANNELS_APPEARANCE"] ?? "light"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "channels-\(name)-\(look).png"))
    }

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub = env["PERSONA_CHANNELS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_CHANNELS=1 (PERSONA_CHANNELS_HUB)") }
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            for label in ["允许", "Allow"] where alert.buttons[label].exists { alert.buttons[label].tap(); return true }
            return false
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
        return app
    }

    private func text(_ app: XCUIApplication, containing s: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    @MainActor func testTheSettingsEntries() throws {
        let app = try launch(["-tab", "settings"])
        let settings = app.descendants(matching: .any)["settings-list"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 15))
        let assistant = app.buttons["settings-assistant"]
        for _ in 0..<8 where !assistant.isHittable { settings.swipeUp() }
        XCTAssertTrue(assistant.exists, "助手 settings must remain")

        let entry = app.buttons["settings-channels"]
        for _ in 0..<8 where !entry.isHittable { settings.swipeUp() }
        XCTAssertTrue(entry.exists, "渠道 must be on settings itself")
        let connectors = app.staticTexts["连接器"]
        XCTAssertTrue(connectors.exists, "no 连接器 heading")
        XCTAssertLessThan(entry.frame.maxY, connectors.frame.minY, "渠道 must precede 连接器")
        shot(app, "settings")

        let about = app.buttons["settings-about"]
        for _ in 0..<12 where !about.isHittable { settings.swipeUp() }
        XCTAssertTrue(about.exists)
        about.tap()
        let diagnostics = app.descendants(matching: .any)["settings-diagnostics"].firstMatch
        XCTAssertTrue(diagnostics.waitForExistence(timeout: 10))
        diagnostics.tap()
        let build = text(app, containing: "构建")
        XCTAssertTrue(text(app, containing: "版本").exists && build.exists, "About must keep version and build under Diagnostics")
        XCTAssertFalse(app.buttons["settings-about-persona"].exists)
        XCTAssertFalse(text(app, containing: "认识 ").exists, "the duplicate profile entry must be gone")
        shot(app, "about")
    }

    @MainActor func testExtensionsNoLongerContainChannels() throws {
        let app = try launch(["-tab", "settings", "-openExtensions", "YES"])
        XCTAssertTrue(app.buttons["plugin-example-notes"].waitForExistence(timeout: 15), "no extension list")
        let list = app.descendants(matching: .any)["extensions-list"].firstMatch
        XCTAssertTrue(list.exists)
        XCTAssertFalse(list.buttons["settings-channels"].exists)
        XCTAssertFalse(list.buttons["plugins-channels"].exists)
        shot(app, "extensions")
    }

    @MainActor func testTheListAndAChannel() throws {
        let app = try launch(["-tab", "settings"])
        let entry = app.buttons["settings-channels"]
        for _ in 0..<8 where !entry.isHittable { app.swipeUp() }
        XCTAssertTrue(entry.waitForExistence(timeout: 15), "no 渠道 row:\n\(app.debugDescription)")
        // The counts follow what the other tests bound on the same mock: the words are checked here, the counts in
        // ChannelsModelTests.
        XCTAssertTrue(text(app, containing: "个已连接 · 1 个出错").waitForExistence(timeout: 10), "the row's summary:\n\(app.debugDescription)")
        shot(app, "entry")
        entry.tap()

        let wechat = app.buttons["channel-wechat"]
        XCTAssertTrue(wechat.waitForExistence(timeout: 15), "no channel list:\n\(app.debugDescription)")
        for id in ["app", "feishu", "examplechat", "examplebroken", "lark_agent"] {
            XCTAssertTrue(app.buttons["channel-\(id)"].exists, "no \(id)")
        }
        XCTAssertTrue(text(app, containing: "令牌失效了").exists, "the broken one says why")
        shot(app, "list")
        for _ in 0..<4 where !app.buttons["channel-examplemarket"].exists { app.swipeUp() }
        XCTAssertTrue(app.buttons["channel-examplemarket"].exists, "no 可以安装:\n\(app.debugDescription)")
        shot(app, "list-market")
        for _ in 0..<4 { app.swipeDown() }

        wechat.tap()
        XCTAssertTrue(app.buttons["channel-unbind"].waitForExistence(timeout: 10), "bound: 解除绑定:\n\(app.debugDescription)")
        XCTAssertTrue(text(app, containing: "微信用户 a1b2c3").exists)
        XCTAssertTrue(app.switches["channel-enabled"].exists)
        shot(app, "wechat")
        app.swipeUp()
        XCTAssertTrue(app.switches["channel-feature-asks"].waitForExistence(timeout: 5), "no features:\n\(app.debugDescription)")
        shot(app, "wechat-lower")
    }

    /// A channel bound with a token: Bind, the field, a wrong token said beside it, the right one binds it.
    @MainActor func testBindWithAToken() throws {
        let app = try launch(["-tab", "settings", "-openChannel", "examplechat"])
        let start = app.buttons["connector.signIn"]
        XCTAssertTrue(start.waitForExistence(timeout: 15), "no 绑定:\n\(app.debugDescription)")
        start.tap()
        let token = app.secureTextFields["connector.field.token"]
        XCTAssertTrue(token.waitForExistence(timeout: 10), "no token field:\n\(app.debugDescription)")
        token.tap()
        token.typeText("nope")
        app.buttons["connector.next"].tap()
        XCTAssertTrue(app.staticTexts["令牌不对"].waitForExistence(timeout: 5))
        shot(app, "bind-fields")
        token.tap()
        for _ in 0..<4 { token.typeText(XCUIKeyboardKey.delete.rawValue) }
        token.typeText("example-token")
        app.buttons["connector.next"].tap()
        // Bound: the keyboard goes and the page is back at its top (it had scrolled to the field).
        XCTAssertTrue(app.buttons["channel-test"].waitForExistence(timeout: 10) || app.buttons["channel-unbind"].exists, "bound:\n\(app.debugDescription)")
        for _ in 0..<4 where !app.buttons["channel-unbind"].isHittable { app.swipeDown() }
        XCTAssertTrue(app.buttons["channel-unbind"].exists, "bound: 解除绑定:\n\(app.debugDescription)")
        XCTAssertTrue(text(app, containing: "示例机器人").waitForExistence(timeout: 5), "the account:\n\(app.debugDescription)")
        shot(app, "bound")
        // The token's field has gone: iOS must not offer to save the token as a password (#1598).
        PasswordPrompt.assertNone(app, "a channel's token")
    }

    /// The Feishu bot unbound, then bound again with its pairing code (the mock pairs it after a few asks).
    @MainActor func testThePairingCode() throws {
        let app = try launch(["-tab", "settings", "-openChannel", "feishu"])
        let unbind = app.buttons["channel-unbind"]
        XCTAssertTrue(unbind.waitForExistence(timeout: 15), "no 解除绑定:\n\(app.debugDescription)")
        shot(app, "feishu")
        unbind.tap()
        // The dialog's button, not the row's.
        let confirm = app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != %@", "解除绑定", "channel-unbind")).firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "no confirm:\n\(app.debugDescription)")
        confirm.tap()
        let start = app.buttons["connector.signIn"]
        XCTAssertTrue(start.waitForExistence(timeout: 10), "unbound: 绑定:\n\(app.debugDescription)")
        start.tap()
        XCTAssertTrue(app.staticTexts["connector.pairCode"].waitForExistence(timeout: 10), "no pairing code:\n\(app.debugDescription)")
        XCTAssertTrue(text(app, containing: "482913").exists)
        shot(app, "bind-pair")
        XCTAssertTrue(app.buttons["channel-unbind"].waitForExistence(timeout: 45), "paired:\n\(app.debugDescription)")
    }

    /// 「来自 微信」 under the main conversation's first message (its client_msg_id is wechat-…).
    @MainActor func testWhereAMessageCameFrom() throws {
        let app = try launch(["-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"])
        let mark = app.descendants(matching: .any)["message-channel"]
        for _ in 0..<6 where !mark.waitForExistence(timeout: 3) { app.swipeDown() }
        XCTAssertTrue(mark.exists, "no 来自 微信:\n\(app.debugDescription)")
        XCTAssertTrue(text(app, containing: "来自 微信").exists)
        shot(app, "source")
    }
}
