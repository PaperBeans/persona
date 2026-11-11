import XCTest

/// The sidebar's assistant switcher (docs/assistant/managers.md "Clients"): what its list presents once it has gone away.
/// New Assistant asks for a name and then shows the install code, Manage Assistants… opens Settings › Assistants, and a
/// row still installing shows its install code; at the largest text size both actions can still be tapped. Against
/// scripts/mock/mock_hub.py (three installed managers and one installing by default):
/// `TEST_RUNNER_PERSONA_SWITCHER_HUB=http://127.0.0.1:<port>`. `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screens, named by
/// `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` (light / dark).
final class AssistantSwitcherUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var hub = ""

    /// Back on the default assistant's main conversation for the classes after this one (`AssistantUITestReset`).
    override class func tearDown() {
        MainActor.assumeIsolated { AssistantUITestReset.toDefaultMain(hub: ProcessInfo.processInfo.environment["PERSONA_SWITCHER_HUB"]) }
        super.tearDown()
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
        XCUIDevice.shared.orientation = .portrait
        guard let h = env["PERSONA_SWITCHER_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_SWITCHER_HUB)") }
        hub = h
    }

    @MainActor func testNewAssistantAsksForANameThenShowsItsCode() throws {
        let app = openList()
        element(app, "assistant-switcher-new").tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10), "no name alert after the list closed:\n\(app.debugDescription)")
        let field = alert.textFields.firstMatch
        field.tap()
        field.typeText("书房")
        shot(app, "switcher-new-alert")
        alert.buttons["创建"].tap()
        XCTAssertTrue(element(app, "managerCode.countdown").waitForExistence(timeout: 10), "no install code:\n\(app.debugDescription)")
        shot(app, "switcher-new-code")
    }

    @MainActor func testManageAssistantsOpensTheirSettingsPage() throws {
        let app = openList()
        element(app, "assistant-switcher-manage").tap()
        XCTAssertTrue(element(app, "managers.row.m-3fa21c").waitForExistence(timeout: 10), "Settings › Assistants did not open:\n\(app.debugDescription)")
        shot(app, "switcher-manage")
    }

    @MainActor func testARowStillInstallingShowsItsCode() throws {
        let app = openList()
        element(app, "assistant-switcher-row-m-c1e9f0").tap()
        XCTAssertTrue(element(app, "managerCode.countdown").waitForExistence(timeout: 10), "no install code:\n\(app.debugDescription)")
        shot(app, "switcher-installing-code")
    }

    /// At the largest text size New Assistant and Manage Assistants… are still whole and can be tapped: the rows scroll
    /// above them.
    @MainActor func testAtTheLargestTextSizeBothActionsCanBeTapped() throws {
        let app = openList(["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"])
        let create = element(app, "assistant-switcher-new"), manage = element(app, "assistant-switcher-manage")
        // The rows scroll on their own, down to the last one (still installing), above the two actions.
        let last = element(app, "assistant-switcher-row-m-c1e9f0")
        for _ in 0..<4 where !last.isHittable { element(app, "assistant-switcher-list").swipeUp() }
        XCTAssertTrue(last.isHittable, "the rows don't scroll to the last one:\n\(app.debugDescription)")
        XCTAssertTrue(create.isHittable && manage.isHittable, "an action out of reach:\n\(app.debugDescription)")
        let window = app.windows.firstMatch.frame
        XCTAssertTrue(window.contains(create.frame) && window.contains(manage.frame), "an action cut off by the screen")
        shot(app, "switcher-ax-actions")
        manage.tap()
        XCTAssertTrue(element(app, "managers.row.m-3fa21c").waitForExistence(timeout: 10), "Settings › Assistants did not open:\n\(app.debugDescription)")
    }

    /// Another assistant made current closes a page pushed on the Persona page (the profile here): it read the one
    /// before and would write to the next. Regular width only, where the sidebar's switcher stays beside a pushed page.
    @MainActor func testSwitchingClosesAPushedPage() throws {
        let app = launch(["-openAssistantPage", "profile"])
        guard app.windows.firstMatch.frame.width > 700 else { throw XCTSkip("the switcher beside a pushed page: regular width only") }
        // The profile's bar (its title Persona) with the way back: the page is pushed.
        let profile = app.navigationBars.matching(identifier: "Persona").firstMatch
        XCTAssertTrue(profile.waitForExistence(timeout: 20), "the profile did not open:\n\(app.debugDescription)")
        let header = switcherHeader(app)
        XCTAssertTrue(header.waitForExistence(timeout: 20), "no switcher in the sidebar's header:\n\(app.debugDescription)")
        header.tap()
        let work = element(app, "assistant-switcher-row-m-3fa21c")
        XCTAssertTrue(work.waitForExistence(timeout: 10), "the list did not open:\n\(app.debugDescription)")
        work.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: profile)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 10), .completed, "the profile stayed after switching:\n\(app.debugDescription)")
        shot(app, "switcher-switched-closes-page")
    }

    // MARK: -

    /// The app on the Persona page, its sidebar out, on the default assistant.
    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-app.tab", "assistant", "-openAssistantSidebar", "YES",
                               "-openAssistantManager", "assistant"] + extra
        app.launchPinned()
        // A fresh install asks for notifications first, and the app waits inactive (no roster) until it is answered.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 2) { springboard.buttons[label].tap() }
        return app
    }

    /// The sidebar's header. The drawer's identifier can cover its own on a phone (`drawerSettingsButton`): the
    /// default's name then.
    @MainActor private func switcherHeader(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "identifier == %@ OR (identifier == %@ AND label BEGINSWITH %@)",
                                         "assistant-switcher", "assistant-sidebar", "助手")).firstMatch
    }

    /// The Persona page's sidebar out, on the default assistant, with the switcher's list open.
    @MainActor private func openList(_ extra: [String] = []) -> XCUIApplication {
        let app = launch(extra)
        let header = switcherHeader(app)
        XCTAssertTrue(header.waitForExistence(timeout: 20), "no switcher in the sidebar's header:\n\(app.debugDescription)")
        header.tap()
        XCTAssertTrue(element(app, "assistant-switcher-new").waitForExistence(timeout: 10), "the list did not open:\n\(app.debugDescription)")
        return app
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
        let look = env["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name)-\(look).png"))
    }
}

/// The app as a launch on the default assistant's main conversation leaves it: the pick and the conversation on screen
/// are kept across launches, and a test class after one that switched or opened another conversation would start there.
/// `hub`: the mock hub to pair with (its main conversation is `server/b2e4d6f8-0000-4000-8000-000000000001`).
@MainActor enum AssistantUITestReset {
    static func toDefaultMain(hub: String?) {
        guard let hub else { return }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-app.tab", "assistant", "-openAssistantManager", "assistant",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001", "-openAssistantSidebar", "NO"]
        app.launchPinned()
        _ = app.buttons["assistant-sidebar-button"].waitForExistence(timeout: 20)
        sleep(3) // the roster read and `-openAssistantManager`'s pick
        app.terminate()
    }
}
