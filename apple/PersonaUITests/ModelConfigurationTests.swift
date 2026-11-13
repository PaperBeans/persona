import XCTest

/// Against the mock hub: common model choices, advanced providers, and a real PATCH through the editor.
final class ModelConfigurationTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let value = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py")
        }
        hub = value
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", ""] + extra
        app.launchPinned()
        return app
    }

    @MainActor func testSettingsOpensModelsBeforeAdvancedProviders() {
        let app = launch()
        let models = app.descendants(matching: .any)["settings-models"].firstMatch
        XCTAssertTrue(models.waitForExistence(timeout: 10))
        for _ in 0..<4 where !models.isHittable { app.swipeUp() }
        XCTAssertFalse(app.descendants(matching: .any)["settings-model-providers"].exists)
        models.tap()
        XCTAssertTrue(app.descendants(matching: .any)["model-purpose-assistant"].waitForExistence(timeout: 10))
        let providers = app.descendants(matching: .any)["settings-model-providers"].firstMatch
        for _ in 0..<4 where !providers.isHittable { app.swipeUp() }
        XCTAssertTrue(providers.exists)
        providers.tap()
        XCTAssertTrue(app.searchFields.firstMatch.waitForExistence(timeout: 10))
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    @MainActor func testFourCategoriesAndReadOnlyScope() {
        let app = launch(["-openModelConfiguration", "YES"])
        XCTAssertTrue(app.descendants(matching: .any)["model-purpose-assistant"].waitForExistence(timeout: 10))
        for id in ["assistant", "agent.new", "kb", "inbox.triage"] {
            XCTAssertTrue(app.descendants(matching: .any)["model-purpose-\(id)"].exists)
        }
        // The four categories and the background supervisor's use, which the hub answers for by its own id (#5083).
        XCTAssertTrue(app.descendants(matching: .any)["model-purpose-assistant.supervise"].exists)
        XCTAssertEqual(app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH %@", "model-purpose-")).count, 5)
        XCTAssertFalse(app.searchFields.firstMatch.exists)
        let drafts = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "草拟回复"))
        XCTAssertFalse(drafts.firstMatch.exists)
        let scope = app.buttons["model-config-scope"]
        XCTAssertTrue(scope.exists)
        scope.tap()
        XCTAssertTrue(drafts.firstMatch.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["草拟回复"].exists)
        XCTAssertFalse(app.descendants(matching: .any)["model-purpose-inbox.draft"].exists)
        addShot(app, "expanded")
        scope.tap()
        XCTAssertFalse(drafts.firstMatch.exists)
        addShot(app, "collapsed")
    }

    @MainActor private func addShot(_ app: XCUIApplication, _ state: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.lifetime = .keepAlways
        add(attachment)
        if let directory = ProcessInfo.processInfo.environment["PERSONA_MODEL_SHOTS"] {
            try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: directory).appendingPathComponent("\(state).png"))
        }
    }

    @MainActor func testPurposeSaveUsesTheUnifiedConfigurationEndpoint() throws {
        let app = launch(["-openModelConfiguration", "YES", "-openModelPurpose", "agent.new"])
        let requested = "model-ui-" + UUID().uuidString.prefix(8).lowercased()
        let list = app.descendants(matching: .any)["model-config-model-list"].firstMatch
        if list.waitForExistence(timeout: 10) {
            list.tap()
            let search = app.searchFields.firstMatch
            XCTAssertTrue(search.waitForExistence(timeout: 10))
            search.tap()
            search.typeText(requested)
            let choice = app.buttons["newChat.model." + requested]
            XCTAssertTrue(choice.waitForExistence(timeout: 10))
            choice.tap()
        } else {
            let field = app.textFields["model-config-model"]
            XCTAssertTrue(field.waitForExistence(timeout: 10))
            field.tap()
            if let current = field.value as? String, !current.contains("留空"), !current.isEmpty {
                field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count))
            }
            field.typeText(requested)
        }
        addShot(app, "editor")
        let save = app.buttons["model-config-save"]
        XCTAssertTrue(save.isEnabled)
        save.tap()
        let deadline = Date().addingTimeInterval(10)
        var model: String?
        repeat {
            let data = try Data(contentsOf: URL(string: hub + "/api/v1/model-config/purposes/agent.new")!)
            let result = try JSONSerialization.jsonObject(with: data) as? [String: Any]
            model = ((result?["purpose"] as? [String: Any])?["override"] as? [String: Any])?["model"] as? String
            if model != requested { usleep(100_000) }
        } while model != requested && Date() < deadline
        XCTAssertEqual(model, requested)
    }
}
