import XCTest

/// Settings › About › Developing Persona (#9460, docs/operations/developing-persona.md): the switch, the rows it adds,
/// the question before turning it off with something recorded, and Export Diagnostics. Needs a mock hub in
/// `PERSONA_MOCK_HUB`; `PERSONA_SHOTS=<dir>` keeps `dev-mode-<1-off|2-on|3-confirm|4-export>-<light|dark>.png`.
final class DevModeUITests: XCTestCase {
    override func setUp() { continueAfterFailure = false }

    @MainActor private func launch(_ extra: [String] = []) throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("Needs a mock hub in PERSONA_MOCK_HUB")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", "", "-devMode.on", "NO",
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func element(_ id: String, _ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    @MainActor private func shot(_ name: String, _ look: String) {
        let png = XCUIScreen.main.screenshot().pngRepresentation
        let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        attachment.name = "dev-mode-\(name)-\(look)"
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("dev-mode-\(name)-\(look).png"))
    }

    /// One appearance a run (`PERSONA_SHOTS_APPEARANCE`, light by default; the run script sets the simulator's too).
    @MainActor func testSwitchExportAndTurnOff() throws {
        do {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] == "dark" ? "dark" : "light"
            XCUIDevice.shared.appearance = look == "dark" ? .dark : .light
            let app = try launch(["-openAbout", "YES"])
            let toggle = element("dev-mode-switch", app)
            XCTAssertTrue(toggle.waitForExistence(timeout: 30), "no Developing Persona switch:\n\(app.debugDescription)")
            XCTAssertFalse(element("dev-mode-export", app).exists, "export shows only while on")
            shot("1-off", look)

            toggle.switches.firstMatch.tap()
            XCTAssertTrue(element("dev-mode-export", app).waitForExistence(timeout: 10), "no export row after turning on")
            XCTAssertTrue(element("dev-mode-recorded", app).exists)
            // Something is recorded at once (the line saying recording started), so turning off asks first.
            let recorded = element("dev-mode-recorded", app)
            let sized = NSPredicate(format: "label CONTAINS %@", "·")
            expectation(for: sized, evaluatedWith: recorded)
            waitForExpectations(timeout: 10)
            shot("2-on", look)

            toggle.switches.firstMatch.tap()
            let alert = app.alerts.firstMatch
            XCTAssertTrue(alert.waitForExistence(timeout: 10), "no question before deleting what was recorded")
            XCTAssertTrue(alert.buttons["先导出…"].exists)
            XCTAssertTrue(alert.buttons["关闭并删除"].exists)
            shot("3-confirm", look)
            alert.buttons["先导出…"].tap()

            XCTAssertTrue(element("dev-export-summary", app).waitForExistence(timeout: 10), "Export First… did not open the export page")
            XCTAssertTrue(element("dev-export-logs", app).exists)
            XCTAssertTrue(element("dev-export-share", app).exists)
            shot("4-export", look)
            // Unticking a category and ticking it again.
            element("dev-export-errors", app).tap()
            XCTAssertFalse(element("dev-export-errors", app).isSelected)
            element("dev-export-errors", app).tap()
            XCTAssertTrue(element("dev-export-errors", app).isSelected)

            app.navigationBars.buttons.firstMatch.tap()
            XCTAssertTrue(toggle.waitForExistence(timeout: 10))
            toggle.switches.firstMatch.tap()
            XCTAssertTrue(app.alerts.firstMatch.waitForExistence(timeout: 10))
            app.alerts.firstMatch.buttons["关闭并删除"].tap()
            XCTAssertTrue(element("dev-mode-export", app).waitForNonExistence(timeout: 10), "still on after Turn Off and Delete")
            app.terminate()
        }
    }
}
