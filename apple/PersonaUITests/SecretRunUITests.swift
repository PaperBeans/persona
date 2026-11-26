import XCTest

/// The password card (secret_run) in Persona's conversation: the commands verbatim, Run only once a password is typed
/// (and only Run, not Return), the answer carrying exactly that password, and iOS never offering to save it.
///   PORT=18937 MOCK_SECRET_RUN=pending python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_SECRET_RUN_HUB=http://127.0.0.1:18937 xcodebuild test … -only-testing:PersonaUITests/SecretRunUITests
/// The env fixture runs separately: MOCK_SECRET_RUN=env and TEST_RUNNER_PERSONA_SECRET_RUN_ENV_HUB=<its URL>.
/// PERSONA_SHOTS_APPEARANCE=light|dark, PERSONA_SHOTS_LARGE=1 and TEST_RUNNER_PERSONA_SHOTS=/tmp/<dir> capture its
/// default/largest-text card in either appearance; no image is written by an ordinary test run.
final class SecretRunUITests: XCTestCase {
    /// The mock's main Persona conversation.
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    @MainActor func testRunSendsTheTypedPasswordOnce() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_SECRET_RUN_HUB"] else {
            throw XCTSkip("needs MOCK_SECRET_RUN=pending scripts/mock/mock_hub.py (PERSONA_SECRET_RUN_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main]
        app.launchPinned()

        let field = app.secureTextFields["interaction.field.password"]
        XCTAssertTrue(field.waitForExistence(timeout: 20), app.debugDescription)
        let commands = app.descendants(matching: .any)["secret-run.commands"]
        XCTAssertTrue(commands.exists, "the card does not list its commands")
        XCTAssertTrue(commands.label.contains("launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist"), commands.label)
        XCTAssertFalse(app.descendants(matching: .any)["secret-run.env-warning"].exists,
                       "default stdin delivery must not show the env warning")
        let run = app.buttons["secret-run.run"]
        XCTAssertFalse(run.isEnabled, "Run before a password is typed")

        field.tap()
        field.typeText("example-password\n") // Return puts the keyboard away; it does not run the commands
        Thread.sleep(forTimeInterval: 1)
        XCTAssertTrue(resolved(hub).isEmpty, "Return answered the card")
        XCTAssertTrue(run.isEnabled)
        run.tap()
        var answers: [[String: Any]] = []
        let deadline = Date().addingTimeInterval(5)
        while answers.isEmpty, Date() < deadline {
            answers = resolved(hub)
            if answers.isEmpty { Thread.sleep(forTimeInterval: 0.1) }
        }
        guard let row = answers.first else { return XCTFail("mock did not receive the card's answer") }
        let resolution = row["resolution"] as? [String: Any]
        XCTAssertEqual(resolution?["allow"] as? Bool, true)
        XCTAssertEqual(resolution?["values"] as? [String: String], ["password": "example-password"])
        XCTAssertTrue(field.waitForNonExistence(timeout: 5), "the card stayed after Run")
        XCTAssertEqual(resolved(hub).count, 1, "the card was answered more than once")
        PasswordPrompt.assertNone(app, "the password card's field")
    }

    /// A separate fixture run: every command comes from the fixture's raw tool_input, so this test cannot silently
    /// pass against the stdin card. No XCTSkip when the fixture is configured; its ordinary absence skips as other mock suites do.
    @MainActor func testEnvRunWarnsAndSendsTheTypedPasswordOnlyFromThisCard() throws {
        let env = ProcessInfo.processInfo.environment
        guard let hub = env["PERSONA_SECRET_RUN_ENV_HUB"] else {
            throw XCTSkip("needs MOCK_SECRET_RUN=env scripts/mock/mock_hub.py (PERSONA_SECRET_RUN_ENV_HUB)")
        }
        guard let input = pendingSecretRunInput(hub), input["delivery"] as? String == "env",
              let expectedCommands = input["commands"] as? [String], !expectedCommands.isEmpty else {
            XCTFail("MOCK_SECRET_RUN=env must provide ix_secret_run with delivery=env and non-sudo commands verbatim")
            return
        }

        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        let largest = env["PERSONA_SHOTS_LARGE"] == "1"
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main,
                               "-ui.scheme", appearance]
        if largest {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        }
        app.launchPinned()
        acceptIconAlert()

        let field = app.secureTextFields["interaction.field.password"]
        XCTAssertTrue(field.waitForExistence(timeout: 20), app.debugDescription)
        let warning = app.descendants(matching: .any)["secret-run.env-warning"]
        XCTAssertTrue(warning.waitForExistence(timeout: 5),
                      "env approval must warn that every listed command receives the typed secret: \(app.debugDescription)")
        XCTAssertTrue(warning.label.contains("PERSONA_SECRET"), warning.label)
        let commands = app.descendants(matching: .any)["secret-run.commands"]
        XCTAssertTrue(commands.exists, "the env card does not list its commands")
        for command in expectedCommands {
            XCTAssertTrue(commands.label.contains(command), "the env card dropped or rewrote: \(command)\n\(commands.label)")
        }
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let iconAlert = NSPredicate(format: "label CONTAINS[c] 'icon' OR label CONTAINS '图标'")
        for _ in 0..<3 {
            acceptIconAlert(timeout: 1)
            if !springboard.alerts.containing(iconAlert).firstMatch.exists { break }
        }
        XCTAssertFalse(springboard.alerts.containing(iconAlert).firstMatch.exists, "an icon-change alert covers the env card")
        shot(app, appearance: appearance, largest: largest)

        let run = app.buttons["secret-run.run"]
        XCTAssertFalse(run.isEnabled, "env Run before this card's password is typed")
        field.tap()
        field.typeText("example-env-password\n")
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: run)
        wait(for: [enabled], timeout: 5)
        XCTAssertTrue(resolved(hub).isEmpty, "Return answered the env card without a deliberate Run")
        run.tap()

        var answers: [[String: Any]] = []
        let deadline = Date().addingTimeInterval(5)
        while answers.isEmpty, Date() < deadline {
            answers = resolved(hub)
            if answers.isEmpty { Thread.sleep(forTimeInterval: 0.1) }
        }
        guard let row = answers.first else { return XCTFail("mock did not receive the env card's answer") }
        let resolution = row["resolution"] as? [String: Any]
        XCTAssertEqual(resolution?["allow"] as? Bool, true)
        XCTAssertEqual(resolution?["values"] as? [String: String], ["password": "example-env-password"])
        XCTAssertNil(resolution?["always"], "env approval must never be remembered")
        XCTAssertTrue(field.waitForNonExistence(timeout: 5), "the env card stayed after Run")
        XCTAssertEqual(resolved(hub).count, 1, "the env card was answered more than once")
        PasswordPrompt.assertNone(app, "the env password card's field")
    }

    /// The env fixture's pending card, straight from GET /api/v1/interactions.
    private func pendingSecretRunInput(_ hub: String) -> [String: Any]? {
        guard let data = try? Data(contentsOf: URL(string: hub + "/api/v1/interactions")!),
              let response = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let rows = response["interactions"] as? [[String: Any]],
              let card = rows.first(where: { $0["id"] as? String == "ix_secret_run" }) else { return nil }
        return card["tool_input"] as? [String: Any]
    }

    private func shot(_ app: XCUIApplication, appearance: String, largest: Bool) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let name = "secret-run-env-\(UITestStyle.name)-\(appearance)-\(largest ? "largest" : "default").png"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name))
    }

    private func resolved(_ hub: String) -> [[String: Any]] {
        guard let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
              let response = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let rows = response["resolved"] as? [[String: Any]] else { return [] }
        return rows.filter { $0["id"] as? String == "ix_secret_run" }
    }
}
