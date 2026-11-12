import XCTest

/// Run against Fixtures/send_feedback_hub.py: HTTP acceptance in 150 ms, no transcript for ten seconds.
final class SendFeedbackUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        hub = try XCTUnwrap(ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"])
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let tree = XCTAttachment(string: app.debugDescription)
            tree.name = "send feedback tree"
            tree.lifetime = .keepAlways
            add(tree)
        }
        super.tearDown()
    }

    @MainActor private var field: XCUIElement {
        app.descendants(matching: .any)["shell-composer-field"].firstMatch
    }

    @MainActor private func openChat(assistant: Bool) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-AppleLanguages", "(zh-Hans)"]
        app.launchPinned()
        XCTAssertTrue(app.buttons["Persona"].waitForExistence(timeout: 25) || field.waitForExistence(timeout: 25))
        app.terminate()
        app.launchArguments = ["-pairURL", pair, "-AppleLanguages", "(zh-Hans)", "-holdChromeOpen", "YES",
                               "-dev.openChat", "", "-tab", assistant ? "assistant" : "dev",
                               "-app.tab", assistant ? "assistant" : "chats"]
        app.launchArguments += assistant
            ? ["-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"]
            : ["-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001"]
        app.launchPinned()
        XCTAssertTrue(field.waitForExistence(timeout: 25))
    }

    @MainActor private func send(_ text: String) -> Date {
        field.tap()
        field.typeText(text)
        let start = Date.now
        app.buttons["shell-composer-send"].firstMatch.tap()
        return start
    }

    @MainActor private func accepted(_ name: String, assistant: Bool) {
        openChat(assistant: assistant)
        let start = send("Check send feedback")
        let processing = app.staticTexts["处理中…"].firstMatch
        // waitForExistence polls too coarsely for a one-second presentation budget.
        while !processing.exists && Date.now.timeIntervalSince(start) < 12 { usleep(20_000) }
        let visible = processing.exists
        let seconds = Date.now.timeIntervalSince(start)
        print(String(format: "SEND_FEEDBACK %@ processing_visible=%@ seconds=%.3f", name, String(visible), seconds))
        XCTAssertTrue(visible, "acceptance must be visible before any model output")
        XCTAssertFalse(processing.frame.isEmpty, "the status must have a laid-out frame")
        // The host timing checker enforces one second from XCTest's synthesized input event.
        // tap() also waits for idle and resolves the button before generating that event.
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "\(name)-processing"
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let appearance = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "light"
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name)-\(appearance).png"))
            // Capture on the host while the app is foreground, not Springboard after XCTest cleanup.
            let url = URL(string: hub + "/mock/send-feedback-shot?name=\(name)-\(appearance)")!
            let data = try? Data(contentsOf: url)
            let response = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
            XCTAssertEqual(response?["ok"] as? Bool, true, "the assigned simulator screenshot was not captured")
        }
    }

    @MainActor func testAssistantAcceptanceBeforeOutput() { accepted("assistant", assistant: true) }
    @MainActor func testAgentAcceptanceBeforeOutput() { accepted("agent", assistant: false) }

    @MainActor func testSendingBeforeAcceptance() {
        openChat(assistant: false)
        _ = send("Slow feedback")
        XCTAssertTrue(app.staticTexts["正在发送…"].firstMatch.waitForExistence(timeout: 1))
        XCTAssertTrue(app.staticTexts["处理中…"].firstMatch.waitForExistence(timeout: 5))
    }

    @MainActor func testFailureRecovery() {
        openChat(assistant: false)
        _ = send("Fail feedback \(UUID().uuidString)")
        let failed = app.buttons["发送失败，重发或删除"].firstMatch
        XCTAssertTrue(failed.waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["处理中…"].firstMatch.exists, "a refused turn must not stay processing")
        failed.tap()
        let resend = app.buttons["重发"].firstMatch
        XCTAssertTrue(resend.waitForExistence(timeout: 3))
        resend.tap()
        XCTAssertTrue(app.staticTexts["处理中…"].firstMatch.waitForExistence(timeout: 2))
    }
}
