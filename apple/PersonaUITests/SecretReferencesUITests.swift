import XCTest

/// Secret references on the iPhone (#3962), against the mock hub's made-up provider (`MOCK_EXTENSIONS=1
/// MOCK_SECRET_HANDOFF=1`, `TEST_RUNNER_PERSONA_SECRET_REFERENCES_HUB`): Example Notes' API key holds a reference of
/// Example Vault, so its row says where the value comes from and its sheet opens with "Use a Reference" on and the
/// reference in the field; Example Vault's page lists an Always rule, which a swipe offers to remove and which goes
/// only after a confirm (cancelled here, so the mock keeps it). Light and dark; with `PERSONA_SHOTS`, a screenshot of
/// each.
final class SecretReferencesUITests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment

    @MainActor func testReferencedSecretItsSheetAndRemovingARule() throws {
        guard let hub = env["PERSONA_SECRET_REFERENCES_HUB"] else {
            throw XCTSkip("Needs MOCK_EXTENSIONS=1 MOCK_SECRET_HANDOFF=1 (PERSONA_SECRET_REFERENCES_HUB)")
        }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        for scheme in ["light", "dark"] {
            // The extension whose secret holds a reference: its row, then its sheet.
            let app = XCUIApplication()
            app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-ui.scheme", scheme, "-openExtension", "example-notes"]
            app.launchPinned()
            dismissSystemAlerts(wait: 4)
            // The page first; its rows below the fold exist only once scrolled to.
            XCTAssertTrue(app.switches["plugin-enabled"].waitForExistence(timeout: 20), app.debugDescription)
            let secret = app.buttons["plugin-secret-api_key"]
            for _ in 0..<10 where !(secret.exists && secret.isHittable) { app.swipeUp() }
            XCTAssertTrue(secret.waitForExistence(timeout: 5), app.debugDescription)
            XCTAssertTrue(secret.label.contains("Example Vault"), secret.label)
            shot(app, "secret-row-" + scheme)
            secret.tap()
            let useReference = app.switches["plugin-secret-use-reference"]
            XCTAssertTrue(useReference.waitForExistence(timeout: 10), app.debugDescription)
            XCTAssertEqual(useReference.value as? String, "1")
            XCTAssertTrue(app.textFields["plugin-secret-reference"].exists, app.debugDescription)
            shot(app, "secret-sheet-" + scheme)
            app.terminate()

            // The provider's page: removing an Always rule asks first.
            app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-ui.scheme", scheme, "-openExtension", "example-vault"]
            app.launchPinned()
            dismissSystemAlerts(wait: 2)
            XCTAssertTrue(app.switches["plugin-enabled"].waitForExistence(timeout: 20), app.debugDescription)
            let rule = app.descendants(matching: .any)["handoff-rule"].firstMatch
            for _ in 0..<10 where !(rule.exists && rule.isHittable) { app.swipeUp() }
            XCTAssertTrue(rule.waitForExistence(timeout: 5), app.debugDescription)
            rule.swipeLeft()
            let remove = app.buttons["移除"].firstMatch
            XCTAssertTrue(remove.waitForExistence(timeout: 5), app.debugDescription)
            remove.tap()
            let confirm = app.buttons["handoff-rule-remove-confirm"]
            XCTAssertTrue(confirm.waitForExistence(timeout: 5), app.debugDescription)
            shot(app, "rule-remove-confirm-" + scheme)
            // iOS 26 shows the dialog as a popover without a Cancel button: a tap outside it cancels.
            let cancel = app.buttons["取消"].firstMatch
            if cancel.waitForExistence(timeout: 2) {
                cancel.tap()
            } else {
                let outside = app.otherElements["PopoverDismissRegion"]
                XCTAssertTrue(outside.exists, app.debugDescription)
                outside.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.08)).tap()
            }
            XCTAssertTrue(confirm.waitForNonExistence(timeout: 5), app.debugDescription)
            XCTAssertTrue(rule.waitForExistence(timeout: 5), "a cancelled removal kept the rule")
            app.terminate()
        }
    }

    /// Answers the system's own questions (notifications, local network) so they never cover a screenshot.
    @MainActor private func dismissSystemAlerts(wait: TimeInterval = 1) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        var deadline = Date.now.addingTimeInterval(wait)
        while Date.now < deadline {
            let alert = springboard.alerts.firstMatch
            if alert.waitForExistence(timeout: 0.5) {
                for label in ["允许", "Allow", "好", "OK"] where alert.buttons[label].exists { alert.buttons[label].tap(); break }
                deadline = Date.now.addingTimeInterval(1.5) // another may follow
            }
        }
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("secret-references-" + name + ".png"))
        }
    }
}
