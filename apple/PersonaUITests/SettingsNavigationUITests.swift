import XCTest

final class SettingsNavigationUITests: XCTestCase {
    override func setUp() { continueAfterFailure = false }

    @MainActor private func launch(_ extra: [String] = []) throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_SETTINGS_HUB"] else {
            throw XCTSkip("Needs an isolated mock hub in PERSONA_SETTINGS_HUB")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", "", "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func element(_ id: String, _ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    @MainActor private func reveal(_ id: String, _ app: XCUIApplication) -> XCUIElement {
        let target = element(id, app)
        let list = element("settings-list", app)
        for _ in 0..<12 where !target.isHittable { list.swipeUp() }
        return target
    }

    @MainActor func testThreeGroupsAndCommonContainers() throws {
        let app = try launch()
        XCTAssertTrue(element("settings-list", app).waitForExistence(timeout: 30))
        XCTAssertTrue(element("settings-models", app).exists)
        XCTAssertTrue(element("settings-assistant", app).exists)
        XCTAssertTrue(element("settings-group-assistant", app).exists)
        XCTAssertTrue(reveal("settings-appearance", app).exists)
        XCTAssertTrue(element("settings-group-device", app).exists)
        XCTAssertTrue(reveal("settings-pairing", app).exists)
        XCTAssertTrue(element("settings-group-hub", app).exists)
        XCTAssertTrue(element("settings-persona", app).exists)
        XCTAssertFalse(element("settings-new-chat-check", app).exists)
    }

    @MainActor func testSearchAliasesOpenADevicePage() throws {
        let app = try launch()
        XCTAssertTrue(element("settings-list", app).waitForExistence(timeout: 30))
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText("听写")
        let result = element("settings-search-language", app)
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        result.tap()
        XCTAssertTrue(element("dictation-language", app).waitForExistence(timeout: 10))
    }

    @MainActor func testSearchDeveloperAliasesAndNoResults() throws {
        let app = try launch(["-openAppearance", "search:commit"])
        XCTAssertTrue(element("settings-search-about", app).waitForExistence(timeout: 30))
        element("settings-search-about", app).tap()
        XCTAssertTrue(element("settings-diagnostics", app).waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["构建"].exists)
        element("settings-diagnostics", app).tap()
        XCTAssertTrue(app.staticTexts["构建"].waitForExistence(timeout: 10))
    }

    @MainActor func testLegacySettingsPathsStillOpen() throws {
        let app = try launch(["-openTriageSettings", "YES"])
        XCTAssertTrue(element("triage-enabled", app).waitForExistence(timeout: 30))
    }
}
