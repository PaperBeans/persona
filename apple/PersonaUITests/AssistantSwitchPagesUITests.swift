import XCTest

/// Another assistant made current while a page of the one before is open (docs/assistant/managers.md "The current
/// assistant"): nothing of the one before stays on screen, and the page is the new assistant's (the app closes it;
/// opened again it lists the new one's). On an iPad only, where the sidebar's
/// switcher stays beside a page pushed on the Persona page; an iPhone's drawer is covered by it. Against
/// scripts/mock/mock_hub.py as `AssistantSwitcherUITests` (`TEST_RUNNER_PERSONA_SWITCHER_HUB`, with `MOCK_TASKS=1` for
/// the default assistant's scheduled tasks): the default assistant has a mail of its own (`homeMail`) and the scheduled
/// task `morning`; the second one (`m-3fa21c`) has another mail (`workMail`) and no scheduled task.
final class AssistantSwitchPagesUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var hub = ""
    private static let homeMail = "下周的分享能不能提前到周二？", workMail = "Can we move the design review?"

    /// Back on the default assistant's main conversation for the classes after this one (`AssistantUITestReset`).
    override class func tearDown() {
        MainActor.assumeIsolated { AssistantUITestReset.toDefaultMain(hub: ProcessInfo.processInfo.environment["PERSONA_SWITCHER_HUB"]) }
        super.tearDown()
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard UIDevice.current.userInterfaceIdiom == .pad else { throw XCTSkip("iPad only: the switcher beside a pushed page") }
        guard let h = env["PERSONA_SWITCHER_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_SWITCHER_HUB)") }
        hub = h
        XCUIDevice.shared.orientation = .portrait
    }

    /// The second assistant's inbox with one of its items open, then the default assistant picked: the item closes, and
    /// the inbox lists the default assistant's mail, not the second one's.
    @MainActor func testAfterASwitchAnOpenInboxItemIsGoneAndTheInboxIsTheNewAssistants() throws {
        let app = launch(on: "m-3fa21c", named: "工作")
        openInbox(app)
        let mail = labeled(app, Self.workMail)
        XCTAssertTrue(mail.waitForExistence(timeout: 15), "工作's mail is not in its inbox:\n\(app.debugDescription)")
        XCTAssertFalse(labeled(app, Self.homeMail).exists, "the default assistant's mail in 工作's inbox")
        mail.tap()
        let done = app.buttons["inbox.detail.done"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 10), "the item did not open:\n\(app.debugDescription)")
        shot(app, "switch-pages-inbox-item")

        pick("assistant", from: "工作", to: "助手", app)
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: done)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 10), .completed, "工作's item stayed open after switching:\n\(app.debugDescription)")

        // The inbox closed with its item (a page pushed on the Persona page): opened again, as the user would.
        if !app.buttons["inbox.more"].firstMatch.exists { openInbox(app) }
        XCTAssertTrue(labeled(app, Self.homeMail).waitForExistence(timeout: 15), "the default assistant's mail is not listed:\n\(app.debugDescription)")
        XCTAssertFalse(labeled(app, Self.workMail).exists, "工作's mail in the default assistant's inbox")
        shot(app, "switch-pages-inbox-switched")
    }

    /// The second assistant's scheduled tasks (none) open, then the default assistant picked: the page lists the default's
    /// (it closes on the switch, and is opened again).
    @MainActor func testAfterASwitchScheduledTasksAreTheNewAssistants() throws {
        let app = launch(on: "m-3fa21c", named: "工作")
        let add = app.buttons["新建定时任务"].firstMatch, task = element(app, "task-morning")
        openScheduledTasks(app)
        XCTAssertTrue(add.waitForExistence(timeout: 10), "the scheduled tasks did not open:\n\(app.debugDescription)")
        sleep(3) // its read
        XCTAssertFalse(task.exists, "the default assistant's task among 工作's")
        shot(app, "switch-pages-tasks")

        pick("assistant", from: "工作", to: "助手", app)
        sleep(2) // the pushed page's own closing
        if !add.exists { openScheduledTasks(app) }
        XCTAssertTrue(task.waitForExistence(timeout: 15), "the default assistant's task is not listed:\n\(app.debugDescription)")
        shot(app, "switch-pages-tasks-switched")
    }

    // MARK: -

    /// The app on the Persona page with `id` current: its sidebar's header names it (`name`).
    @MainActor private func launch(on id: String, named name: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-app.tab", "assistant", "-openAssistantSidebar", "YES",
                               "-openAssistantManager", id]
        app.launchPinned()
        // A fresh install asks for notifications first, and the app waits inactive (no roster) until it is answered.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 2) { springboard.buttons[label].tap() }
        XCTAssertTrue(header(app, name).waitForExistence(timeout: 30), "\(name) is not the current assistant:\n\(app.debugDescription)")
        sleep(2) // the pick's own closing of pushed pages
        return app
    }

    /// The sidebar's header while the assistant named `name` is current.
    @MainActor private func header(_ app: XCUIApplication, _ name: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "(identifier == %@ OR identifier == %@) AND label BEGINSWITH %@",
                                         "assistant-switcher", "assistant-sidebar", name)).firstMatch
    }

    /// `id` picked in the switcher's list, from the assistant named `from`: the header names `to` afterwards.
    @MainActor private func pick(_ id: String, from: String, to: String, _ app: XCUIApplication) {
        header(app, from).tap()
        let row = element(app, "assistant-switcher-row-" + id)
        XCTAssertTrue(row.waitForExistence(timeout: 10), "the list did not open:\n\(app.debugDescription)")
        row.tap()
        XCTAssertTrue(header(app, to).waitForExistence(timeout: 10), "the header does not name \(to):\n\(app.debugDescription)")
    }

    @MainActor private func openInbox(_ app: XCUIApplication) {
        let row = element(app, "sidebar-inbox")
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no Inbox in the sidebar:\n\(app.debugDescription)")
        row.tap()
        XCTAssertTrue(app.buttons["inbox.more"].firstMatch.waitForExistence(timeout: 10), "the inbox did not open:\n\(app.debugDescription)")
    }

    /// The sidebar's Scheduled Tasks row: it has no identifier of its own, so by its title, in the sidebar's column
    /// (under the Inbox row).
    @MainActor private func openScheduledTasks(_ app: XCUIApplication) {
        let inbox = element(app, "sidebar-inbox")
        XCTAssertTrue(inbox.waitForExistence(timeout: 10), "no sidebar:\n\(app.debugDescription)")
        let row = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "定时任务")).allElementsBoundByIndex
            .first { $0.exists && $0.frame.minX < inbox.frame.maxX }
        guard let row else { return XCTFail("no Scheduled Tasks in the sidebar:\n\(app.debugDescription)") }
        row.tap()
    }

    private func labeled(_ app: XCUIApplication, _ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let png = app.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name).png"))
    }
}
