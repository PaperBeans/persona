import XCTest

/// Dashboards (#15017) on the iPhone and the iPad, against scripts/mock/mock_hub.py with `MOCK_DASHBOARDS=1` (the
/// switch on, Morning run pinned): `TEST_RUNNER_PERSONA_DASHBOARDS_HUB=http://127.0.0.1:<port>`. The drawer (the iPad's
/// sidebar) lists the Dashboards place and the pinned page; the pinned page opens with its View's title and the
/// ownership line under it; a tap answers Received; Settings › Assistant has the switch. `TEST_RUNNER_PERSONA_SHOTS=<dir>`
/// keeps the screenshots, named by `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` (light|dark: set the look itself with
/// `xcrun simctl ui <udid> appearance`, the app on Match System) and `TEST_RUNNER_PERSONA_UI_LANGUAGE` (en|zh-Hans|ja).
final class DashboardsUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var language: String { env["PERSONA_UI_LANGUAGE"] ?? "zh-Hans" }
    private var appearance: String { env["PERSONA_SHOTS_APPEARANCE"] ?? "light" }
    private static let pinned = "pg_3f9a1c0d5e7b2a41"

    private var words: (title: String, ownership: String, done: String, received: String, switchTitle: String) {
        switch language {
        case "en": ("Morning run", "Kept up to date by Persona", "Done today", "Received", "Let Persona make dashboards")
        case "ja": ("朝ラン", "Persona が更新しています", "今日は完了", "受け取りました", "Persona にダッシュボードを作らせる")
        default: ("晨跑打卡", "由 Persona 维护", "今天完成", "已收到", "让 Persona 做仪表盘")
        }
    }

    override func setUp() { continueAfterFailure = false }

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub = env["PERSONA_DASHBOARDS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_DASHBOARDS=1 (PERSONA_DASHBOARDS_HUB)") }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-AppleLanguages", "(\(language))", "-ui.scheme", "system"] + args
        app.launchPinned(navigation: false)
        // The app follows the look with its icon; the first launch after a change has the system's notice over it.
        let notice = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch
        if notice.waitForExistence(timeout: 4) { notice.buttons.firstMatch.tap() }
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone"
        try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "dashboards-\(name)-\(device)-\(language)-\(appearance).png"))
    }

    /// The drawer (the iPad's sidebar) lists Dashboards and the pinned page; the page opens with its View's title and
    /// the ownership line under it, and a tap on its button answers Received.
    @MainActor func testThePinnedPageOpensFromTheSidebar() throws {
        let iPad = UIDevice.current.userInterfaceIdiom == .pad
        let app = try launch(["-tab", "assistant"] + (iPad ? [] : ["-openAssistantSidebar", "YES"]))
        let index = app.descendants(matching: .any)["sidebar-extension--pages-index"].firstMatch
        let notice = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch
        for _ in 0..<15 where !index.waitForExistence(timeout: 2) {
            if notice.exists { notice.buttons.firstMatch.tap() } // a late icon notice over the drawer
        }
        XCTAssertTrue(index.waitForExistence(timeout: 5), "no Dashboards place:\n\(app.debugDescription)")
        let row = app.descendants(matching: .any)["sidebar-extension--pages-\(Self.pinned)"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no pinned page in the sidebar")
        sleep(1) // the drawer settles
        shot(app, "sidebar")

        row.tap()
        let title = app.navigationBars.staticTexts[words.title].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 20), "the page's header is not its View's title:\n\(app.debugDescription)")
        XCTAssertFalse(app.navigationBars.staticTexts[Self.pinned].exists, "the raw page id is in the header")
        XCTAssertTrue(app.staticTexts[words.ownership].waitForExistence(timeout: 5), "no ownership line:\n\(app.debugDescription)")
        sleep(1) // the chart's marks settle
        shot(app, "page")

        let done = app.buttons[words.done].firstMatch
        for _ in 0..<6 where !(done.exists && done.isHittable) { app.swipeUp() }
        XCTAssertTrue(done.waitForExistence(timeout: 5), "no page button:\n\(app.debugDescription)")
        done.tap()
        XCTAssertTrue(app.staticTexts[words.received].waitForExistence(timeout: 5), "the tap was not answered Received")
    }

    /// Settings › Assistant shows the switch, on (the mock's state), with its footnote.
    @MainActor func testTheSwitchInSettings() throws {
        let app = try launch(["-tab", "settings", "-openAssistantPage", "assistant"])
        let toggle = app.descendants(matching: .any)["assistant-dashboards"].firstMatch
        for _ in 0..<12 where !(toggle.exists && toggle.isHittable) { app.swipeUp() }
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), "no Dashboards switch:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts[words.switchTitle].exists || toggle.label.contains(words.switchTitle))
        // The switch and its footnote in the middle of the screen: slow drags, no fling past them.
        let window = app.windows.firstMatch
        func drag(_ from: CGFloat, _ to: CGFloat) {
            window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: from))
                .press(forDuration: 0.1, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: to)),
                       withVelocity: .slow, thenHoldForDuration: 0.3)
        }
        for _ in 0..<6 where toggle.frame.midY > window.frame.height * 0.55 { drag(0.7, 0.5) }
        for _ in 0..<6 where toggle.frame.midY < window.frame.height * 0.3 { drag(0.4, 0.6) }
        XCTAssertTrue(toggle.isHittable)
        sleep(1)
        shot(app, "settings")
    }
}
