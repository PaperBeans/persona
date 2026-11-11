import XCTest

/// Settings › Connectors › Telegram against scripts/mock/mock_hub.py started with MOCK_TELEGRAM=off MOCK_TELEGRAM_2FA=1
/// (PERSONA_MOCK_HUB_TELEGRAM), as a hub has it at first: switched off, its program not run, so the hub lists no
/// capabilities for it. Switching it on, then signing in a step at a time (API credentials → phone → code, a wrong one
/// first → two-step password), then signing out. The mock is left on and signed out (its credentials gone too).
final class TelegramConnectorTests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testSignInAStepAtATime() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB_TELEGRAM"] else { throw XCTSkip("needs a mock with MOCK_TELEGRAM=off MOCK_TELEGRAM_2FA=1") }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", ""]
        app.launchPinned()
        let any = app.descendants(matching: .any)

        // From Settings (not -openConnectorTelegram: opened at launch, the page can beat the pairing): the hub's connectors
        // list Telegram under Connectors.
        let row = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Telegram")).firstMatch
        for _ in 0..<8 where !row.isHittable { app.swipeUp() }
        XCTAssertTrue(row.isHittable, "no Telegram row in 设置:\n\(app.debugDescription)")
        shot(app, "settings")
        row.tap()

        // Off: its switch, not 「登录在助手的配置里设置」 (the hub does not know yet that it signs in here).
        let toggle = app.switches["connector.enabled"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no switch while off:\n\(app.debugDescription)")
        XCTAssertEqual(toggle.value as? String, "0")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "manager.json")).firstMatch.exists, "says manager.json while off")
        shot(app, "off")
        // The switch itself, at the row's right end (the row's middle is its label).
        (toggle.switches.firstMatch.exists ? toggle.switches.firstMatch : toggle).coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()

        // On: its program starts (a moment of 「启动后就能登录」), then it signs in here.
        let signIn = any["connector.signIn"]
        XCTAssertTrue(signIn.waitForExistence(timeout: 20), "no 登录 Telegram once on:\n\(app.debugDescription)")
        XCTAssertEqual(toggle.value as? String, "1")
        XCTAssertFalse(any["connector.logout"].exists, "退出登录 before signing in")
        shot(app, "signed-out")
        signIn.tap()

        func fill(_ name: String, _ text: String) {
            let f = any["connector.field." + name]
            XCTAssertTrue(f.waitForExistence(timeout: 10), "no field \(name):\n\(app.debugDescription)")
            f.tap()
            f.typeText(text)
        }
        func next() {
            let b = any["connector.next"]
            XCTAssertTrue(b.isEnabled, "下一步 is off:\n\(app.debugDescription)")
            b.tap()
        }

        XCTAssertTrue(any["connector.field.api_id"].waitForExistence(timeout: 10), "no credentials step:\n\(app.debugDescription)")
        XCTAssertFalse(any["connector.next"].isEnabled, "下一步 with nothing filled in")
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "my.telegram.org")).firstMatch.exists, "no guide")
        shot(app, "credentials")
        fill("api_id", "1234567")
        fill("api_hash", "0123456789abcdef0123456789abcdef")
        next()

        fill("phone", "+15550100")
        shot(app, "phone")
        next()

        fill("code", "54321")
        next()
        let error = any["connector.signInError"]
        XCTAssertTrue(error.waitForExistence(timeout: 10), "a wrong code says nothing:\n\(app.debugDescription)")
        XCTAssertEqual(error.label, "验证码不对")
        shot(app, "code-wrong")
        // The wrong code stays in the field (the same step came back): put the cursor after it (the text is right-aligned),
        // rub it out, type the right one.
        let code = any["connector.field.code"]
        code.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        code.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 5) + "12345")
        XCTAssertEqual(code.value as? String, "12345")
        next()

        fill("password", "example-password")
        shot(app, "password")
        next()

        // Signed in: the account (Sign Out is there from the credentials on, so it says nothing).
        XCTAssertTrue(app.staticTexts["示例用户（@example）"].waitForExistence(timeout: 15), "not signed in:\n\(app.debugDescription)")
        let logout = any["connector.logout"]
        XCTAssertTrue(logout.exists)
        XCTAssertTrue(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "api_id 1234567")).firstMatch.exists, "no api_id")
        XCTAssertFalse(any["connector.signIn"].exists)
        shot(app, "signed-in")
        // api_id beside the hidden api_hash, then the two-step password: none of it is offered to Passwords (#1598).
        PasswordPrompt.assertNone(app, "Telegram's api_hash and password")

        logout.tap()
        // Once the dialog is up (its title), its own Sign Out (an action sheet or a popover, by size), not the row's.
        XCTAssertTrue(app.staticTexts["退出 Telegram 登录？"].waitForExistence(timeout: 5), "no confirmation:\n\(app.debugDescription)")
        let confirm = app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != %@", "退出登录", "connector.logout")).firstMatch
        XCTAssertTrue(confirm.exists, "no 退出登录 in the dialog:\n\(app.debugDescription)")
        shot(app, "logout-confirm")
        confirm.tap()
        XCTAssertTrue(signIn.waitForExistence(timeout: 15), "still signed in:\n\(app.debugDescription)")
    }
}
