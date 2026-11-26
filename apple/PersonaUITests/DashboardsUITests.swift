import XCTest

/// Dashboards (#15017) on the iPhone and the iPad, against scripts/mock/mock_hub.py with `MOCK_DASHBOARDS=1` (the
/// switch on, Morning run pinned): `TEST_RUNNER_PERSONA_DASHBOARDS_HUB=http://127.0.0.1:<port>`. The drawer (the iPad's
/// sidebar) lists the Dashboards place and the pinned page; the pinned page opens with its View's title and the
/// ownership line under it; a tap answers Received; Settings › Assistant has the switch. `TEST_RUNNER_PERSONA_SHOTS=<dir>`
/// keeps the screenshots, named by `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` (light|dark: set the look itself with
/// `xcrun simctl ui <udid> appearance`, the app on Match System) and `TEST_RUNNER_PERSONA_UI_LANGUAGE` (en|zh-Hans|ja).
///
/// Phase 2 (#13020), "Watch pages" below: those tests set the mock's mode to `watch` themselves (`GET /mock/dashboards`),
/// so one mock started with any mode serves them all; `testTheSidebarAsItIs` captures the sidebar in the mode the mock
/// was started with, for comparing with origin/main.
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
        let app = try launch(["-tab", "assistant"])
        openDrawer(app) // by its button: opened at launch, its rows are sometimes missing from the accessibility tree
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

    // MARK: Watch pages (#13020)
    //
    // The mock's watch mode: four kept watch pages, a number (pinned), a status light, a table and a small trend, each
    // drawn empty, normal and tripped (`?watch=all&state=…`, spec/fixtures/dashboards/watch.json "draws"), and phase 1's
    // Morning run. The Dashboards index shows each page's reading in its row; the footer has Refresh now (Checking now…
    // until `?advance=1`), Pause / Resume updates (Updates paused) and the Alert me switch.

    private var iPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }
    /// The watch number page, pinned in the mock's watch mode.
    private static let number = "pg_1a2b3c4d5e6f7081"

    /// The mock's words (scripts/mock/dashboards_mock.py `WORDS`) in the run's language.
    private struct Words {
        var index: String
        var titles: [String: String] // display → page title
        var values: [String: [String: String]] // state → display → the reading as the row and the metric show it
        var refresh, checking, pause, resume, paused, alertMe: String
    }

    private var watchWords: Words {
        switch language {
        case "en":
            Words(index: "Dashboards",
                  titles: ["number": "Elm Street rent", "status": "Shop sites", "table": "Elm Street listings", "trend": "Morning weight"],
                  values: ["empty": ["number": "—", "status": "—", "table": "—", "trend": "—"],
                           "normal": ["number": "2,150 USD", "status": "All 3 sites up", "table": "2 rows", "trend": "77.9 kg"],
                           "alert": ["number": "1,985.5 USD", "status": "Shop site down", "table": "3 rows", "trend": "80.3 kg"]],
                  refresh: "Refresh now", checking: "Checking now…", pause: "Pause updates", resume: "Resume updates",
                  paused: "Updates paused", alertMe: "Alert me")
        case "ja":
            Words(index: "ダッシュボード",
                  titles: ["number": "エルム通りの家賃", "status": "ショップのサイト", "table": "新着の賃貸物件", "trend": "朝の体重"],
                  values: ["empty": ["number": "—", "status": "—", "table": "—", "trend": "—"],
                           "normal": ["number": "2,150 ドル", "status": "3 サイトすべて稼働中", "table": "2 件", "trend": "77.9 kg"],
                           "alert": ["number": "1,985.5 ドル", "status": "ショップサイトが停止中", "table": "3 件", "trend": "80.3 kg"]],
                  refresh: "今すぐ更新", checking: "確認中…", pause: "更新を一時停止", resume: "更新を再開",
                  paused: "更新は一時停止中", alertMe: "条件に達したら通知")
        default:
            Words(index: "仪表盘",
                  titles: ["number": "榆树街房租", "status": "店铺网站", "table": "低价房源", "trend": "晨间体重"],
                  values: ["empty": ["number": "—", "status": "—", "table": "—", "trend": "—"],
                           "normal": ["number": "2,150 美元", "status": "3 个网站均正常", "table": "2 条记录", "trend": "77.9 公斤"],
                           "alert": ["number": "1,985.5 美元", "status": "店铺网站已离线", "table": "3 条记录", "trend": "80.3 公斤"]],
                  refresh: "立即刷新", checking: "正在检查…", pause: "暂停更新", resume: "恢复更新",
                  paused: "更新已暂停", alertMe: "触发时提醒")
        }
    }

    private static let displays = ["number", "status", "table", "trend"]
    private static let states = ["empty", "normal", "alert"]

    // MARK: Watch pages: the mock

    private func hub() throws -> String {
        guard let hub = env["PERSONA_DASHBOARDS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_DASHBOARDS (PERSONA_DASHBOARDS_HUB)") }
        return hub
    }

    /// `GET /mock/dashboards?<query>`: the mock's state after it.
    @discardableResult private func mock(_ query: String) throws -> [String: Any] {
        let url = try XCTUnwrap(URL(string: try hub() + "/mock/dashboards" + (query.isEmpty ? "" : "?" + query)))
        let data = try Data(contentsOf: url)
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    /// The watch pages afresh: mode watch (four kept watch pages and Morning run), every display in `state`.
    private func reset(_ state: String) throws {
        let body = try mock("mode=watch")
        guard body["mode"] as? String == "watch" else { throw XCTSkip("needs mock_hub.py with the watch mode of MOCK_DASHBOARDS") }
        try mock("watch=all&state=" + state)
    }

    private func page(_ id: String) throws -> [String: Any] {
        let pages = try mock("")["pages"] as? [[String: Any]] ?? []
        return try XCTUnwrap(pages.first { $0["id"] as? String == id })
    }

    // MARK: Watch pages: the app

    /// The app on the Assistant with the sidebar out (the iPhone's drawer; the iPad's is always there).
    @MainActor private func launchWatch() throws -> XCUIApplication {
        let app = try launch(["-tab", "assistant"])
        openDrawer(app)
        return app
    }

    /// The iPhone's drawer, opened with its button as a person does. Opened at launch (`-openAssistantSidebar`), it is
    /// sometimes drawn with none of its rows in the accessibility tree for as long as a test waits (phase 1's test
    /// meets it too, on iOS 27.0 simulators); then it is closed and opened again.
    @MainActor private func openDrawer(_ app: XCUIApplication) {
        guard !iPad else { return }
        let button = app.buttons["assistant-sidebar-button"].firstMatch
        let rows = drawerRows(app)
        for _ in 0..<3 where !rows.exists {
            if button.waitForExistence(timeout: 20), button.isHittable { button.tap() }
            if rows.waitForExistence(timeout: 5) { break }
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap() // closes it
            sleep(1)
        }
        XCTAssertTrue(rows.exists, "the drawer did not open:\n\(app.debugDescription)")
    }

    /// One of the drawer's (the sidebar's) rows: they are named `sidebar-…` in every language.
    @MainActor private func drawerRows(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'sidebar-'")).firstMatch
    }

    @MainActor private func any(_ id: String, _ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)[id].firstMatch
    }

    /// An element whose spoken label has `text` in it (a metric and a row read as one element).
    @MainActor private func containing(_ text: String, _ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// Opens a place of the sidebar (the drawer on the iPhone): `index` or a pinned page.
    @MainActor private func openPlace(_ ui: String, _ app: XCUIApplication) {
        let row = any("sidebar-extension--pages-\(ui)", app)
        let notice = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch
        for _ in 0..<15 where !row.waitForExistence(timeout: 2) {
            if notice.exists { notice.buttons.firstMatch.tap() } // a late icon notice over the drawer
        }
        XCTAssertTrue(row.waitForExistence(timeout: 5), "no \(ui) place in the sidebar:\n\(app.debugDescription)")
        row.tap()
    }

    /// Back to the page under this one (UIKit's back button, labelled with that page).
    @MainActor private func back(_ app: XCUIApplication) {
        let system = app.navigationBars.buttons.matching(identifier: "BackButton").allElementsBoundByIndex.filter(\.isHittable)
        if let b = system.last { b.tap(); return }
        app.navigationBars.buttons.matching(NSPredicate(format: "label == %@", watchWords.index)).firstMatch.tap()
    }

    /// Waits for `text` in an element, drawing the page again (pull to refresh) if the mock's push has not done it.
    @MainActor private func waitFor(_ text: String, _ app: XCUIApplication, _ what: String) {
        let e = containing(text, app)
        if !e.waitForExistence(timeout: 8) {
            app.swipeDown()
            _ = e.waitForExistence(timeout: 8)
        }
        XCTAssertTrue(e.exists, "\(what): no \"\(text)\":\n\(app.debugDescription)")
    }

    /// Scrolls until `e` is on screen and can be tapped.
    @MainActor private func reveal(_ e: XCUIElement, _ app: XCUIApplication) {
        for _ in 0..<8 where !(e.exists && e.isHittable) { app.swipeUp() }
        XCTAssertTrue(e.waitForExistence(timeout: 5) && e.isHittable, "can't reach \(e):\n\(app.debugDescription)")
    }

    // MARK: Watch pages

    /// Every display (number, status light, table, trend) empty, normal and tripped: the page's first section, and the
    /// Dashboards index with each page's reading in its row. Each page is shown while the mock changes its reading,
    /// so the redraw an `extension_ui` invalidation asks for is what is captured.
    @MainActor func testEveryWatchDisplayInEachState() throws {
        try reset("empty")
        let app = try launchWatch()
        openPlace("index", app)
        let w = watchWords
        XCTAssertTrue(app.navigationBars.staticTexts[w.index].waitForExistence(timeout: 20), "the index:\n\(app.debugDescription)")
        for state in Self.states {
            try mock("watch=all&state=" + state)
            for display in Self.displays {
                waitFor(try XCTUnwrap(w.values[state]?[display]), app, "the \(display) row, \(state)")
            }
            sleep(1)
            shot(app, "index-\(state)")
        }
        for display in Self.displays {
            try mock("watch=all&state=empty")
            let title = try XCTUnwrap(w.titles[display])
            let row = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", title)).firstMatch
            reveal(row, app)
            row.tap()
            XCTAssertTrue(app.navigationBars.staticTexts[title].waitForExistence(timeout: 20), "the \(display) page:\n\(app.debugDescription)")
            for state in Self.states {
                try mock("watch=all&state=" + state)
                waitFor(try XCTUnwrap(w.values[state]?[display]), app, "the \(display) page, \(state)")
                sleep(1) // the number's change settles
                shot(app, "page-\(display)-\(state)")
            }
            back(app)
            XCTAssertTrue(app.navigationBars.staticTexts[w.index].waitForExistence(timeout: 10), "back to the index:\n\(app.debugDescription)")
        }
    }

    /// The footer of the number page (pinned): Checked … · next check, Refresh now; Checking now… until the run writes
    /// a reading; Pause updates leaves Updates paused and Resume updates, without Refresh now; the Alert me switch sends
    /// persona.alert with on=true and then on=false, shown at once.
    @MainActor func testTheWatchFooter() throws {
        try reset("normal")
        let app = try launchWatch()
        openPlace(Self.number, app)
        let w = watchWords
        let title = try XCTUnwrap(w.titles["number"])
        XCTAssertTrue(app.navigationBars.staticTexts[title].waitForExistence(timeout: 20), "the page:\n\(app.debugDescription)")
        waitFor(try XCTUnwrap(w.values["normal"]?["number"]), app, "the reading")

        let refresh = app.buttons[w.refresh].firstMatch
        reveal(refresh, app)
        sleep(1)
        shot(app, "footer")

        refresh.tap()
        XCTAssertTrue(app.staticTexts[w.checking].waitForExistence(timeout: 5), "no Checking now…:\n\(app.debugDescription)")
        sleep(1)
        shot(app, "footer-checking")
        XCTAssertEqual(try page(Self.number)["refreshing"] as? Bool, true)
        try mock("advance=1") // the run writes its reading
        waitFor(try XCTUnwrap(w.values["alert"]?["number"]), app, "the run's reading")
        let checking = app.staticTexts[w.checking].firstMatch
        for _ in 0..<10 where checking.exists { sleep(1) } // the toast goes too
        XCTAssertFalse(checking.exists, "Checking now… stays after the reading")

        let pause = app.buttons[w.pause].firstMatch
        reveal(pause, app)
        pause.tap()
        let resume = app.buttons[w.resume].firstMatch
        XCTAssertTrue(resume.waitForExistence(timeout: 5), "no Resume updates:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts[w.paused].exists, "no Updates paused")
        XCTAssertFalse(app.buttons[w.refresh].exists, "Refresh now while paused")
        XCTAssertEqual(try page(Self.number)["paused"] as? Bool, true)
        reveal(resume, app)
        sleep(2) // the toast has gone
        shot(app, "footer-paused")
        resume.tap()
        XCTAssertTrue(app.buttons[w.pause].waitForExistence(timeout: 5), "no Pause updates after Resume")
        XCTAssertTrue(app.buttons[w.refresh].waitForExistence(timeout: 5), "no Refresh now after Resume")
        XCTAssertEqual(try page(Self.number)["paused"] as? Bool, false)

        let alert = app.switches["plugin-ui-switch-on"].firstMatch
        reveal(alert, app)
        XCTAssertEqual(alert.value as? String, "0")
        XCTAssertTrue(alert.label.contains(w.alertMe), "the switch is labelled by its row: \(alert.label)")
        alert.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap() // the switch, not the label
        XCTAssertEqual(alert.value as? String, "1", "on at once")
        let on = expectation(for: NSPredicate { _, _ in (try? self.page(Self.number)["alert_on"] as? Bool) == true }, evaluatedWith: nil)
        wait(for: [on], timeout: 10)
        sleep(2) // the toast has gone
        shot(app, "footer-alert-on")
        alert.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        XCTAssertEqual(alert.value as? String, "0", "off at once")
        let off = expectation(for: NSPredicate { _, _ in (try? self.page(Self.number)["alert_on"] as? Bool) == false }, evaluatedWith: nil)
        wait(for: [off], timeout: 10)
    }

    /// One page in the run's language (zh-Hans and ja on top of en): the number page tripped, and its footer.
    @MainActor func testAWatchPageInTheRunsLanguage() throws {
        try reset("alert")
        let app = try launchWatch()
        openPlace(Self.number, app)
        let w = watchWords
        XCTAssertTrue(app.navigationBars.staticTexts[try XCTUnwrap(w.titles["number"])].waitForExistence(timeout: 20),
                      "the page:\n\(app.debugDescription)")
        waitFor(try XCTUnwrap(w.values["alert"]?["number"]), app, "the reading")
        sleep(1)
        shot(app, "lang-page")
        let alert = app.switches["plugin-ui-switch-on"].firstMatch
        reveal(alert, app)
        XCTAssertTrue(app.buttons[w.refresh].exists && app.buttons[w.pause].exists, "the footer's buttons")
        sleep(1)
        shot(app, "lang-footer")
    }

    /// The drawer (the iPad's sidebar) as it is: for comparing with origin/main, with phase 1's pages
    /// (`MOCK_DASHBOARDS=1`) or the switch off (`off`). Nothing is set up: the mock runs in the mode to compare.
    @MainActor func testTheSidebarAsItIs() throws {
        let mode = (try mock("")["mode"] as? String) ?? "?"
        let app = try launchWatch()
        XCTAssertTrue(drawerRows(app).waitForExistence(timeout: 20), "no sidebar:\n\(app.debugDescription)")
        if mode != "off" && mode != "empty" { XCTAssertTrue(any("sidebar-extension--pages-index", app).waitForExistence(timeout: 20)) }
        sleep(3) // the drawer and the avatar settle
        shot(app, "sidebar-\(mode)")
    }
}
