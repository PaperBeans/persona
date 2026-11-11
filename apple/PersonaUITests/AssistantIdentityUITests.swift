import XCTest

final class AssistantIdentityUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    @MainActor private func launch(_ hubKey: String, extra: [String] = []) throws -> XCUIApplication {
        let hub = try XCTUnwrap(env[hubKey], "Provide an isolated mock hub for this roster size")
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-openWelcome", "YES", "-demo.on", "NO", "-openChatsFresh", "YES", "-pairURL", pair, "-tab", "assistant", "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000002", "-dev.openChat", "", "-openAssistantSidebar", "NO"] + extra
        app.launchPinned()
        XCTAssertTrue(app.buttons["assistant-sidebar-button"].waitForExistence(timeout: 20), app.debugDescription)
        return app
    }

    @MainActor private func identityHeading(_ app: XCUIApplication) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "identifier == %@ AND label == %@", "assistant-sidebar", "Persona")).firstMatch
    }

    @MainActor func testSingleAssistantKeepsNavigationWithoutIdentity() throws {
        let app = try launch("PERSONA_IDENTITY_SINGLE_HUB")
        let sidebar = app.buttons["assistant-sidebar-button"]
        XCTAssertFalse((sidebar.value as? String ?? "").contains("Persona"))
        XCTAssertFalse(app.descendants(matching: .any).matching(identifier: "manager-switcher").firstMatch.exists)
        XCTAssertFalse(app.buttons["persona-avatar"].exists)
        sidebar.tap()
        XCTAssertTrue(app.drawerSettingsButton.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.drawerNewChatButton.exists)
        XCTAssertFalse(identityHeading(app).exists)
        XCTAssertFalse(app.buttons["sidebar-main"].label.contains("Persona"))
        XCTAssertTrue(app.buttons["sidebar-main"].label.contains("主对话"))
    }

    @MainActor private func shot(_ app: XCUIApplication, name: String) throws {
        acceptIconAlert(timeout: 3)
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let folder = env["PERSONA_SHOTS"] {
            try screenshot.pngRepresentation.write(to: URL(fileURLWithPath: folder).appending(path: name + ".png"))
        }
        if let folder = env["PERSONA_SIMCTL_HANDOFF"] {
            let root = URL(fileURLWithPath: folder)
            let ack = root.appending(path: name + ".ack")
            if FileManager.default.fileExists(atPath: ack.path) { try FileManager.default.removeItem(at: ack) }
            let ready = try JSONSerialization.data(withJSONObject: ["file": name + ".png"])
            try ready.write(to: root.appending(path: "ready.json"), options: .atomic)
            let expected = XCTNSPredicateExpectation(predicate: NSPredicate { @Sendable _, _ in
                FileManager.default.fileExists(atPath: ack.path)
            }, object: nil)
            let result = XCTWaiter.wait(for: [expected], timeout: 30)
            XCTAssertEqual(result, .completed, "The host must capture this exact page with simctl before continuing")
            if result != .completed { throw NSError(domain: "PersonaScreenshotHandoff", code: 1) }
        }
    }

    @MainActor func testVoiceAttributionAndCaptionsFollowRosterSize() throws {
        for (count, hubKey) in [("single", "PERSONA_IDENTITY_SINGLE_HUB"), ("multiple", "PERSONA_IDENTITY_MULTIPLE_HUB")] {
            for look in ["light", "dark"] {
                let chat = try launch(hubKey, extra: ["-ui.scheme", look])
                let body = chat.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "早上好")).firstMatch
                XCTAssertTrue(body.waitForExistence(timeout: 20), chat.debugDescription)
                let switcher = chat.descendants(matching: .any).matching(identifier: "manager-switcher").firstMatch
                if count == "multiple" { XCTAssertTrue(switcher.waitForExistence(timeout: 20)) } else { XCTAssertFalse(switcher.exists) }
                try shot(chat, name: "iphone-\(count)-chat-\(look)")
                chat.buttons["assistant-sidebar-button"].tap()
                XCTAssertTrue(chat.drawerSettingsButton.waitForExistence(timeout: 10), chat.debugDescription)
                if count == "single" { XCTAssertFalse(identityHeading(chat).exists) } else { XCTAssertTrue(identityHeading(chat).exists) }
                try shot(chat, name: "iphone-\(count)-sidebar-\(look)")
                chat.terminate()
                let app = try launch(hubKey, extra: ["-ui.scheme", look, "-showVoiceSample", "speaking"])
                let tail = app.descendants(matching: .any).matching(identifier: "voice-tail").firstMatch
                XCTAssertTrue(tail.waitForExistence(timeout: 20), app.debugDescription)
                try shot(app, name: "iphone-\(count)-voice-\(look)")
                app.buttons["voice-more"].tap()
                app.buttons["字幕全文"].tap()
                let captions = app.descendants(matching: .any).matching(identifier: "voice-transcript").firstMatch
                XCTAssertTrue(captions.waitForExistence(timeout: 10), app.debugDescription)
                let named = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "Persona"))
                if count == "single" { XCTAssertEqual(named.count, 0) } else { XCTAssertGreaterThan(named.count, 0) }
                try shot(app, name: "iphone-\(count)-captions-\(look)")
                app.terminate()
            }
        }
    }

    @MainActor func testMultipleAssistantsKeepIdentityAndSwitching() throws {
        let app = try launch("PERSONA_IDENTITY_MULTIPLE_HUB")
        let switcher = app.descendants(matching: .any).matching(identifier: "manager-switcher").firstMatch
        XCTAssertTrue(switcher.waitForExistence(timeout: 20), app.debugDescription)
        app.buttons["manager-switch-m-3fa21c"].tap()
        XCTAssertTrue(app.buttons["manager-switch-m-8b04d2"].exists)
        app.buttons["assistant-sidebar-button"].tap()
        XCTAssertTrue(app.drawerSettingsButton.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(identityHeading(app).exists)
        XCTAssertTrue(app.buttons["sidebar-main"].label.contains("Persona"))
    }
}
